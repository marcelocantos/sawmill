// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package proxy_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcelocantos/sawmill/proxy"
)

// startEchoHandshakeDaemon starts a minimal fake daemon that:
//  1. Reads the project root line.
//  2. Writes back a JSON status {"status":"ok",...}.
//  3. Echoes all subsequent bytes back verbatim (simulating MCP relay).
//
// Returns the socket path and a stop function. The returned stop function
// closes the listener and waits for the goroutine to exit.
func startEchoHandshakeDaemon(t *testing.T) (socketPath string, stop func()) {
	t.Helper()

	tmpDir := t.TempDir()
	socketPath = filepath.Join(tmpDir, "test.sock")

	// Listen before launching the goroutine so the socket is ready
	// immediately upon return.
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ln.Close()

		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read handshake line.
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			return
		}
		root := strings.TrimSpace(scanner.Text())

		// Write status JSON.
		status := map[string]interface{}{
			"status": "ok",
			"root":   root,
			"files":  0,
		}
		b, _ := json.Marshal(status)
		b = append(b, '\n')
		conn.Write(b) //nolint:errcheck

		// Echo the remaining bytes back so the proxy can relay them.
		buf := make([]byte, 4096)
		for {
			n, rdErr := conn.Read(buf)
			if n > 0 {
				conn.Write(buf[:n]) //nolint:errcheck
			}
			if rdErr != nil {
				return
			}
		}
	}()

	stop = func() {
		ln.Close()
		wg.Wait()
	}
	return socketPath, stop
}

// TestProxyConnectsAndRelays verifies that proxy.Run:
//   - connects to the daemon
//   - performs the handshake
//   - relays data from stdin→socket and socket→stdout
//
// The test uses real pipes for stdin/stdout substitution.
func TestProxyConnectsAndRelays(t *testing.T) {
	socketPath, stop := startEchoHandshakeDaemon(t)
	defer stop()

	// Create pipe pairs to simulate stdin and capture stdout.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe (stdin): %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe (stdout): %v", err)
	}

	// Redirect os.Stdin / os.Stdout for the duration of the test.
	origStdin := os.Stdin
	origStdout := os.Stdout
	os.Stdin = stdinR
	os.Stdout = stdoutW
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
	}()

	// Run proxy.Run in a goroutine — it will block on the relay.
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- proxy.Run(socketPath, t.TempDir())
	}()

	// Send a test message through stdin (→ socket → echo → stdout).
	payload := `{"jsonrpc":"2.0","method":"ping","id":1}` + "\n"
	fmt.Fprint(stdinW, payload)

	// Read the echoed message from stdout (in a separate goroutine with timeout).
	reader := bufio.NewReader(stdoutR)
	echoed := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		echoed <- line
	}()

	select {
	case line := <-echoed:
		if !strings.Contains(line, "ping") {
			t.Errorf("expected echoed payload to contain 'ping', got: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for echoed message from proxy")
	}

	// Close stdin to signal EOF; proxy.Run should terminate cleanly.
	stdinW.Close()
	stdoutW.Close()

	select {
	case err := <-proxyDone:
		if err != nil {
			t.Errorf("proxy.Run returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for proxy.Run to return")
	}
}
