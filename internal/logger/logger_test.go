// logger_test.go
package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// decodeLine parses the single JSON log line written to buf.
func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := bytes.TrimSpace(buf.Bytes())
	require.NotEmpty(t, line, "expected a log line")
	require.Equal(t, 1, bytes.Count(line, []byte{'\n'})+1, "expected single-line JSON")
	var m map[string]any
	require.NoError(t, json.Unmarshal(line, &m))
	return m
}

// validSpanCtx fabricates a valid, sampled span context for trace correlation.
func validSpanCtx(t *testing.T) context.Context {
	t.Helper()
	tid, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	sid, err := trace.SpanIDFromHex("0123456789abcdef")
	require.NoError(t, err)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	require.True(t, sc.IsValid())
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestNew_EmitsValidJSONWithBaseAttrs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := New(Config{Level: "info", Service: "svc", Version: "1.2.3", Env: "prod", Writer: &buf})

	log.Info("hello")

	m := decodeLine(t, &buf)
	assert.Contains(t, m, "time")
	assert.Equal(t, "INFO", m["level"])
	assert.Equal(t, "hello", m["msg"])
	assert.Equal(t, "svc", m["service"])
	assert.Equal(t, "1.2.3", m["version"])
	assert.Equal(t, "prod", m["env"])
}

func TestNew_InjectsTraceIDsWhenSpanValid(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := New(Config{Level: "debug", Service: "svc", Writer: &buf})

	log.InfoContext(validSpanCtx(t), "with-span")

	m := decodeLine(t, &buf)
	assert.Equal(t, "0123456789abcdef0123456789abcdef", m["trace_id"])
	assert.Equal(t, "0123456789abcdef", m["span_id"])
}

func TestNew_NoTraceIDsWithoutSpan(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := New(Config{Level: "debug", Service: "svc", Writer: &buf})

	log.InfoContext(context.Background(), "no-span")

	m := decodeLine(t, &buf)
	assert.NotContains(t, m, "trace_id")
	assert.NotContains(t, m, "span_id")
}

func TestNew_WithAttrsAndGroupStillInjectTraceIDs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	base := New(Config{Level: "debug", Service: "svc", Writer: &buf})

	log := base.With(slog.String("component", "http")).WithGroup("req")
	log.InfoContext(validSpanCtx(t), "grouped", slog.String("route", "/x"))

	m := decodeLine(t, &buf)
	// Trace ids stay at the JSON root even under an open group, so collectors
	// can correlate; the record's own attrs nest under the group as usual.
	assert.Equal(t, "0123456789abcdef0123456789abcdef", m["trace_id"])
	assert.Equal(t, "0123456789abcdef", m["span_id"])
	assert.Equal(t, "svc", m["service"])    // base attr at root
	assert.Equal(t, "http", m["component"]) // added before the group → root
	assert.Equal(t, "grouped", m["msg"])    // built-in field always at root
	group, ok := m["req"].(map[string]any)
	require.True(t, ok, "expected grouped attrs under req")
	assert.Equal(t, "/x", group["route"])
}

func TestNew_LevelFiltering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		level     string
		wantEmpty bool
	}{
		{"info drops debug", "info", true},
		{"debug keeps debug", "debug", false},
		{"unknown defaults to info drops debug", "bogus", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			log := New(Config{Level: tt.level, Service: "svc", Writer: &buf})
			log.Debug("dbg")
			if tt.wantEmpty {
				assert.Empty(t, bytes.TrimSpace(buf.Bytes()))
			} else {
				assert.NotEmpty(t, bytes.TrimSpace(buf.Bytes()))
			}
		})
	}
}

func TestNew_NilWriterDefaultsToStdout(t *testing.T) {
	t.Parallel()
	// Must not panic and must return a usable logger when Writer is nil.
	log := New(Config{Level: "info", Service: "svc"})
	require.NotNil(t, log)
	log.Info("to-stdout")
}

func TestNew_ConcurrentLoggingRaceSafe(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := New(Config{Level: "debug", Service: "svc", Writer: &syncWriter{w: &buf}})
	ctx := validSpanCtx(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.InfoContext(ctx, "concurrent")
		}()
	}
	wg.Wait()
	assert.Equal(t, 50, bytes.Count(buf.Bytes(), []byte("concurrent")))
}

// syncWriter serializes concurrent writes so the buffer itself is not the race.
type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
