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

	"github.com/marcelocantos/sawmill/fdusage"
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

// SessionIdleTimeout is how long a session may go without a tool call before
// its handler is closed and its borrowed model released.
//
// The unregister hook is not sufficient on its own. A session is only
// unregistered when the transport says so, and a stdio bridge process that
// outlives the agent it was started for never says so — twenty such bridges,
// the oldest twelve days old, were once found each pinning a model and its
// watcher, which defeated the model pool's own idle eviction because the
// reference count never reached zero. Reaping on inactivity closes that gap
// without depending on a well-behaved client.
//
// Reaping a live-but-quiet session is cheap: the next tool call rebuilds the
// handler, and re-parsing is bounded by the store, so the cost is a slower
// first call rather than lost work.
const SessionIdleTimeout = 30 * time.Minute

// sessionSweepInterval is how often idle sessions are looked for.
const sessionSweepInterval = 2 * time.Minute

// sessionEntry is one session's handler plus the time of its last tool call.
type sessionEntry struct {
	handler  *mcp.Handler
	lastUsed time.Time
}

// sessionRegistry maps mcp-go session IDs to their per-session *mcp.Handler.
// Handlers are created lazily on first tool call from a session, and closed
// (releasing any borrowed model) when the session unregisters or falls idle.
type sessionRegistry struct {
	mu      sync.Mutex
	entries map[string]*sessionEntry
	// now is swappable so the reaper can be tested without waiting.
	now func() time.Time
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{entries: make(map[string]*sessionEntry), now: time.Now}
}

// get returns the handler for sessionID, creating one with the given loader
// on first access, and marks the session as active.
func (r *sessionRegistry) get(sessionID string, loader mcp.ModelLoader) *mcp.Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[sessionID]; ok {
		e.lastUsed = r.now()
		return e.handler
	}
	h := mcp.NewHandlerWithLoader(loader)
	r.entries[sessionID] = &sessionEntry{handler: h, lastUsed: r.now()}
	return h
}

// remove closes and unregisters the handler for sessionID.
func (r *sessionRegistry) remove(sessionID string) {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	if ok {
		delete(r.entries, sessionID)
	}
	r.mu.Unlock()
	if ok && e.handler != nil {
		e.handler.Close()
	}
}

// reapIdle closes every session with no tool call within timeout and returns
// how many it closed. Handlers are closed outside the lock so a slow model
// Close cannot stall live tool calls.
func (r *sessionRegistry) reapIdle(timeout time.Duration) int {
	r.mu.Lock()
	cutoff := r.now().Add(-timeout)
	var stale []*mcp.Handler
	for id, e := range r.entries {
		if e.lastUsed.Before(cutoff) {
			stale = append(stale, e.handler)
			delete(r.entries, id)
		}
	}
	r.mu.Unlock()

	for _, h := range stale {
		if h != nil {
			h.Close()
		}
	}
	return len(stale)
}

// len reports how many sessions are currently registered.
func (r *sessionRegistry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// closeAll closes every handler in the registry.
func (r *sessionRegistry) closeAll() {
	r.mu.Lock()
	entries := r.entries
	r.entries = make(map[string]*sessionEntry)
	r.mu.Unlock()
	for _, e := range entries {
		if e.handler != nil {
			e.handler.Close()
		}
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
		// Initialize-time instructions: agents see this without calling
		// get_agent_prompt. Keep short; full capability cards are languages.
		mcpsrv.WithInstructions(
			"Sawmill is an AST transform server. First call parse(path=...) to bind a project root. "+
				"Before renaming or adding fields in an unfamiliar language, call languages(language=<id or ext>) "+
				"for capability caveats (Bash rename is best-effort; SQL is dialect-agnostic; add_field is unavailable on Bash; "+
				"AST merge is full only for Python and Go). Call languages with no argument to list all supported languages. "+
				"Call get_agent_prompt for the full agent guide.",
		),
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

	stopReaper := make(chan struct{})
	defer close(stopReaper)
	go s.reapSessions(stopReaper)

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

// reapSessions closes sessions that have gone quiet, releasing the models
// they hold. See SessionIdleTimeout for why the unregister hook alone leaks.
func (s *Server) reapSessions(stop <-chan struct{}) {
	ticker := time.NewTicker(sessionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if n := s.sessions.reapIdle(SessionIdleTimeout); n > 0 {
				log.Printf("sawmill: reaped %d idle session(s)", n)
			}
			// Descriptor usage is checked on the same tick because the two
			// are related: sessions that never go away are what keep watchers
			// — and their descriptors — alive. Saying so in the log is the
			// whole point; the previous failure was silent right up until
			// accept() started failing.
			if u, err := fdusage.Read(); err == nil && u.OverBudget() {
				log.Printf("sawmill: descriptor usage over budget: %s", u)
			}
		}
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
