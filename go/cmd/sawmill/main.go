// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// sawmill is the CLI entry point for the sawmill MCP server and daemon.
//
// Usage:
//
//	sawmill daemon [--socket PATH]   start the daemon
//	sawmill version                  print version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/marcelocantos/sawmill/daemon"
	mcpserver "github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/proxy"
)

// version is set by ldflags at build time.
var version = "dev"

// defaultSocketPath returns the default Unix socket path (~/.sawmill/sawmill.sock).
func defaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".sawmill/sawmill.sock"
	}
	return filepath.Join(home, ".sawmill", "sawmill.sock")
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sawmill <command> [options]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  serve     Start the MCP server (stdio transport)\n")
		fmt.Fprintf(os.Stderr, "  daemon    Start the sawmill daemon\n")
		fmt.Fprintf(os.Stderr, "  version   Print version and exit\n")
	}

	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		runServe(args)
	case "daemon":
		runDaemon(args)
	case "version":
		fmt.Printf("sawmill %s\n", version)
	case "--version", "-version":
		fmt.Printf("sawmill %s\n", version)
	case "--help", "-help", "help":
		flag.Usage()
	case "--help-agent", "-help-agent":
		printAgentHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		flag.Usage()
		os.Exit(1)
	}
}

// daemonRunning returns true if a daemon is already listening on socketPath.
func daemonRunning(socketPath string) bool {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	socketPath := fs.String("socket", defaultSocketPath(), "Unix socket path of the running daemon")
	rootPath := fs.String("root", "", "Project root to pass to the daemon (default: current directory)")
	fs.Parse(args) //nolint:errcheck // ExitOnError handles errors

	// Resolve project root.
	root := *rootPath
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolving working directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Expand ~ in socket path.
	sockPath := *socketPath
	if strings.HasPrefix(sockPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			sockPath = filepath.Join(home, sockPath[2:])
		}
	}

	if daemonRunning(sockPath) {
		// Proxy mode: relay MCP JSON-RPC between stdio and the daemon socket.
		if err := proxy.Run(sockPath, root); err != nil {
			fmt.Fprintf(os.Stderr, "proxy error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Fallback: no daemon running — serve in-process with a warning.
	fmt.Fprintf(os.Stderr, "warning: sawmill daemon not running on %s; falling back to in-process mode\n", sockPath)
	fmt.Fprintf(os.Stderr, "         Run 'sawmill daemon' to start the daemon for better performance.\n")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	s := mcpserver.NewServer()
	if err := s.Serve(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		os.Exit(1)
	}
}

func printAgentHelp() {
	fmt.Printf(`sawmill %s — MCP server for AST-level multi-language code transformations

COMMANDS
  serve               Start the MCP server (stdio transport). Reads JSON-RPC
                      on stdin, writes responses on stdout. Use this as the
                      command for MCP clients (e.g. Claude Desktop).

  daemon [--socket PATH]
                      Start the sawmill daemon. Listens on a Unix socket
                      (default: ~/.sawmill/sawmill.sock) and manages shared
                      state across multiple MCP sessions. Recommended for
                      long-running use via "brew services start sawmill".

  version             Print version and exit.

AGENT GUIDE
  See agents-guide.md (served as MCP instructions) for the full guide on
  available MCP tools, recipes, and conventions.
`, version)
}

func runDaemon(args []string) {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	socketPath := fs.String("socket", defaultSocketPath(), "Unix socket path for the daemon")
	fs.Parse(args) //nolint:errcheck // ExitOnError handles errors

	// Expand ~ if not already done by the flag default.
	path := *socketPath
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}

	if err := daemon.Start(path); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}
