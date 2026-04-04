// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

mod adapters;
mod forest;
mod rewrite;

use clap::{Parser, Subcommand};
use std::path::PathBuf;

#[derive(Parser)]
#[command(name = "polyrefactor", version, about = "AST-level multi-language refactoring tool")]
struct Cli {
    #[command(subcommand)]
    command: Command,
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
}

fn main() -> anyhow::Result<()> {
    let cli = Cli::parse();

    match cli.command {
        Command::Parse { path } => {
            let forest = forest::Forest::from_path(&path)?;
            println!("{forest}");
        }
        Command::Rename { from, to, path } => {
            let mut forest = forest::Forest::from_path(&path)?;
            let diff = forest.rename(&from, &to)?;
            print!("{diff}");
        }
    }

    Ok(())
}
