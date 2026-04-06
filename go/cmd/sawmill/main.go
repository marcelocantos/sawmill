// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// sawmill is an MCP server for AST-level multi-language code transformations.
//
// Usage:
//
//	sawmill                          MCP stdio server (for MCP clients)
//	sawmill serve [--socket PATH]    start the background daemon
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
		fmt.Fprintf(os.Stderr, "Usage: sawmill [command] [options]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  (none)    MCP stdio server — auto-starts daemon if needed\n")
		fmt.Fprintf(os.Stderr, "  serve     Start the background daemon (for brew services)\n")
		fmt.Fprintf(os.Stderr, "  version   Print version and exit\n")
	}

	// No args or first arg starts with "-" → MCP stdio mode.
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		runMCP(os.Args[1:])
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		runServe(args)
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

// daemonRunning returns true if a daemon is listening on socketPath.
func daemonRunning(socketPath string) bool {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// expandSocket expands ~ and returns the resolved socket path.
func expandSocket(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// ensureDaemon starts the daemon if it isn't already running.
func ensureDaemon(sockPath string) {
	if daemonRunning(sockPath) {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot find own executable: %v\n", err)
		os.Exit(1)
	}
	cmd := exec.Command(exe, "serve", "--socket", sockPath)
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

// runMCP is the default mode: MCP stdio server that proxies to the daemon.
func runMCP(args []string) {
	fs := flag.NewFlagSet("sawmill", flag.ExitOnError)
	socketPath := fs.String("socket", defaultSocketPath(), "Unix socket path of the daemon")
	rootPath := fs.String("root", "", "Project root (default: current directory)")
	fs.Parse(args) //nolint:errcheck

	root := *rootPath
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolving working directory: %v\n", err)
			os.Exit(1)
		}
	}

	sockPath := expandSocket(*socketPath)
	ensureDaemon(sockPath)

	if err := proxy.Run(sockPath, root); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runServe starts the background daemon (for brew services or manual use).
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	socketPath := fs.String("socket", defaultSocketPath(), "Unix socket path")
	fs.Parse(args) //nolint:errcheck

	if err := daemon.Start(expandSocket(*socketPath)); err != nil {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		os.Exit(1)
	}
}

func printAgentHelp() {
	fmt.Printf(`sawmill %s — MCP server for AST-level multi-language code transformations

USAGE
  sawmill                   MCP stdio server. Reads JSON-RPC on stdin,
                            writes responses on stdout. Auto-starts the
                            background daemon if needed.

  sawmill serve             Start the background daemon. Listens on a Unix
                            socket (default: ~/.sawmill/sawmill.sock).
                            Use "brew services start sawmill" for auto-start.

  sawmill version           Print version and exit.

FLAGS
  --socket PATH             Unix socket path (default: ~/.sawmill/sawmill.sock)
  --root PATH               Project root (default: current directory)

AGENT GUIDE
  See agents-guide.md (embedded, also served via get_agent_prompt tool).
`, version)
}
