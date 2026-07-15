// server.go
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RouteRegistrar mounts a domain's routes onto the versioned API group. Each
// domain's HTTP adapter implements it, so the kernel serves any domain without
// importing it — the seam that keeps this package domain-agnostic.
type RouteRegistrar interface {
	Register(rg *gin.RouterGroup)
}

// Option customizes the Server at construction.
type Option func(*Server)

// WithGroupMiddleware appends middleware to the versioned /v1 group, applied in
// order before the routes. The composition root builds the chain from the
// middleware package and passes it here — so the server stays a pure engine and
// never hardcodes a middleware. See internal/httpx/middleware.
func WithGroupMiddleware(mw ...gin.HandlerFunc) Option {
	return func(s *Server) { s.groupMiddleware = append(s.groupMiddleware, mw...) }
}

// WithRoutes registers one or more domain route registrars on the /v1 group.
// The kernel applies the middleware chain, then each registrar mounts its
// handlers — so the server serves any set of domains without importing them.
func WithRoutes(regs ...RouteRegistrar) Option {
	return func(s *Server) { s.registrars = append(s.registrars, regs...) }
}

// Server owns the gin engine and its HTTP lifecycle.
type Server struct {
	cfg             Config
	log             *slog.Logger
	ready           *Readiness
	registrars      []RouteRegistrar
	groupMiddleware []gin.HandlerFunc
	http            *http.Server
}

// NewServer builds the configured gin server. Routing is assembled here so the
// engine is ready before Start.
func NewServer(cfg Config, log *slog.Logger, ready *Readiness, opts ...Option) *Server {
	s := &Server{cfg: cfg, log: log, ready: ready}
	for _, opt := range opts {
		opt(s)
	}

	if cfg.Env == "dev" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := s.buildEngine()
	s.http = &http.Server{
		Handler:      engine,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	return s
}

// buildEngine assembles the router: health routes bypass the request chain;
// the /v1 group carries the full middleware stack.
func (s *Server) buildEngine() *gin.Engine {
	engine := gin.New()
	// Never trust client-supplied forwarding headers by default. Disabling can
	// only fail on a malformed CIDR, which nil cannot be — log defensively
	// rather than discard the error.
	if err := engine.SetTrustedProxies(nil); err != nil {
		s.log.Error("disabling trusted proxies", slog.String("error", err.Error()))
	}

	health := &healthHandler{ready: s.ready, version: s.cfg.Version}
	engine.GET("/healthz", health.healthz)
	engine.GET("/readyz", health.readyz)

	// The /v1 group carries the middleware chain assembled by the composition
	// root (order is the caller's responsibility); each registrar then mounts
	// its routes. Health routes above bypass the chain to keep probe traffic
	// out of metrics and logs.
	api := engine.Group("/v1")
	api.Use(s.groupMiddleware...)
	for _, r := range s.registrars {
		r.Register(api)
	}

	return engine
}

// Handler exposes the gin engine for in-process tests.
func (s *Server) Handler() http.Handler {
	return s.http.Handler
}

// Start binds the listener, flips readiness on, and serves until ctx is done or
// serving fails. Per the lifecycle.Component contract it blocks until ctx is
// cancelled and then returns nil — the actual drain is performed by Stop, which
// the runner calls next. A serve error (e.g. the port crashing) is returned so
// the runner can shut the process down.
func (s *Server) Start(ctx context.Context) error {
	addr := net.JoinHostPort("", strconv.Itoa(s.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	// Ready only once the listener is bound; startup is observed via /readyz and
	// request metrics, never an info log (ADR-6).
	s.ready.SetReady(true)

	serveErr := make(chan error, 1)
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("serving http: %w", err)
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

// Stop flips readiness off (so Kubernetes drains the pod) then drains in-flight
// requests, bounded by ctx.
func (s *Server) Stop(ctx context.Context) error {
	s.ready.SetReady(false)
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down http server: %w", err)
	}
	return nil
}
