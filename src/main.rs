// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Many public APIs are defined ahead of their call sites (planned features
// documented in docs/design.md and docs/frontier.md). Suppress warnings
// crate-wide rather than annotating each stub individually.
#![allow(dead_code)]

mod adapters;
mod codegen;
mod exemplar;
mod forest;
mod index;
mod js_engine;
mod lsp;
mod mcp;
mod model;
mod rewrite;
mod store;
mod transform;
mod watcher;

use clap::{CommandFactory, Parser, Subcommand};
use std::path::PathBuf;

const AGENT_GUIDE: &str = include_str!("../agents-guide.md");

#[derive(Parser)]
#[command(name = "canopy", version, about = "Codebase operations platform")]
struct Cli {
    /// Print help text followed by the agent guide.
    #[arg(long)]
    help_agent: bool,

    #[command(subcommand)]
    command: Option<Command>,
}

#[derive(Subcommand)]
enum Command {
    /// Parse files and display forest summary.
    Parse {
        /// Path to parse (file or directory).
        #[arg(default_value = ".")]
        path: PathBuf,
    },

    /// Rename a symbol.
    Rename {
        /// Current symbol name.
        from: String,
        /// New symbol name.
        to: String,
        /// Path scope.
        #[arg(long, default_value = ".")]
        path: PathBuf,
    },

    /// Run as an MCP server over stdio.
    Serve,
}

fn main() -> anyhow::Result<()> {
    let cli = Cli::parse();

    if cli.help_agent {
        let mut cmd = Cli::command();
        cmd.print_help()?;
        println!("\n\n{AGENT_GUIDE}");
        return Ok(());
    }

    let command = match cli.command {
        Some(cmd) => cmd,
        None => {
            Cli::command().print_help()?;
            return Ok(());
        }
    };

    match command {
        Command::Parse { path } => {
            let forest = forest::Forest::from_path(&path)?;
            println!("{forest}");
        }
        Command::Rename { from, to, path } => {
            let forest = forest::Forest::from_path(&path)?;
            let diff = forest.rename_diff(&from, &to)?;
            print!("{diff}");
        }
        Command::Serve => {
            tokio::runtime::Builder::new_multi_thread()
                .enable_all()
                .build()?
                .block_on(mcp::serve())?;
        }
    }

    Ok(())
}
