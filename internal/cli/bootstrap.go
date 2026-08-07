// bootstrap.go
package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/luisfelipecoelho/go-template/internal/config"
	"github.com/luisfelipecoelho/go-template/internal/logger"
	"github.com/luisfelipecoelho/go-template/internal/middleware"
	"github.com/luisfelipecoelho/go-template/internal/telemetry"
	"github.com/luisfelipecoelho/go-template/internal/worker"
)

// Compile-time proof that the telemetry recorders satisfy the consumer-owned
// Metrics interfaces they are injected into. The assertions live here, not in
// internal/telemetry, because telemetry sits below middleware and worker in the
// dependency graph — importing them there to assert would invert the layering.
// The composition root is the one place that legitimately sees both sides.
var (
	_ middleware.Metrics = (*telemetry.HTTPMetrics)(nil)
	_ worker.Metrics     = (*telemetry.WorkerMetrics)(nil)
)

// noopCloser stands in for an optional resource that was never constructed, so
// the lifecycle Runner can hold one uniform Stop func whether or not the
// resource is enabled.
type noopCloser struct{}

// Close does nothing and never fails.
func (noopCloser) Close() error { return nil }

// observability is the shared logger + telemetry bootstrap for every command
// that runs a lifecycle.Runner (server, worker). It exists so both compose
// these singletons from one place instead of each hand-rolling identical
// setup. server and worker are always separate OS processes — never both
// alive in the same process — so there is no shared instance to race over;
// this only dedups construction, not lifecycle ownership.
type observability struct {
	log *slog.Logger
	tel *telemetry.Providers
}

// newObservability builds the logger and initializes telemetry for cfg.
func newObservability(ctx context.Context, cfg config.Config) (observability, error) {
	log := logger.New(logger.Config{
		Level:   cfg.LogLevel,
		Service: cfg.Otel.ServiceName,
		Version: version,
		Env:     cfg.Env,
	})

	tel, err := telemetry.Init(ctx, telemetry.Config{
		Enabled:     cfg.Otel.Enabled,
		Endpoint:    cfg.Otel.Endpoint,
		ServiceName: cfg.Otel.ServiceName,
		Version:     version,
		Env:         cfg.Env,
		SampleRatio: cfg.Otel.SampleRatio,
	})
	if err != nil {
		return observability{}, fmt.Errorf("initializing telemetry: %w", err)
	}

	return observability{log: log, tel: tel}, nil
}
