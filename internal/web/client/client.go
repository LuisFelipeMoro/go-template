// Package httpclient is a resilient outbound HTTP client for downstream calls
// (BFF aggregation, ingestion fetches, webhooks). It layers three concerns onto
// the standard library: OpenTelemetry instrumentation (every request is a
// traced span propagating W3C context), bounded retry with backoff, and a
// circuit breaker — so a flaky dependency degrades gracefully instead of
// cascading. Detach it by deleting this package; nothing else depends on it.
package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/luisfelipecoelho/go-template/pkg/resilience"
)

// Config tunes the client. The zero value is usable via New's defaults.
type Config struct {
	Timeout time.Duration            // per-attempt timeout; defaults to 10s
	Retry   resilience.RetryConfig   // retry budget; MaxAttempts defaults to 3
	Breaker resilience.BreakerConfig // breaker thresholds; sensible defaults applied
}

// Client performs resilient, traced HTTP requests.
type Client struct {
	http    *http.Client
	retry   resilience.RetryConfig
	breaker *resilience.CircuitBreaker
}

// New builds a Client. The underlying transport is wrapped with otelhttp so
// every request is traced and propagates context to the callee.
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Retry.MaxAttempts < 1 {
		cfg.Retry.MaxAttempts = 3
	}
	return &Client{
		http: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		retry:   cfg.Retry,
		breaker: resilience.NewCircuitBreaker(cfg.Breaker),
	}
}

// Do sends req with retry + circuit-breaker protection. A response is retried
// only on a transport error or a retryable status (>=500 or 429); 4xx responses
// are returned to the caller unretried. Requests with a body are only retried
// when req.GetBody is set (the standard library sets it for in-memory bodies).
//
// The caller owns closing the returned response body.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := resilience.Retry(ctx, c.retry, func(ctx context.Context) (*http.Response, error) {
		attempt := req.Clone(ctx)
		if req.Body != nil {
			if req.GetBody == nil {
				// Cannot safely resend the body — do not retry.
				return c.once(ctx, req, false)
			}
			body, gerr := req.GetBody()
			if gerr != nil {
				return nil, resilience.Permanent(fmt.Errorf("rewinding request body: %w", gerr))
			}
			attempt.Body = body
		}
		return c.once(ctx, attempt, true)
	})
	if err != nil {
		return nil, fmt.Errorf("http request %s %s: %w", req.Method, req.URL.Redacted(), err)
	}
	return resp, nil
}

// once performs a single attempt through the breaker. When retryable is true a
// >=500/429 response is turned into an error so Retry re-issues it; otherwise
// the response is returned as-is.
func (c *Client) once(ctx context.Context, req *http.Request, retryable bool) (*http.Response, error) {
	var resp *http.Response
	err := c.breaker.Execute(ctx, func(ctx context.Context) error {
		r, e := c.http.Do(req)
		if e != nil {
			return e
		}
		resp = r
		if retryable && isRetryableStatus(r.StatusCode) {
			// Drain and close so the connection can be reused before retrying.
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
			return fmt.Errorf("retryable upstream status %d", r.StatusCode)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// isRetryableStatus reports whether a status code warrants a retry.
func isRetryableStatus(code int) bool {
	return code >= http.StatusInternalServerError || code == http.StatusTooManyRequests
}

// State exposes the circuit-breaker state for metrics or health reporting.
func (c *Client) State() resilience.State {
	return c.breaker.State()
}
