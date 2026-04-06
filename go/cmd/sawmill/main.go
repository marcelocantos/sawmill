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
	"os"
	"path/filepath"
	"strings"

	"github.com/marcelocantos/sawmill/daemon"
)

const version = "0.5.0"

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
	case "daemon":
		runDaemon(args)
	case "version":
		fmt.Printf("sawmill %s\n", version)
	case "--version", "-version":
		fmt.Printf("sawmill %s\n", version)
	case "--help", "-help", "help":
		flag.Usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		flag.Usage()
		os.Exit(1)
	}
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
