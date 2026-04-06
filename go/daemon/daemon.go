// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package daemon implements the sawmill daemon that serves a CodebaseModel
// for a single project root over a Unix domain socket using mcpbridge.
package daemon

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/marcelocantos/mcpbridge"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
)

// Daemon manages a CodebaseModel and an mcpbridge.Server for a single project.
type Daemon struct {
	model  *model.CodebaseModel
	server *mcpbridge.Server
}

// Start creates a daemon for the given project root, listens on socketPath,
// and blocks until SIGINT or SIGTERM.
func Start(socketPath, root string) error {
	// Ensure the containing directory exists.
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	// Load the project model.
	m, err := model.Load(root)
	if err != nil {
		return fmt.Errorf("loading model for %q: %w", root, err)
	}

	handler := mcp.NewHandlerWithModel(m)

	srv, err := mcpbridge.NewServer(mcpbridge.DaemonConfig{
		SocketPath: socketPath,
		Tools:      mcp.Definitions(),
		Handler:    handler,
	})
	if err != nil {
		m.Close()
		return fmt.Errorf("creating server: %w", err)
	}

	d := &Daemon{model: m, server: srv}

	log.Printf("sawmill daemon listening on %s (root: %s)", socketPath, root)

	// Handle OS signals for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Printf("shutting down")
		d.Shutdown()
	}()

	if err := srv.Serve(); err != nil {
		// A "use of closed" error after shutdown is expected.
		select {
		default:
			return err
		}
	}
	return nil
}

// Shutdown closes the server and model.
func (d *Daemon) Shutdown() {
	if d.server != nil {
		d.server.Close()
	}
	if d.model != nil {
		d.model.Close()
	}
}
