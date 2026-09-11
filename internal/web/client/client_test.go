// client_test.go
package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/pkg/resilience"
)

func fastClient(maxAttempts int) *Client {
	return New(Config{
		Timeout: 2 * time.Second,
		Retry:   resilience.RetryConfig{MaxAttempts: maxAttempts, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
		Breaker: resilience.BreakerConfig{FailureThreshold: 100}, // effectively off for these tests
	})
}

func getReq(t *testing.T, ctx context.Context, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	return req
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	require.NoError(t, resp.Body.Close())
}

func TestDo_SuccessNoRetry(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, werr := io.WriteString(w, "ok")
		assert.NoError(t, werr)
	}))
	t.Cleanup(srv.Close)

	resp, err := fastClient(3).Do(context.Background(), getReq(t, context.Background(), srv.URL))
	require.NoError(t, err)
	t.Cleanup(func() { closeBody(t, resp) })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
}

func TestDo_RetriesOn5xxThenSucceeds(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	resp, err := fastClient(5).Do(context.Background(), getReq(t, context.Background(), srv.URL))
	require.NoError(t, err)
	t.Cleanup(func() { closeBody(t, resp) })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.EqualValues(t, 3, atomic.LoadInt32(&calls), "must retry until success")
}

func TestDo_DoesNotRetry4xx(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	resp, err := fastClient(5).Do(context.Background(), getReq(t, context.Background(), srv.URL))
	require.NoError(t, err, "4xx is a delivered response, not a transport error")
	t.Cleanup(func() { closeBody(t, resp) })
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "4xx must not be retried")
}

func TestDo_ExhaustsRetriesOnPersistent5xx(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	_, err := fastClient(3).Do(context.Background(), getReq(t, context.Background(), srv.URL))
	require.Error(t, err)
	assert.EqualValues(t, 3, atomic.LoadInt32(&calls))
}

func TestDo_RetriesPostWithGetBody(t *testing.T) {
	t.Parallel()
	var bodies []string
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if atomic.AddInt32(&calls, 1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// http.NewRequestWithContext sets GetBody for a strings.Reader body.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(`{"k":"v"}`))
	require.NoError(t, err)
	resp, err := fastClient(3).Do(context.Background(), req)
	require.NoError(t, err)
	t.Cleanup(func() { closeBody(t, resp) })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, bodies, 2)
	assert.Equal(t, `{"k":"v"}`, bodies[1], "body must be resent intact on retry")
}

func TestDo_BreakerOpensAfterFailures(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := New(Config{
		Timeout: time.Second,
		Retry:   resilience.RetryConfig{MaxAttempts: 10, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond},
		Breaker: resilience.BreakerConfig{FailureThreshold: 2, Cooldown: time.Minute, HalfOpenMax: 1},
	})
	_, err := c.Do(context.Background(), getReq(t, context.Background(), srv.URL))
	require.Error(t, err)
	assert.Equal(t, resilience.StateOpen, c.State(), "breaker must open after repeated 5xx")
}

func TestDo_ContextCancelStops(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fastClient(5).Do(ctx, getReq(t, ctx, srv.URL))
	require.Error(t, err)
}
