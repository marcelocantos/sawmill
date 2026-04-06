// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package proxy implements a stdio-to-Unix-socket proxy for the sawmill MCP
// server. It connects to a running daemon, performs a project-root handshake,
// and then bidirectionally relays MCP JSON-RPC between the caller's
// stdin/stdout and the daemon socket.
package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/marcelocantos/sawmill/handshake"
)

// Run connects to the daemon Unix socket at socketPath, sends a JSON
// handshake (project root + binary hash), verifies the daemon accepted the
// connection, then relays MCP JSON-RPC between os.Stdin/os.Stdout and the
// socket until either direction reaches EOF or an error.
//
// Returns nil on a clean EOF from either side.
func Run(socketPath, projectRoot string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connecting to daemon at %q: %w", socketPath, err)
	}
	defer conn.Close()

	// Send JSON handshake with project root and binary hash.
	hs := handshake.Handshake{
		Root:       projectRoot,
		BinaryHash: handshake.BinaryHash(),
	}
	if err := json.NewEncoder(conn).Encode(hs); err != nil {
		return fmt.Errorf("sending handshake to daemon: %w", err)
	}

	// Read the daemon's JSON response line.
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading daemon response: %w", err)
	}

	var resp handshake.Response
	if err := json.Unmarshal([]byte(statusLine), &resp); err != nil {
		return fmt.Errorf("parsing daemon response %q: %w", statusLine, err)
	}
	if resp.Status == "error" {
		fmt.Fprintf(os.Stderr, "sawmill: %s\n", resp.Error)
		return fmt.Errorf("daemon: %s", resp.Error)
	}

	// Bidirectional relay: stdin→socket and socket→stdout concurrently.
	// The bufio.Reader may have buffered bytes from the socket after the status
	// line; drain those into stdout first by constructing a multi-reader.
	socketReader := io.MultiReader(reader, conn)

	var wg sync.WaitGroup
	wg.Add(2)

	var copyErr1, copyErr2 error

	// stdin → socket
	go func() {
		defer wg.Done()
		_, copyErr1 = io.Copy(conn, os.Stdin)
		// Signal the other direction by closing our write-side of the conn.
		// On a net.UnixConn this closes only the write end; on a generic
		// net.Conn we close the whole conn — the other goroutine will see EOF.
		if tc, ok := conn.(*net.UnixConn); ok {
			tc.CloseWrite() //nolint:errcheck
		} else {
			conn.Close() //nolint:errcheck
		}
	}()

	// socket → stdout
	go func() {
		defer wg.Done()
		_, copyErr2 = io.Copy(os.Stdout, socketReader)
	}()

	wg.Wait()

	// Treat EOF (nil) as a clean termination from either direction.
	if copyErr1 != nil {
		return fmt.Errorf("relaying stdin→socket: %w", copyErr1)
	}
	if copyErr2 != nil {
		return fmt.Errorf("relaying socket→stdout: %w", copyErr2)
	}
	return nil
}
