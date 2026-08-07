package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/item/adapters"
	itemhttp "github.com/luisfelipecoelho/go-template/internal/item/http"
	"github.com/luisfelipecoelho/go-template/internal/messaging"
	"github.com/luisfelipecoelho/go-template/internal/middleware"
	"github.com/luisfelipecoelho/go-template/internal/web"
)

// testServer wires the real domain + in-memory infra behind the kernel, with the
// item HTTP handler registered — exercising the full request path.
func testServer(t *testing.T) *web.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	pub, _, closer, err := messaging.New("memory")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })
	svc := item.NewService(log, adapters.NewDatabase(), adapters.NewPublisher(pub, "items"))
	ready := web.NewReadiness()
	ready.SetReady(true)
	return web.NewServer(web.Config{Env: "prod", Version: "test"}, log, ready, web.WithRoutes(itemhttp.NewHandler(svc)))
}

func do(t *testing.T, s *web.Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

type itemResp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type errResp struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func decodeItem(t *testing.T, rec *httptest.ResponseRecorder) itemResp {
	t.Helper()
	var out itemResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errResp {
	t.Helper()
	var out errResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestCreate_Success(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	rec := do(t, s, http.MethodPost, "/v1/items", `{"name":"widget","quantity":3,"price_cents":1990}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	got := decodeItem(t, rec)
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, "widget", got.Name)
}

func TestCreate_ValidationErrors(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	for _, tt := range []struct{ name, body string }{
		{"empty name", `{"name":"","quantity":1,"price_cents":1}`},
		{"negative quantity", `{"name":"ok","quantity":-1,"price_cents":1}`},
		{"malformed json", `{"name":`},
		{"unknown field", `{"name":"ok","quantity":1,"price_cents":1,"bogus":true}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, s, http.MethodPost, "/v1/items", tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, web.CodeValidation, decodeErr(t, rec).Error)
		})
	}
}

func TestValidationMessage_NoSentinelLeak(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	rec := do(t, s, http.MethodPost, "/v1/items", `{"name":"","quantity":1,"price_cents":1}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	msg := decodeErr(t, rec).Message
	assert.Contains(t, msg, "name must be")
	assert.NotContains(t, msg, "invalid argument")
}

func TestGet_FoundNotFoundBadID(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	created := decodeItem(t, do(t, s, http.MethodPost, "/v1/items", `{"name":"a","quantity":1,"price_cents":1}`))

	rec := do(t, s, http.MethodGet, "/v1/items/"+created.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, s, http.MethodGet, "/v1/items/11111111-1111-1111-1111-111111111111", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, web.CodeNotFound, decodeErr(t, rec).Error)

	rec = do(t, s, http.MethodGet, "/v1/items/not-a-uuid", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestList_PaginationAndBounds(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	for i := 0; i < 3; i++ {
		do(t, s, http.MethodPost, "/v1/items", `{"name":"a","quantity":1,"price_cents":1}`)
	}
	rec := do(t, s, http.MethodGet, "/v1/items?page=1&rows=2", "")
	require.Equal(t, http.StatusOK, rec.Code)

	for _, bad := range []string{"?page=0", "?rows=0", "?rows=101", "?page=abc"} {
		rec := do(t, s, http.MethodGet, "/v1/items"+bad, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, "query %s must be rejected", bad)
	}
}

func TestUpdate_PatchEmptyAndNotFound(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	created := decodeItem(t, do(t, s, http.MethodPost, "/v1/items", `{"name":"a","quantity":1,"price_cents":1}`))

	rec := do(t, s, http.MethodPatch, "/v1/items/"+created.ID, `{"name":"b"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "b", decodeItem(t, rec).Name)

	rec = do(t, s, http.MethodPatch, "/v1/items/"+created.ID, `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = do(t, s, http.MethodPatch, "/v1/items/11111111-1111-1111-1111-111111111111", `{"name":"x"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDelete(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	created := decodeItem(t, do(t, s, http.MethodPost, "/v1/items", `{"name":"a","quantity":1,"price_cents":1}`))

	rec := do(t, s, http.MethodDelete, "/v1/items/"+created.ID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, s, http.MethodDelete, "/v1/items/"+created.ID, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// blockingStore stalls until its context is cancelled, standing in for a
// dependency that has stopped responding.
type blockingStore struct{}

func (blockingStore) Create(ctx context.Context, _ item.Item) error { <-ctx.Done(); return ctx.Err() }
func (blockingStore) Update(ctx context.Context, _ item.Item) error { <-ctx.Done(); return ctx.Err() }
func (blockingStore) Delete(ctx context.Context, _ string) error    { <-ctx.Done(); return ctx.Err() }

func (blockingStore) QueryByID(ctx context.Context, _ string) (item.Item, error) {
	<-ctx.Done()
	return item.Item{}, ctx.Err()
}

func (blockingStore) Query(ctx context.Context, _ item.Page) ([]item.Item, int, error) {
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

// A blown handler deadline must surface as 504 with the standard envelope, not
// collapse into the generic 500 that every other internal failure maps to.
func TestHandlerTimeout_MapsToGatewayTimeout(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	pub, _, closer, err := messaging.New("memory")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	svc := item.NewService(log, blockingStore{}, adapters.NewPublisher(pub, "items"))
	ready := web.NewReadiness()
	ready.SetReady(true)
	s := web.NewServer(web.Config{Env: "prod", Version: "test"}, log, ready,
		web.WithGroupMiddleware(middleware.Timeout(20*time.Millisecond)),
		web.WithRoutes(itemhttp.NewHandler(svc)))

	rec := do(t, s, http.MethodGet, "/v1/items/a2b7172e-cca4-4fa6-89e2-5f65d45e850f", "")

	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	var env web.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, web.CodeTimeout, env.Error)
	assert.NotContains(t, rec.Body.String(), "context deadline", "internal cause must not leak")
}
