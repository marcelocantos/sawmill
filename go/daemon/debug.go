// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/marcelocantos/sawmill/fdusage"
)

// debugReadHeaderTimeout bounds header reads on the diagnostics listener.
const debugReadHeaderTimeout = 10 * time.Second

// StartDebugServer starts the diagnostics listener on addr, serving
// net/http/pprof. An empty addr disables it.
//
// This exists because of how the daemon fails. It runs for weeks, and when
// something wedges — a parse that never returns, a lock nobody releases — the
// evidence lives entirely in the running process. Restarting to enable
// diagnostics destroys exactly what needs diagnosing, so the endpoint has to
// already be there. `go tool pprof http://127.0.0.1:8766/debug/pprof/goroutine`
// then answers in seconds what otherwise takes symbolising a stripped binary.
//
// It binds loopback by default and is served on its own listener, separate
// from the MCP transport, so no profiling route is ever reachable through the
// MCP port.
func StartDebugServer(addr string) {
	if addr == "" {
		return
	}

	mux := http.NewServeMux()
	// Descriptor usage, so the question "is it about to wedge again?" has a
	// one-line answer that does not require attaching to the process.
	mux.HandleFunc("/debug/fds", func(w http.ResponseWriter, _ *http.Request) {
		u, err := fdusage.Read()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "%s\nover_budget %v\nbudget %.2f\n", u, u.OverBudget(), fdusage.Budget)
	})
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Losing diagnostics must never stop the daemon from serving. The
		// usual cause is a second sawmill already holding the port.
		log.Printf("sawmill: diagnostics endpoint unavailable on %s: %v", addr, err)
		return
	}

	log.Printf("sawmill: diagnostics at http://%s/debug/pprof/", addr)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: debugReadHeaderTimeout,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("sawmill: diagnostics endpoint stopped: %v", err)
		}
	}()
}
