// server.go
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/luisfelipecoelho/go-template/internal/auth"
	"github.com/luisfelipecoelho/go-template/internal/cache"
	"github.com/luisfelipecoelho/go-template/internal/config"
	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/item/adapters"
	itemhttp "github.com/luisfelipecoelho/go-template/internal/item/http"
	"github.com/luisfelipecoelho/go-template/internal/lifecycle"
	"github.com/luisfelipecoelho/go-template/internal/messaging"
	"github.com/luisfelipecoelho/go-template/internal/middleware"
	"github.com/luisfelipecoelho/go-template/internal/profiling"
	"github.com/luisfelipecoelho/go-template/internal/telemetry"
	"github.com/luisfelipecoelho/go-template/internal/web"
)

// eventsTopic is the messaging topic the item domain publishes to.
const eventsTopic = "items"

// newServerCmd returns the HTTP server command.
func newServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Run the HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading server config: %w", err)
			}
			return runServer(cmd.Context(), cfg)
		},
	}
}

// runServer is the composition root for the HTTP process: it constructs every
// concrete dependency, injects interfaces into constructors, registers the
// components with the lifecycle Runner, and runs until shutdown. Registration
// order is start order; stop runs in reverse — so telemetry (registered first)
// flushes last, after the HTTP server has drained.
func runServer(ctx context.Context, cfg config.Config) error {
	obs, err := newObservability(ctx, cfg)
	if err != nil {
		return fmt.Errorf("bootstrapping observability: %w", err)
	}
	log, tel := obs.log, obs.tel

	httpMetrics, err := telemetry.NewHTTPMetrics(tel.Meter)
	if err != nil {
		return fmt.Errorf("building http metrics: %w", err)
	}

	storer, cacheCloser, err := buildStorer(ctx, cfg, log)
	if err != nil {
		return err
	}

	publisher, _, busCloser, err := messaging.New(cfg.Messaging.Driver)
	if err != nil {
		return fmt.Errorf("building messaging: %w", err)
	}

	svc := item.NewService(log, storer, adapters.NewPublisher(publisher, eventsTopic))

	chain, err := buildMiddleware(cfg, log, httpMetrics, tel)
	if err != nil {
		return err
	}

	server := buildHTTPServer(cfg, log, web.NewReadiness(), chain, svc)

	runner := lifecycle.NewRunner(log, cfg.ShutdownTimeout)
	registerServerComponents(runner, cfg, tel, server, busCloser, cacheCloser)

	if err := runner.Run(ctx); err != nil {
		return fmt.Errorf("running server lifecycle: %w", err)
	}
	return nil
}

// buildHTTPServer translates the HTTP config into the transport and mounts the
// routes. The kernel is domain-agnostic: each bounded context's HTTP adapter
// registers its own routes onto the /v1 group, so adding a context means
// constructing its service and passing its handler here — the server never
// learns item internals.
func buildHTTPServer(
	cfg config.Config,
	log *slog.Logger,
	ready *web.Readiness,
	chain []gin.HandlerFunc,
	svc *item.Service,
) *web.Server {
	return web.NewServer(web.Config{
		Port:              cfg.HTTPPort,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		Env:               cfg.Env,
		Version:           version,
	}, log, ready,
		web.WithGroupMiddleware(chain...),
		web.WithRoutes(itemhttp.NewHandler(svc)),
	)
}

