// bootstrap.go
package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/luisfelipecoelho/go-template/internal/config"
	"github.com/luisfelipecoelho/go-template/internal/logger"
	"github.com/luisfelipecoelho/go-template/internal/telemetry"
)

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
