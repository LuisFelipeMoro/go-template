// logger.go
// Package logger builds the application's structured logger: single-line JSON
// via stdlib slog, wrapped so every record carries trace_id/span_id when the
// caller's context holds a valid OpenTelemetry span.
//
// Security (SEC-2): this package never logs values on its own. Callers MUST NOT
// pass PII, secrets, tokens, or card data as log attributes.
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config configures the logger. Writer defaults to os.Stdout when nil.
type Config struct {
	Level   string // debug|info|warn|error; unknown falls back to info
	Service string
	Version string
	Env     string
	Writer  io.Writer
}

// New returns a *slog.Logger writing single-line JSON with service/version/env
// base attributes and automatic trace-id injection from the context span.
func New(cfg Config) *slog.Logger {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}

	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(cfg.Level)})
	handler := NewTraceHandler(base)

	attrs := []slog.Attr{}
	if cfg.Service != "" {
		attrs = append(attrs, slog.String("service", cfg.Service))
	}
	if cfg.Version != "" {
		attrs = append(attrs, slog.String("version", cfg.Version))
	}
	if cfg.Env != "" {
		attrs = append(attrs, slog.String("env", cfg.Env))
	}

	return slog.New(handler.WithAttrs(attrs))
}

// parseLevel maps a level string to slog.Level; unknown values map to info.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
