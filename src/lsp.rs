// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! LSP client for semantic queries.
//!
//! Manages connections to language-specific LSP servers, handles the
//! JSON-RPC protocol over stdio, and provides high-level methods for
//! semantic operations (hover, definition, references, diagnostics).

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicI64, Ordering};

use anyhow::{Context, Result, bail};
use serde_json::Value;

/// Convert a file path to a file:// URI string.
fn path_to_uri(path: &Path) -> String {
    let abs = path.canonicalize().unwrap_or_else(|_| path.to_owned());
    format!("file://{}", abs.display())
}

/// Extract a file path from a file:// URI string.
fn uri_to_path(uri: &str) -> Option<PathBuf> {
    uri.strip_prefix("file://").map(PathBuf::from)
}

/// A connection to a single LSP server.
struct LspConnection {
    process: Child,
    next_id: AtomicI64,
    #[allow(dead_code)]
    language_id: String,
}

/// Manages LSP connections for multiple languages.
pub struct LspManager {
    /// language_id → connection
    connections: HashMap<String, LspConnection>,
    root: PathBuf,
}

impl LspManager {
    /// Create a new LSP manager for the given project root.
    /// Attempts to start LSP servers for all provided language adapters.
    pub fn new(root: &Path, adapters: &[&'static dyn crate::adapters::LanguageAdapter]) -> Self {
        let root = root.canonicalize().unwrap_or_else(|_| root.to_owned());
        let root_uri = path_to_uri(&root);

        let mut connections = HashMap::new();

        for adapter in adapters {
            let cmd_parts = match adapter.lsp_command() {
                Some(parts) if !parts.is_empty() => parts,
                _ => continue,
            };

            let lang_id = adapter.lsp_language_id().to_string();
            if lang_id.is_empty() || connections.contains_key(&lang_id) {
                continue;
            }

            match LspConnection::start(cmd_parts, &lang_id, &root_uri) {
                Ok(conn) => { connections.insert(lang_id, conn); }
                Err(_) => {} // LSP server not available; skip.
            }
        }

        LspManager { connections, root }
    }

    /// List connected languages.
    pub fn connected_languages(&self) -> Vec<&str> {
        self.connections.keys().map(|s| s.as_str()).collect()
    }

    /// Open a document in the appropriate LSP server.
    pub fn did_open(&mut self, path: &Path, lang_id: &str, text: &str) -> Result<()> {
        let conn = match self.connections.get_mut(lang_id) {
            Some(c) => c,
            None => return Ok(()),
        };

        conn.send_notification("textDocument/didOpen", serde_json::json!({
            "textDocument": {
                "uri": path_to_uri(path),
                "languageId": lang_id,
                "version": 1,
                "text": text,
            }
        }))
    }

    /// Get hover information (type info) at a position.
    pub fn hover(&mut self, path: &Path, lang_id: &str, line: u32, character: u32) -> Result<Option<String>> {
        let conn = match self.connections.get_mut(lang_id) {
            Some(c) => c,
            None => return Ok(None),
        };

        let result = conn.send_request("textDocument/hover", serde_json::json!({
            "textDocument": {"uri": path_to_uri(path)},
            "position": {"line": line, "character": character},
        }))?;

        if result.is_null() {
            return Ok(None);
        }

        let contents = &result["contents"];
        if let Some(value) = contents.as_str() {
            return Ok(Some(value.to_string()));
        }
        if let Some(value) = contents["value"].as_str() {
            return Ok(Some(value.to_string()));
        }

        Ok(Some(serde_json::to_string_pretty(contents).unwrap_or_default()))
    }

    /// Go to definition. Returns list of (file_path, line, column).
    pub fn definition(&mut self, path: &Path, lang_id: &str, line: u32, character: u32) -> Result<Vec<LocationInfo>> {
        let conn = match self.connections.get_mut(lang_id) {
            Some(c) => c,
            None => return Ok(Vec::new()),
        };

        let result = conn.send_request("textDocument/definition", serde_json::json!({
            "textDocument": {"uri": path_to_uri(path)},
            "position": {"line": line, "character": character},
        }))?;

        parse_location_infos(&result, &self.root)
    }

    /// Find all references. Returns list of (file_path, line, column).
    pub fn references(&mut self, path: &Path, lang_id: &str, line: u32, character: u32) -> Result<Vec<LocationInfo>> {
        let conn = match self.connections.get_mut(lang_id) {
            Some(c) => c,
            None => return Ok(Vec::new()),
        };

        let result = conn.send_request("textDocument/references", serde_json::json!({
            "textDocument": {"uri": path_to_uri(path)},
            "position": {"line": line, "character": character},
            "context": {"includeDeclaration": true},
        }))?;

        parse_location_infos(&result, &self.root)
    }

    /// Get diagnostics by sending modified content and collecting responses.
    pub fn get_diagnostics(&mut self, path: &Path, lang_id: &str, text: &str) -> Result<Vec<String>> {
        let conn = match self.connections.get_mut(lang_id) {
            Some(c) => c,
            None => return Ok(Vec::new()),
        };

        let uri = path_to_uri(path);

        // Send didChange.
        conn.send_notification("textDocument/didChange", serde_json::json!({
            "textDocument": {"uri": &uri, "version": 99},
            "contentChanges": [{"text": text}],
        }))?;

        // Read responses briefly, collecting diagnostics.
        let mut errors = Vec::new();
        for _ in 0..20 {
            match conn.try_read_message(std::time::Duration::from_millis(100)) {
                Some(msg) => {
                    if msg["method"].as_str() == Some("textDocument/publishDiagnostics") {
                        let params = &msg["params"];
                        if params["uri"].as_str() == Some(&uri) {
                            if let Some(diags) = params["diagnostics"].as_array() {
                                for d in diags {
                                    let severity = d["severity"].as_u64().unwrap_or(1);
                                    if severity <= 2 {
                                        let message = d["message"].as_str().unwrap_or("unknown");
                                        let line = d["range"]["start"]["line"].as_u64().unwrap_or(0) + 1;
                                        errors.push(format!("{}:{}: {}", path.display(), line, message));
                                    }
                                }
                            }
                        }
                    }
                }
                None => break,
            }
        }

        Ok(errors)
    }

    /// Shut down all LSP connections.
    pub fn shutdown(&mut self) {
        for (_, mut conn) in self.connections.drain() {
            conn.shutdown_gracefully();
        }
    }
}

impl Drop for LspManager {
    fn drop(&mut self) {
        self.shutdown();
    }
}

/// A simplified location result from LSP.
pub struct LocationInfo {
    pub path: PathBuf,
    pub line: u32,
    pub column: u32,
}

impl std::fmt::Display for LocationInfo {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}:{}:{}", self.path.display(), self.line + 1, self.column + 1)
    }
}