// buildStorer constructs the item store and, when a cache driver is configured,
// the read-through decorator in front of it. The cache sits ALONGSIDE the
// store — it never replaces it — and CACHE_DRIVER=none skips the decorator
// entirely, so the disabled path costs nothing.
//
// The returned Closer is always non-nil so the caller's lifecycle component can
// call Close unconditionally; with no cache it is a no-op.
func buildStorer(ctx context.Context, cfg config.Config, log *slog.Logger) (item.Storer, io.Closer, error) {
	var storer item.Storer = adapters.NewDatabase()

	if cfg.Cache.Driver == "" || cfg.Cache.Driver == "none" {
		return storer, noopCloser{}, nil
	}

	c, closer, err := cache.New(ctx, cache.Config{
		Driver: cfg.Cache.Driver,
		Redis: cache.RedisConfig{
			Addr:     cfg.Cache.RedisAddr,
			Password: cfg.Cache.RedisPassword,
			DB:       cfg.Cache.RedisDB,
			TLS:      cfg.Cache.RedisTLS,
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("building cache: %w", err)
	}
	return adapters.NewCache(storer, c, cfg.Cache.TTL, log), closer, nil
}

// buildMiddleware assembles the /v1 chain. Order is load-bearing and decided
// here, in one place: the server applies the slice verbatim and never hardcodes
// a middleware of its own.
//
//	RequestID        first, so the correlation id reaches every layer below
//	SecurityHeaders  applies to every response, including the rejections below
//	Telemetry        high, so it observes 401/429/503 and recovered 500s
//	Recovery         converts a panic into a 500 the layers above can see
//	Throttle         sheds load before any handler work is done
//	otelgin          traces what survived admission control
//	Auth             rejects before the handler deadline starts
//	Timeout          after auth/throttle so rejected requests are not charged
//	                 against it, before BodyLimit so it also covers decoding
//	BodyLimit        last, immediately in front of the routes
func buildMiddleware(
	cfg config.Config,
	log *slog.Logger,
	httpMetrics *telemetry.HTTPMetrics,
	tel *telemetry.Providers,
) ([]gin.HandlerFunc, error) {
	chain := []gin.HandlerFunc{
		middleware.RequestID(),
		middleware.SecurityHeaders(),
		middleware.Telemetry(log, httpMetrics),
		middleware.Recovery(log),
	}

	if cfg.Throttle.Enabled {
		chain = append(chain, middleware.Throttle(middleware.NewSemaphore(cfg.Throttle.MaxInFlight)))
	}
	if cfg.Otel.Enabled {
		// Inject the tracer provider explicitly rather than letting otelgin read
		// the global.
		chain = append(chain, otelgin.Middleware(cfg.Otel.ServiceName, otelgin.WithTracerProvider(tel.Tracer)))
	}
	if cfg.Auth.Enabled {
		authr, err := auth.NewStaticKeys(cfg.Auth.APIKeys...)
		if err != nil {
			return nil, fmt.Errorf("building authenticator: %w", err)
		}
		chain = append(chain, middleware.Auth(authr))
	}

	return append(chain,
		middleware.Timeout(cfg.HandlerTimeout),
		middleware.BodyLimit(cfg.MaxBodyBytes),
	), nil
}

// registerServerComponents wires the lifecycle graph. Registration order is
// start order and the REVERSE of stop order, which is the only thing that makes
// the shutdown sequence correct — read it bottom-up to see the drain:
//
//	http drains in-flight requests → cache closes → bus closes → telemetry
//	flushes → pprof (if enabled) finally goes away.
func registerServerComponents(
	runner *lifecycle.Runner,
	cfg config.Config,
	tel *telemetry.Providers,
	server *web.Server,
	busCloser, cacheCloser io.Closer,
) {
	// Registered first → stopped last. A hung shutdown is exactly when a
	// goroutine dump is worth having, so the profiling listener outlives every
	// component it might be used to diagnose. It exists only when PPROF_ENABLED
	// is set, is never mounted on the API mux, and config.Load refuses a
	// non-loopback bind in production.
	if cfg.Pprof.Enabled {
		pp := profiling.New(cfg.Pprof.Addr)
		runner.Add(lifecycle.Component{Name: "pprof", Start: pp.Start, Stop: pp.Stop})
	}
	// Flush telemetry after everything it might still be recording has drained.
	runner.Add(lifecycle.Component{
		Name:  "telemetry",
		Start: noStart,
		Stop:  tel.Shutdown,
	})
	// Bus is a stop-only resource; Close unblocks any consumer, after the HTTP
	// server has drained but before the telemetry flush.
	runner.Add(lifecycle.Component{
		Name:  "bus",
		Start: noStart,
		Stop:  func(context.Context) error { return busCloser.Close() },
	})
	// Closed after the HTTP server drains so in-flight requests keep their cache.
	runner.Add(lifecycle.Component{
		Name:  "cache",
		Start: noStart,
		Stop:  func(context.Context) error { return cacheCloser.Close() },
	})
	runner.Add(lifecycle.Component{
		Name:  "http",
		Start: server.Start,
		Stop:  server.Stop,
	})
}

// noStart is the Start of a stop-only component: it owns a resource to release
// on shutdown but has no work of its own to run.
func noStart(context.Context) error { return nil }
