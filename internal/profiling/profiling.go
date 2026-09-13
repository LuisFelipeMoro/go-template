// Package profiling serves the Go runtime profiles (net/http/pprof) on a
// listener of their own, separate from the API server.
//
// Security: pprof exposes heap contents, goroutine stacks, and the process
// command line. Mounting it on the public mux would put that behind whatever
// the API's middleware chain happens to allow; giving it a private listener
// makes reachability the control instead. The composition root only builds this
// when PPROF_ENABLED is set, config.Load refuses a non-loopback bind in
// production, and nothing here is ever registered on the gin engine. Reach it
// with `kubectl port-forward deploy/go-template 6060:6060`.
//
// One standing constraint follows from importing net/http/pprof: its init
// registers the profile handlers on http.DefaultServeMux. No server in this
// module may ever be given a nil handler (http.ListenAndServe(addr, nil)),
// because that serves the default mux and would publish profiling on whatever
// port it binds. Every server here passes an explicit Handler.
//
// Deleting it is a three-line change: drop this package, the PprofConfig block
// in internal/config, and the pprof lifecycle component in internal/cli.
package profiling

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"
)

// Timeouts for the admin listener. These are fixed rather than configurable:
// the listener is loopback-only and hand-driven, so there is no deployment that
// needs to tune them — but a server with no bounds at all is the Slowloris
// finding this template exists to avoid. The read side is tight; the write side
// is generous because a CPU profile legitimately streams for its full duration
// (`?seconds=30` is the common case, and go tool pprof allows more).
const (
	readHeaderTimeout = 2 * time.Second
	readTimeout       = 5 * time.Second
	writeTimeout      = 10 * time.Minute
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 1 << 20
)

// Server is a lifecycle.Component serving only /debug/pprof/ on its own
// address. Construct it with New; the zero value is not usable.
type Server struct {
	// mu guards addr, which Start rewrites to the resolved address once the
	// listener is bound while callers (tests, operators logging the port) may be
	// reading it from another goroutine.
	mu   sync.RWMutex
	addr string
	http *http.Server
}

// New builds the profiling server bound to addr ("host:port"). It registers the
// pprof handlers on a private mux — never on the application's router — so no
// API middleware, route, or metric is affected by enabling it.
func New(addr string) *Server {
	// A private mux, never http.DefaultServeMux. Importing net/http/pprof at all
	// registers these handlers on the default mux from its init — that cannot be
	// prevented, only made inert, which is why nothing in this module ever serves
	// http.DefaultServeMux. Registering explicitly here is what makes THIS
	// listener serve them; it is not what keeps them off the default one.
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return &Server{
		addr: addr,
		http: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		},
	}
}

// Addr reports the address the server is listening on. Before Start it is the
// configured address; after Start it is the resolved one, so a caller binding
// port 0 can discover the port the kernel chose. Safe from any goroutine.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// setAddr publishes the resolved listener address.
func (s *Server) setAddr(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addr = addr
}

// Start binds the listener and serves until ctx is done. Per the
// lifecycle.Component contract it returns nil on cancellation — Stop performs
// the drain — and returns a serve error so the runner can fail the process.
//
// Goroutine ownership: Start owns the serve goroutine; it ends when Stop closes
// the server, and reports on a buffered channel so it never blocks.
func (s *Server) Start(ctx context.Context) error {
	addr := s.Addr()

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	s.setAddr(ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("serving pprof: %w", err)
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErr:
		return err
	}
}

// Stop drains the profiling listener, bounded by ctx. It is idempotent.
func (s *Server) Stop(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down pprof server: %w", err)
	}
	return nil
}
