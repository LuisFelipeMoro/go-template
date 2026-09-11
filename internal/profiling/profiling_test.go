package profiling

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// start runs s on a kernel-chosen loopback port and returns its resolved
// address, tearing the server down through the lifecycle contract (cancel then
// Stop) so the test exercises the same shutdown path the runner uses.
func start(t *testing.T, s *Server) string {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	// Start blocks until ctx is done; the resolved address is published by the
	// time it has bound, so wait for the listener before reading Addr.
	ready := make(chan struct{})
	go func() {
		close(ready)
		done <- s.Start(ctx)
	}()
	<-ready

	addr := waitForListen(t, s)

	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(t, s.Stop(stopCtx))
		require.NoError(t, <-done, "Start must report a clean shutdown as nil")
	})
	return addr
}

// waitForListen polls until the server has bound and answers, returning the
// resolved address. Polling (rather than a fixed sleep) keeps the test fast and
// non-flaky under -race.
func waitForListen(t *testing.T, s *Server) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		addr := s.Addr()
		if addr != "127.0.0.1:0" {
			resp, err := http.Get("http://" + addr + "/debug/pprof/")
			if err == nil {
				require.NoError(t, resp.Body.Close())
				return addr
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("profiling server never became reachable")
	return ""
}

// The whole point of the package is that these handlers are reachable on a
// listener of their own. If a refactor drops a registration, profiling silently
// stops working exactly when it is needed.
func TestServer_ServesPprofRoutes(t *testing.T) {
	t.Parallel()

	addr := start(t, New("127.0.0.1:0"))

	tests := []struct {
		name string
		path string
		want int
	}{
		{"index", "/debug/pprof/", http.StatusOK},
		{"named profile", "/debug/pprof/heap?debug=1", http.StatusOK},
		{"goroutine dump", "/debug/pprof/goroutine?debug=1", http.StatusOK},
		{"cmdline", "/debug/pprof/cmdline", http.StatusOK},
		{"symbol", "/debug/pprof/symbol", http.StatusOK},
		// The admin listener carries profiling and nothing else: no health
		// probe, no API route, no accidental second surface to secure.
		{"health probe is not mounted here", "/healthz", http.StatusNotFound},
		{"api is not mounted here", "/v1/items", http.StatusNotFound},
		{"root is not mounted here", "/", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get("http://" + addr + tc.path)
			require.NoError(t, err)
			defer func() { require.NoError(t, resp.Body.Close()) }()
			_, err = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)

			assert.Equal(t, tc.want, resp.StatusCode)
		})
	}
}

// Importing net/http/pprof registers its handlers on http.DefaultServeMux from
// init, which no import style can prevent. What this package CAN guarantee is
// that its own listener serves a private mux, so the only way profiling becomes
// reachable is the address the operator configured — never a server elsewhere
// in the process that was handed a nil handler.
func TestServer_ServesAPrivateMuxNotTheDefaultOne(t *testing.T) {
	t.Parallel()

	s := New("127.0.0.1:0")

	require.NotNil(t, s.http.Handler)
	assert.NotSame(t, http.DefaultServeMux, s.http.Handler,
		"serving the default mux would publish pprof wherever that mux is mounted")

	// Guard the premise of the comment above: if a future Go release stops
	// registering pprof on the default mux, this assertion fails and the
	// constraint documented in the package comment can be relaxed.
	_, pattern := http.DefaultServeMux.Handler(
		&http.Request{Method: http.MethodGet, URL: mustURL(t, "http://example.com/debug/pprof/")},
	)
	assert.NotEmpty(t, pattern,
		"net/http/pprof init still registers on the default mux; nothing here may serve it")
}

// Per the lifecycle.Component contract Start returns nil when its context is
// cancelled — the runner treats a non-nil return as fatal and fails the process.
func TestServer_StartReturnsNilOnContextCancel(t *testing.T) {
	t.Parallel()

	s := New("127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()
	waitForListen(t, s)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	require.NoError(t, s.Stop(stopCtx))
}

// A port the process cannot bind is a startup failure the runner must see —
// silently continuing would leave an operator port-forwarding to nothing.
func TestServer_StartReportsBindFailure(t *testing.T) {
	t.Parallel()

	err := New("127.0.0.1:not-a-port").Start(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listening on")
}

// Stop is called by the runner on every shutdown path, including ones where
// Start never bound. It must not panic or report a spurious failure.
func TestServer_StopIsSafeBeforeStartAndRepeatable(t *testing.T) {
	t.Parallel()

	s := New("127.0.0.1:0")
	ctx := context.Background()

	require.NoError(t, s.Stop(ctx))
	require.NoError(t, s.Stop(ctx), "Stop must be idempotent")
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}