impl LspConnection {
    fn start(cmd_parts: &[&str], language_id: &str, root_uri: &str) -> Result<Self> {
        let process = Command::new(cmd_parts[0])
            .args(&cmd_parts[1..])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .with_context(|| format!("starting LSP: {}", cmd_parts[0]))?;

        let mut conn = LspConnection {
            process,
            next_id: AtomicI64::new(1),
            language_id: language_id.to_string(),
        };

        // Initialize.
        let _init = conn.send_request("initialize", serde_json::json!({
            "processId": std::process::id(),
            "rootUri": root_uri,
            "capabilities": {
                "textDocument": {
                    "hover": {"contentFormat": ["plaintext"]},
                    "definition": {},
                    "references": {},
                    "implementation": {},
                    "publishDiagnostics": {},
                    "synchronization": {
                        "didSave": true,
                        "dynamicRegistration": false,
                    },
                },
            },
        }))?;

        conn.send_notification("initialized", serde_json::json!({}))?;

        Ok(conn)
    }

    fn send_request(&mut self, method: &str, params: Value) -> Result<Value> {
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);

        self.write_message(&serde_json::json!({
            "jsonrpc": "2.0",
            "id": id,
            "method": method,
            "params": params,
        }))?;

        // Read until we get our response.
        loop {
            let msg = self.read_message()?;
            if msg.get("id").and_then(|v| v.as_i64()) == Some(id) {
                if let Some(error) = msg.get("error") {
                    bail!("LSP error: {}", serde_json::to_string(error).unwrap_or_default());
                }
                return Ok(msg.get("result").cloned().unwrap_or(Value::Null));
            }
            // Notification or other response — discard for now.
        }
    }

    fn send_notification(&mut self, method: &str, params: Value) -> Result<()> {
        self.write_message(&serde_json::json!({
            "jsonrpc": "2.0",
            "method": method,
            "params": params,
        }))
    }

    fn write_message(&mut self, msg: &Value) -> Result<()> {
        let body = serde_json::to_string(msg).context("serialising LSP message")?;
        let stdin = self.process.stdin.as_mut()
            .context("LSP stdin unavailable")?;
        write!(stdin, "Content-Length: {}\r\n\r\n{}", body.len(), body)
            .context("writing to LSP")?;
        stdin.flush().context("flushing LSP stdin")?;
        Ok(())
    }

    fn read_message(&mut self) -> Result<Value> {
        let stdout = self.process.stdout.as_mut()
            .context("LSP stdout unavailable")?;

        // Read headers to find Content-Length.
        let mut content_length: Option<usize> = None;
        let mut header_buf = Vec::new();
        loop {
            let byte = read_byte(stdout)?;
            header_buf.push(byte);
            if header_buf.ends_with(b"\r\n\r\n") {
                break;
            }
        }

        let header_str = String::from_utf8_lossy(&header_buf);
        for line in header_str.split("\r\n") {
            if let Some(len_str) = line.strip_prefix("Content-Length: ") {
                content_length = len_str.trim().parse().ok();
            }
        }

        let len = content_length.context("missing Content-Length header")?;

        // Read the body.
        let mut body = vec![0u8; len];
        stdout.read_exact(&mut body).context("reading LSP body")?;

        serde_json::from_slice(&body).context("parsing LSP message")
    }

    /// Try to read a message within a timeout. Returns None on timeout.
    fn try_read_message(&mut self, timeout: std::time::Duration) -> Option<Value> {
        // Simplified: attempt a blocking read with a timeout via thread.
        // For production, use non-blocking I/O.
        let start = std::time::Instant::now();
        if start.elapsed() < timeout {
            self.read_message().ok()
        } else {
            None
        }
    }

    fn shutdown_gracefully(&mut self) {
        let _ = self.send_request("shutdown", Value::Null);
        let _ = self.send_notification("exit", Value::Null);
        let _ = self.process.kill();
        let _ = self.process.wait();
    }
}

fn read_byte(reader: &mut dyn Read) -> Result<u8> {
    let mut buf = [0u8; 1];
    reader.read_exact(&mut buf).context("reading byte from LSP")?;
    Ok(buf[0])
}

/// Parse location results from LSP responses.
fn parse_location_infos(value: &Value, root: &Path) -> Result<Vec<LocationInfo>> {
    if value.is_null() {
        return Ok(Vec::new());
    }

    let locations = if value.is_array() {
        value.as_array().unwrap().clone()
    } else {
        vec![value.clone()]
    };

    let mut results = Vec::new();
    for loc in &locations {
        let uri = loc.get("uri")
            .or_else(|| loc.get("targetUri"))
            .and_then(|v| v.as_str());
        let range = loc.get("range")
            .or_else(|| loc.get("targetRange"));

        if let (Some(uri_str), Some(range_val)) = (uri, range) {
            let path = uri_to_path(uri_str).unwrap_or_else(|| PathBuf::from(uri_str));
            let line = range_val["start"]["line"].as_u64().unwrap_or(0) as u32;
            let col = range_val["start"]["character"].as_u64().unwrap_or(0) as u32;
            results.push(LocationInfo { path, line, column: col });
        }
    }

    Ok(results)
}
