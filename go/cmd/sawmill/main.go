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
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marcelocantos/sawmill/daemon"
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

	if !daemonRunning(sockPath) {
		// Auto-start the daemon in the background.
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot find own executable: %v\n", err)
			os.Exit(1)
		}
		cmd := exec.Command(exe, "daemon", "--socket", sockPath)
		cmd.Stdout = nil
		cmd.Stderr = nil
		// Detach: the daemon runs independently of this process.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to start daemon: %v\n", err)
			os.Exit(1)
		}
		// Release the child so it isn't reaped with us.
		_ = cmd.Process.Release()

		// Wait for the daemon to become ready (up to 3 seconds).
		for range 30 {
			time.Sleep(100 * time.Millisecond)
			if daemonRunning(sockPath) {
				break
			}
		}
		if !daemonRunning(sockPath) {
			fmt.Fprintf(os.Stderr, "daemon did not start within 3 seconds\n")
			os.Exit(1)
		}
	}

	if err := proxy.Run(sockPath, root); err != nil {
		fmt.Fprintf(os.Stderr, "proxy error: %v\n", err)
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
