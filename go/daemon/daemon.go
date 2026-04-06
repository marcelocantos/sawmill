// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package daemon implements the sawmill daemon that manages a pool of
// CodebaseModel instances (one per project root) and serves them over a Unix
// domain socket. Each connection sends a project root path and then uses the
// connection for MCP protocol messages (🎯T11.4).
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
)

// Daemon manages a pool of CodebaseModel instances and a Unix socket listener.
type Daemon struct {
	mu         sync.RWMutex
	models     map[string]*model.CodebaseModel
	listener   net.Listener
	socketPath string
	done       chan struct{}
}

// New creates a new Daemon that will listen on socketPath.
func New(socketPath string) *Daemon {
	return &Daemon{
		models:     make(map[string]*model.CodebaseModel),
		socketPath: socketPath,
		done:       make(chan struct{}),
	}
}

// SetListener injects an already-opened listener. Useful for tests that open
// the socket before calling Serve so they can avoid races on socket creation.
func (d *Daemon) SetListener(ln net.Listener) {
	d.listener = ln
}

// GetOrLoadModel returns the cached model for root, loading it on first access.
func (d *Daemon) GetOrLoadModel(root string) (*model.CodebaseModel, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root %q: %w", root, err)
	}

	// Fast path: model already loaded.
	d.mu.RLock()
	m, ok := d.models[absRoot]
	d.mu.RUnlock()
	if ok {
		return m, nil
	}

	// Slow path: load the model under write lock.
	d.mu.Lock()
	defer d.mu.Unlock()

	// Re-check after acquiring write lock to avoid a double-load race.
	if m, ok = d.models[absRoot]; ok {
		return m, nil
	}

	m, err = model.Load(absRoot)
	if err != nil {
		return nil, fmt.Errorf("loading model for %q: %w", absRoot, err)
	}
	d.models[absRoot] = m
	return m, nil
}

// Shutdown closes all models and removes the socket file.
func (d *Daemon) Shutdown() {
	if d.listener != nil {
		d.listener.Close()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for root, m := range d.models {
		if err := m.Close(); err != nil {
			log.Printf("warning: closing model for %q: %v", root, err)
		}
		delete(d.models, root)
	}

	if d.socketPath != "" {
		os.Remove(d.socketPath)
	}

	select {
	case <-d.done:
		// already closed
	default:
		close(d.done)
	}
}

// statusResponse is the JSON envelope sent back after loading a model.
type statusResponse struct {
	Status string `json:"status"`
	Root   string `json:"root"`
	Files  int    `json:"files"`
	Error  string `json:"error,omitempty"`
}

// handleConn reads the project root from the first line of the connection,
// loads (or retrieves) the corresponding model, writes back a JSON status, and
// then keeps the connection open for future MCP protocol messages (🎯T11.4).
func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			log.Printf("reading root from %s: %v", conn.RemoteAddr(), err)
		}
		return
	}

	root := strings.TrimSpace(scanner.Text())
	if root == "" {
		writeStatus(conn, statusResponse{
			Status: "error",
			Error:  "empty project root",
		})
		return
	}

	m, err := d.GetOrLoadModel(root)
	if err != nil {
		log.Printf("loading model for %q: %v", root, err)
		writeStatus(conn, statusResponse{
			Status: "error",
			Root:   root,
			Error:  err.Error(),
		})
		return
	}

	writeStatus(conn, statusResponse{
		Status: "ok",
		Root:   m.Root,
		Files:  m.FileCount(),
	})

	// Serve MCP JSON-RPC over the connection until the client disconnects or
	// the daemon shuts down.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-d.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer cancel()

	srv := mcp.NewServerWithModel(m)
	if err := srv.ServeConn(ctx, conn); err != nil {
		log.Printf("MCP session for %q ended: %v", root, err)
	}
}

func writeStatus(conn net.Conn, resp statusResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("marshalling status: %v", err)
		return
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		log.Printf("writing status: %v", err)
	}
}

// Serve starts accepting connections on the already-opened listener. It blocks
// until the listener is closed.
func (d *Daemon) Serve() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			// A "use of closed network connection" error is expected when
			// Shutdown closes the listener; treat it as a clean stop.
			select {
			case <-d.done:
				return
			default:
			}
			// Check whether the error is due to the listener being closed.
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			log.Printf("accept error: %v", err)
			return
		}
		go d.handleConn(conn)
	}
}

// Start creates the socket directory, removes any stale socket, starts
// listening, blocks until SIGINT or SIGTERM, then shuts down cleanly.
func Start(socketPath string) error {
	// Expand ~ in socket path.
	expanded, err := expandHome(socketPath)
	if err != nil {
		return fmt.Errorf("expanding socket path: %w", err)
	}
	socketPath = expanded

	// Ensure the containing directory exists.
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	// Remove a stale socket from a previous run.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale socket %q: %w", socketPath, err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listening on %q: %w", socketPath, err)
	}

	d := New(socketPath)
	d.listener = ln

	log.Printf("sawmill daemon listening on %s", socketPath)

	// Handle OS signals for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Printf("shutting down")
		d.Shutdown()
	}()

	d.Serve()
	return nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[1:]), nil
}
