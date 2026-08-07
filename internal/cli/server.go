// server.go
package cli

import (
	"context"
	"fmt"
	"io"

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

	// Wiring: explicit constructor DI. The store is the in-memory default;
	// a cache (Redis/in-memory) fronts it when CACHE_DRIVER is set — it sits
	// alongside the database, so both run at once. "none" skips the decorator
	// entirely (zero overhead).
	var storer item.Storer = adapters.NewDatabase()
	// Stays a no-op unless a cache driver is configured, so the lifecycle
	// component below can call Close unconditionally.
	var cacheCloser io.Closer = noopCloser{}
	if cfg.Cache.Driver != "" && cfg.Cache.Driver != "none" {
		c, closer, cErr := cache.New(ctx, cache.Config{
			Driver: cfg.Cache.Driver,
			Redis: cache.RedisConfig{
				Addr:     cfg.Cache.RedisAddr,
				Password: cfg.Cache.RedisPassword,
				DB:       cfg.Cache.RedisDB,
				TLS:      cfg.Cache.RedisTLS,
			},
		})
		if cErr != nil {
			return fmt.Errorf("building cache: %w", cErr)
		}
		cacheCloser = closer
		storer = adapters.NewCache(storer, c, cfg.Cache.TTL, log)
	}
	publisher, _, busCloser, err := messaging.New(cfg.Messaging.Driver)
	if err != nil {
		return fmt.Errorf("building messaging: %w", err)
	}
	svc := item.NewService(log, storer, adapters.NewPublisher(publisher, eventsTopic))
	ready := web.NewReadiness()

	// The /v1 middleware chain, assembled here in order — the server applies it
	// verbatim and never hardcodes a middleware (see internal/middleware).
	// requestID first so the id reaches every layer; telemetry high so it
	// observes 401/503/recovered-500 below it; throttle and auth reject before
	// the handlers; bodyLimit last, just before the routes.
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
			return fmt.Errorf("building authenticator: %w", err)
		}
		chain = append(chain, middleware.Auth(authr))
	}
	// Timeout precedes bodyLimit so the deadline also covers request decoding,
	// and follows auth/throttle so rejected requests are never charged against it.
	chain = append(chain,
		middleware.Timeout(cfg.HandlerTimeout),
		middleware.BodyLimit(cfg.MaxBodyBytes),
	)

	// The kernel is domain-agnostic: each bounded context's HTTP adapter
	// registers its own routes onto the /v1 group. Add a context = construct its
	// service and pass its handler here — the server never learns item internals.
	opts := []web.Option{
		web.WithGroupMiddleware(chain...),
		web.WithRoutes(itemhttp.NewHandler(svc)),
	}

	server := web.NewServer(web.Config{
		Port:         cfg.HTTPPort,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		Env:          cfg.Env,
		Version:      version,
	}, log, ready, opts...)

	runner := lifecycle.NewRunner(log, cfg.ShutdownTimeout)
	// Registered first → stopped last: flush telemetry after everything drained.
	runner.Add(lifecycle.Component{
		Name:  "telemetry",
		Start: func(context.Context) error { return nil },
		Stop:  tel.Shutdown,
	})
	// Bus is a stop-only resource; Close unblocks any consumer and is done
	// before telemetry flush but after the HTTP server drains.
	runner.Add(lifecycle.Component{
		Name:  "bus",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { return busCloser.Close() },
	})
	// Cache connection (no-op unless a driver is configured); closed after the
	// HTTP server drains so in-flight requests keep their cache.
	runner.Add(lifecycle.Component{
		Name:  "cache",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { return cacheCloser.Close() },
	})
	runner.Add(lifecycle.Component{
		Name:  "http",
		Start: server.Start,
		Stop:  server.Stop,
	})

	if err := runner.Run(ctx); err != nil {
		return fmt.Errorf("running server lifecycle: %w", err)
	}
	return nil
}
