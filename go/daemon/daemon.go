// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package daemon implements the sawmill HTTP MCP server. A single long-running
// process listens on an HTTP address and serves the streamable HTTP MCP
// transport. Each MCP session gets its own *mcp.Handler with per-session
// pending changes/backups; project roots passed to parse are resolved through
// a shared modelpool.Pool so multiple sessions targeting the same root share
// one CodebaseModel.
package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"

	"github.com/marcelocantos/sawmill/mcp"
	"github.com/marcelocantos/sawmill/model"
	"github.com/marcelocantos/sawmill/modelpool"
)

const serverName = "sawmill"

// Server bundles the mcp-go MCPServer, its HTTP transport, the model pool,
// and the per-session handler registry.
type Server struct {
	mcp      *mcpsrv.MCPServer
	http     *mcpsrv.StreamableHTTPServer
	pool     *modelpool.Pool
	sessions *sessionRegistry
}

// sessionRegistry maps mcp-go session IDs to their per-session *mcp.Handler.
// Handlers are created lazily on first tool call from a session, and closed
// (releasing any borrowed model) when the session unregisters.
type sessionRegistry struct {
	mu       sync.Mutex
	handlers map[string]*mcp.Handler
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{handlers: make(map[string]*mcp.Handler)}
}

// get returns the handler for sessionID, creating one with the given loader
// on first access.
func (r *sessionRegistry) get(sessionID string, loader mcp.ModelLoader) *mcp.Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.handlers[sessionID]; ok {
		return h
	}
	h := mcp.NewHandlerWithLoader(loader)
	r.handlers[sessionID] = h
	return h
}

// remove closes and unregisters the handler for sessionID.
func (r *sessionRegistry) remove(sessionID string) {
	r.mu.Lock()
	h, ok := r.handlers[sessionID]
	if ok {
		delete(r.handlers, sessionID)
	}
	r.mu.Unlock()
	if h != nil {
		h.Close()
	}
}

// closeAll closes every handler in the registry.
func (r *sessionRegistry) closeAll() {
	r.mu.Lock()
	handlers := r.handlers
	r.handlers = make(map[string]*mcp.Handler)
	r.mu.Unlock()
	for _, h := range handlers {
		h.Close()
	}
}

// New constructs a Server with all sawmill tools registered. Call Start to
// listen on an HTTP address.
func New(version string) *Server {
	pool := modelpool.New()
	sessions := newSessionRegistry()

	loader := poolLoader(pool)

	hooks := &mcpsrv.Hooks{}
	hooks.AddOnUnregisterSession(func(_ context.Context, session mcpsrv.ClientSession) {
		sessions.remove(session.SessionID())
	})

	srv := mcpsrv.NewMCPServer(
		serverName,
		version,
		mcpsrv.WithToolCapabilities(false),
		mcpsrv.WithHooks(hooks),
	)

	resolve := func(ctx context.Context) *mcp.Handler {
		session := mcpsrv.ClientSessionFromContext(ctx)
		if session == nil {
			// Tool called outside any session — should not happen via HTTP,
			// but fall back to a transient handler.
			return mcp.NewHandlerWithLoader(loader)
		}
		return sessions.get(session.SessionID(), loader)
	}

	mcp.RegisterTools(srv, resolve)

	httpSrv := mcpsrv.NewStreamableHTTPServer(srv, mcpsrv.WithHeartbeatInterval(30*time.Second))

	return &Server{
		mcp:      srv,
		http:     httpSrv,
		pool:     pool,
		sessions: sessions,
	}
}

// poolLoader adapts a modelpool.Pool to the mcp.ModelLoader function type.
func poolLoader(pool *modelpool.Pool) mcp.ModelLoader {
	return func(root string) (*model.CodebaseModel, func(), error) {
		m, err := pool.Get(root)
		if err != nil {
			return nil, nil, err
		}
		return m, func() { pool.Release(root) }, nil
	}
}

// Start runs the HTTP server on addr (e.g. "127.0.0.1:8765"). Blocks until
// SIGINT or SIGTERM.
func (s *Server) Start(addr string) error {
	log.Printf("sawmill HTTP MCP server listening on http://%s/mcp", addr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.Start(addr)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		log.Printf("shutting down")
		return s.Shutdown()
	case err := <-errCh:
		return err
	}
}

// Shutdown stops the HTTP server and closes every session/model.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpErr := s.http.Shutdown(ctx)
	s.sessions.closeAll()
	s.pool.CloseAll()
	if httpErr != nil {
		return fmt.Errorf("shutting down HTTP server: %w", httpErr)
	}
	return nil
}

// MCPServer exposes the underlying mcp-go server for testing (e.g. building
// an in-process client).
func (s *Server) MCPServer() *mcpsrv.MCPServer { return s.mcp }

// Pool exposes the underlying model pool for testing.
func (s *Server) Pool() *modelpool.Pool { return s.pool }

// Definitions returns the tool definitions for introspection.
func Definitions() []mcpgo.Tool { return mcp.Definitions() }
