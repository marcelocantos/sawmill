// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::mpsc;
use std::time::{Duration, Instant};

use notify::{EventKind, RecursiveMode, Watcher, recommended_watcher};

/// Events the watcher can report.
pub enum FileEvent {
    Created(PathBuf),
    Modified(PathBuf),
    Removed(PathBuf),
}

/// Directories whose contents we never want to report events for.
const IGNORED_DIRS: &[&str] = &[
    ".git",
    "target",
    "node_modules",
    ".svn",
    ".hg",
    "dist",
    "build",
    "__pycache__",
];

/// Debounce window: collapse events for the same file within this duration.
const DEBOUNCE: Duration = Duration::from_millis(100);

/// Watches a directory tree for file changes, filtering to supported extensions.
pub struct FileWatcher {
    // Holds the notify watcher alive; dropping it stops watching.
    _watcher: Box<dyn Watcher + Send>,
    // Signals the background thread to stop.
    stop_tx: mpsc::SyncSender<()>,
}

impl FileWatcher {
    /// Start watching a directory. Returns a receiver for file events.
    ///
    /// Only reports events for files with extensions that have a registered
    /// language adapter. Respects common ignore directories (.git, target, …).
    pub fn watch(root: &Path) -> anyhow::Result<(Self, mpsc::Receiver<FileEvent>)> {
        let root = root.to_path_buf();
        if !root.exists() {
            anyhow::bail!("watch root does not exist: {}", root.display());
        }

        // Channel from notify → background thread.
        let (notify_tx, notify_rx) = mpsc::channel::<notify::Result<notify::Event>>();

        // Channel from background thread → caller.
        let (event_tx, event_rx) = mpsc::channel::<FileEvent>();

        // Stop signal channel (capacity 1 so send never blocks).
        let (stop_tx, stop_rx) = mpsc::sync_channel::<()>(1);

        let mut watcher = recommended_watcher(notify_tx)?;
        watcher.watch(&root, RecursiveMode::Recursive)?;

        // Background debounce + filter thread.
        std::thread::spawn(move || {
            // pending[path] = (earliest_kind, deadline)
            let mut pending: HashMap<PathBuf, (PendingKind, Instant)> = HashMap::new();

            loop {
                // Work out how long to wait: either until the earliest pending
                // deadline, or indefinitely if nothing is pending.
                let timeout = pending
                    .values()
                    .map(|(_, deadline)| deadline.saturating_duration_since(Instant::now()))
                    .min()
                    .unwrap_or(Duration::from_secs(60));

                match notify_rx.recv_timeout(timeout) {
                    Ok(Ok(event)) => {
                        // Check stop signal without blocking.
                        if stop_rx.try_recv().is_ok() {
                            break;
                        }

                        let kind = match event.kind {
                            EventKind::Create(_) => PendingKind::Created,
                            EventKind::Modify(_) => PendingKind::Modified,
                            EventKind::Remove(_) => PendingKind::Removed,
                            _ => {
                                flush_ready(&mut pending, &event_tx);
                                continue;
                            }
                        };

                        let deadline = Instant::now() + DEBOUNCE;
                        for path in event.paths {
                            if !is_relevant(&path) {
                                continue;
                            }
                            // For the same file, preserve the original kind but
                            // push the deadline forward.
                            pending
                                .entry(path)
                                .and_modify(|(_, d)| *d = deadline)
                                .or_insert((kind, deadline));
                        }
                    }
                    Ok(Err(e)) => {
                        // Notify reported a watcher error. Suppressed to avoid
                        // interfering with the stdio-based MCP JSON-RPC protocol.
                        // Non-critical; continue watching.
                        let _ = e;
                    }
                    Err(mpsc::RecvTimeoutError::Timeout) => {
                        // Check stop signal.
                        if stop_rx.try_recv().is_ok() {
                            break;
                        }
                        // Fall through to flush ready events.
                    }
                    Err(mpsc::RecvTimeoutError::Disconnected) => {
                        // Notify watcher dropped; drain pending and exit.
                        flush_ready(&mut pending, &event_tx);
                        break;
                    }
                }

                flush_ready(&mut pending, &event_tx);
            }
        });

        Ok((
            FileWatcher {
                _watcher: Box::new(watcher),
                stop_tx,
            },
            event_rx,
        ))
    }

    /// Stop watching and clean up the background thread.
    pub fn stop(self) {
        // Best-effort; if the thread already exited the send will fail silently.
        let _ = self.stop_tx.try_send(());
        // _watcher is dropped here, which stops notify and disconnects notify_rx,
        // causing the background thread to exit on the next iteration.
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Coarse event kind stored in the debounce map.
#[derive(Clone, Copy)]
enum PendingKind {
    Created,
    Modified,
    Removed,
}

/// Flush all pending entries whose deadline has passed.
fn flush_ready(
    pending: &mut HashMap<PathBuf, (PendingKind, Instant)>,
    tx: &mpsc::Sender<FileEvent>,
) {
    let now = Instant::now();
    pending.retain(|path, (kind, deadline)| {
        if *deadline <= now {
            let event = match kind {
                PendingKind::Created => FileEvent::Created(path.clone()),
                PendingKind::Modified => FileEvent::Modified(path.clone()),
                PendingKind::Removed => FileEvent::Removed(path.clone()),
            };
            // If the receiver hung up, stop emitting but keep draining.
            let _ = tx.send(event);
            false // remove from map
        } else {
            true // keep
        }
    });
}

/// Returns true if the path should be reported:
/// - not inside an ignored directory segment
/// - has an extension with a registered language adapter
fn is_relevant(path: &Path) -> bool {
    // Reject paths that contain any ignored directory component.
    for component in path.components() {
        if let std::path::Component::Normal(name) = component
            && let Some(s) = name.to_str()
            && IGNORED_DIRS.contains(&s)
        {
            return false;
        }
    }

    // Accept only extensions known to a language adapter.
    match path.extension().and_then(|e| e.to_str()) {
        Some(ext) => crate::adapters::adapter_for_extension(ext).is_some(),
        None => false,
    }
}
