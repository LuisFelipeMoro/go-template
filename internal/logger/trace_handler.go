// trace_handler.go
package logger

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// op records a deferred WithAttrs or WithGroup applied by a caller after the
// handler was constructed. group != "" means WithGroup; otherwise WithAttrs.
type op struct {
	group string
	attrs []slog.Attr
}

// TraceHandler wraps a slog.Handler and enriches every record with trace_id and
// span_id whenever the handling context carries a valid span context. The trace
// ids are always emitted at the JSON root — even when callers have opened groups
// via WithGroup — so downstream collectors can correlate logs with traces.
//
// It achieves root-level injection by keeping an ungrouped root handler and
// replaying the caller's WithAttrs/WithGroup operations after injecting the
// trace attributes at the root, only on records that carry a valid span. Records
// without a span take the pre-built grouped handler directly (zero extra work).
type TraceHandler struct {
	root    slog.Handler // base attrs only, never carries caller groups
	grouped slog.Handler // root + all caller WithAttrs/WithGroup applied
	ops     []op         // caller operations, in application order
}

// Compile-time assertion that TraceHandler satisfies slog.Handler.
var _ slog.Handler = (*TraceHandler)(nil)

// NewTraceHandler wraps inner so trace context is injected into each record.
func NewTraceHandler(inner slog.Handler) *TraceHandler {
	return &TraceHandler{root: inner, grouped: inner}
}

// Enabled delegates the level check to the wrapped handler.
func (h *TraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.grouped.Enabled(ctx, level)
}

// Handle injects trace_id/span_id at the root when the context holds a valid
// span, then emits the record through the caller's grouped view.
func (h *TraceHandler) Handle(ctx context.Context, rec slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return h.grouped.Handle(ctx, rec) //nolint:wrapcheck // pass-through handler
	}

	next := h.root.WithAttrs([]slog.Attr{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	})
	for _, o := range h.ops {
		if o.group != "" {
			next = next.WithGroup(o.group)
			continue
		}
		next = next.WithAttrs(o.attrs)
	}
	return next.Handle(ctx, rec) //nolint:wrapcheck // pass-through handler
}

// WithAttrs records the operation for root-level trace injection and applies it
// to the grouped view, preserving the wrap.
func (h *TraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return &TraceHandler{
		root:    h.root,
		grouped: h.grouped.WithAttrs(attrs),
		ops:     appendOp(h.ops, op{attrs: attrs}),
	}
}

// WithGroup records the operation for root-level trace injection and applies it
// to the grouped view, preserving the wrap.
func (h *TraceHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &TraceHandler{
		root:    h.root,
		grouped: h.grouped.WithGroup(name),
		ops:     appendOp(h.ops, op{group: name}),
	}
}

// appendOp returns a new slice with o appended, never aliasing the input so
// sibling handlers derived from the same parent stay independent.
func appendOp(ops []op, o op) []op {
	out := make([]op, len(ops), len(ops)+1)
	copy(out, ops)
	return append(out, o)
}
