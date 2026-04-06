// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// sawmill is an MCP server for AST-level multi-language code transformations.
//
// Usage:
//
//	sawmill                          MCP stdio proxy (for MCP clients)
//	sawmill serve [--root PATH]      start a background daemon for a project
//	sawmill version                  print version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marcelocantos/mcpbridge"

	"github.com/marcelocantos/sawmill/daemon"
	"github.com/marcelocantos/sawmill/paths"
)

// version is set by ldflags at build time.
var version = "dev"

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sawmill [command] [options]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  (none)    MCP stdio proxy — auto-starts daemon if needed\n")
		fmt.Fprintf(os.Stderr, "  serve     Start a background daemon for a project\n")
		fmt.Fprintf(os.Stderr, "  version   Print version and exit\n")
	}

	// No args → MCP stdio mode.
	if len(os.Args) < 2 {
		runMCP(nil)
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		runServe(args)
	case "version", "--version", "-version":
		fmt.Printf("sawmill %s\n", version)
	case "help", "--help", "-help":
		flag.Usage()
	case "--help-agent", "-help-agent":
		printAgentHelp()
	default:
		// Anything else (including unknown flags) → MCP stdio mode.
		if strings.HasPrefix(cmd, "-") {
			runMCP(os.Args[1:])
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		flag.Usage()
		os.Exit(1)
	}
}

// resolveRoot resolves the project root from the flag or cwd.
func resolveRoot(rootFlag string) string {
	if rootFlag != "" {
		abs, err := filepath.Abs(rootFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolving root: %v\n", err)
			os.Exit(1)
		}
		return abs
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolving working directory: %v\n", err)
		os.Exit(1)
	}
	return root
}

// daemonRunning returns true if a daemon is listening on socketPath.
func daemonRunning(socketPath string) bool {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ensureDaemon starts a daemon for the given root if it isn't already running.
func ensureDaemon(sockPath, root string) {
	if daemonRunning(sockPath) {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot find own executable: %v\n", err)
		os.Exit(1)
	}
	cmd := exec.Command(exe, "serve", "--root", root)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	_ = cmd.Process.Release()

	for range 30 {
		time.Sleep(100 * time.Millisecond)
		if daemonRunning(sockPath) {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "daemon did not start within 3 seconds\n")
	os.Exit(1)
}

// runMCP is the default mode: MCP stdio proxy that connects to a per-project daemon.
func runMCP(args []string) {
	fs := flag.NewFlagSet("sawmill", flag.ExitOnError)
	rootPath := fs.String("root", "", "Project root (default: current directory)")
	fs.Parse(args) //nolint:errcheck

	root := resolveRoot(*rootPath)
	sockPath := paths.SocketPath(root)

	ensureDaemon(sockPath, root)

	if err := mcpbridge.RunProxy(context.Background(), mcpbridge.ProxyConfig{
		SocketPath: sockPath,
		ServerName: "sawmill",
		Version:    version,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Fprintf(os.Stderr, "hint: the daemon may have stopped — it will auto-start on next invocation\n")
		os.Exit(1)
	}
}

// runServe starts the background daemon for a specific project root.
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	rootPath := fs.String("root", "", "Project root (default: current directory)")
	fs.Parse(args) //nolint:errcheck

	root := resolveRoot(*rootPath)
	sockPath := paths.SocketPath(root)

	if err := daemon.Start(sockPath, root); err != nil {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		os.Exit(1)
	}
}

func printAgentHelp() {
	fmt.Printf(`sawmill %s — MCP server for AST-level multi-language code transformations

USAGE
  sawmill                   MCP stdio proxy. Reads JSON-RPC on stdin,
                            writes responses on stdout. Auto-starts the
                            background daemon if needed.

  sawmill serve             Start a background daemon for the current
                            project root. Listens on a per-project Unix
                            socket under ~/.sawmill/sockets/.

  sawmill version           Print version and exit.

FLAGS
  --root PATH               Project root (default: current directory)

AGENT GUIDE
  See agents-guide.md (embedded, also served via get_agent_prompt tool).
`, version)
}
