// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// sawmill is an HTTP MCP server for AST-level multi-language code
// transformations.
//
// Usage:
//
//	sawmill serve [--addr HOST:PORT]   start the HTTP MCP server
//	sawmill version                    print version and exit
//
// MCP clients connect via the streamable HTTP transport at /mcp.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/marcelocantos/sawmill/daemon"
	"github.com/marcelocantos/sawmill/paths"
)

// version is set by ldflags at build time.
var version = "dev"

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sawmill <command> [options]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  serve          Start the HTTP MCP server\n")
		fmt.Fprintf(os.Stderr, "  merge          AST-aware three-way merge (git mergetool driver)\n")
		fmt.Fprintf(os.Stderr, "  merge-driver   Git low-level merge driver (%%O %%A %%B %%P)\n")
		fmt.Fprintf(os.Stderr, "  version        Print version and exit\n")
	}

	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(exitError)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		runServe(args)
	case "merge":
		os.Exit(runMerge(args, os.Stderr))
	case "merge-driver":
		os.Exit(runMergeDriver(args, os.Stderr))
	case "version", "--version", "-version":
		fmt.Printf("sawmill %s\n", version)
	case "help", "--help", "-help":
		flag.Usage()
	case "--help-agent", "-help-agent":
		printAgentHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		flag.Usage()
		os.Exit(exitError)
	}
}

// runServe starts the HTTP MCP server.
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", paths.DefaultListenAddr, "HTTP listen address")
	fs.Parse(args) //nolint:errcheck

	srv := daemon.New(version)
	if err := srv.Start(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		os.Exit(1)
	}
}

func printAgentHelp() {
	fmt.Printf(`sawmill %s — HTTP MCP server for AST-level multi-language code transformations

USAGE
  sawmill serve [--addr HOST:PORT]
                            Start the HTTP MCP server. Default address is
                            %s. The streamable HTTP MCP transport is
                            served at /mcp.

  sawmill merge --base PATH --local PATH --remote PATH --output PATH \
                [--language NAME] [--marker-style {diff3,merge}]
                            AST-aware three-way merge (git mergetool driver).
                            Reads base/local/remote files, writes merged result
                            to --output. Exits 0 on clean merge, 1 if residual
                            conflicts remain, 2 on hard error.

  sawmill merge-driver BASE LOCAL REMOTE PATHNAME
                            Git low-level merge driver. Invoked as:
                              driver = sawmill merge-driver %%O %%A %%B %%P
                            Writes merged result back to LOCAL (%%A) in place.
                            Exits 0 on clean merge, 1 if residual conflicts, 2
                            on hard error.

  sawmill version           Print version and exit.

GIT INTEGRATION
  To configure sawmill as a mergetool and merge driver, add to ~/.gitconfig or
  .git/config:

    [mergetool "sawmill"]
        cmd = sawmill merge --base "$BASE" --local "$LOCAL" --remote "$REMOTE" --output "$MERGED"

    [merge "sawmill"]
        name = AST-aware merge
        driver = sawmill merge-driver %%O %%A %%B %%P

  Then enable it per file type in .gitattributes:

    *.py  merge=sawmill
    *.go  merge=sawmill

CLIENT INTEGRATION
  Sawmill speaks the MCP streamable HTTP transport. Stdio-based MCP clients
  (Claude Code, etc.) connect through a transparent gateway such as mcpbridge,
  which translates stdio → HTTP without altering the protocol.

  Each MCP session must call parse(path=...) once to bind the session to a
  project root. Subsequent tool calls re-use the loaded model. The server
  amortises parsing across sessions targeting the same root.

AGENT GUIDE
  See agents-guide.md (embedded, also served via get_agent_prompt tool).
`, version, paths.DefaultListenAddr)
}
