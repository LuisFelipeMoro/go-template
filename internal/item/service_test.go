// service_test.go
package item

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"log/slog"

	"github.com/luisfelipecoelho/go-template/pkg/uid"
)

// fakeStore is a hand-written scriptable Storer.
type fakeStore struct {
	items     map[string]Item
	createErr error
	queryErr  error
	updateErr error
	deleteErr error
}

func newFakeStore() *fakeStore { return &fakeStore{items: map[string]Item{}} }

func (f *fakeStore) Create(ctx context.Context, itm Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.createErr != nil {
		return f.createErr
	}
	f.items[itm.ID] = itm
	return nil
}

func (f *fakeStore) QueryByID(ctx context.Context, id string) (Item, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	if f.queryErr != nil {
		return Item{}, f.queryErr
	}
	itm, ok := f.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return itm, nil
}

func (f *fakeStore) Query(ctx context.Context, page Page) ([]Item, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if f.queryErr != nil {
		return nil, 0, f.queryErr
	}
	out := make([]Item, 0, len(f.items))
	for _, itm := range f.items {
		out = append(out, itm)
	}
	return out, len(out), nil
}

func (f *fakeStore) Update(ctx context.Context, itm Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.updateErr != nil {
		return f.updateErr
	}
	if _, ok := f.items[itm.ID]; !ok {
		return ErrNotFound
	}
	f.items[itm.ID] = itm
	return nil
}

func (f *fakeStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.items[id]; !ok {
		return ErrNotFound
	}
	delete(f.items, id)
	return nil
}

// fakePub records published events and can be scripted to fail.
type fakePub struct {
	events []Event
	err    error
}

func (f *fakePub) Publish(ctx context.Context, evt Event) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, evt)
	return nil
}

func newService(store Storer, pub EventPublisher, w *bytes.Buffer) *Service {
	if w == nil {
		w = &bytes.Buffer{}
	}
	log := slog.New(slog.NewJSONHandler(w, nil))
	return NewService(log, store, pub)
}

func validNewItem() NewItem {
	return NewItem{Name: "widget", Quantity: 3, PriceCents: 1990}
}

func TestService_Create_Valid(t *testing.T) {
	t.Parallel()
	store, pub := newFakeStore(), &fakePub{}
	svc := newService(store, pub, nil)

	itm, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)

	assert.NoError(t, uid.Validate(itm.ID), "generated ID must be a UUID")
	assert.Equal(t, "widget", itm.Name)
	assert.Equal(t, 3, itm.Quantity)
	assert.Equal(t, int64(1990), itm.PriceCents)
	assert.False(t, itm.CreatedAt.IsZero())
	assert.Equal(t, itm.CreatedAt, itm.UpdatedAt)
	assert.Contains(t, store.items, itm.ID, "item must be persisted")
}

func TestService_Create_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ni   NewItem
	}{
		{"empty name", NewItem{Name: "", Quantity: 1, PriceCents: 1}},
		{"whitespace name", NewItem{Name: "   ", Quantity: 1, PriceCents: 1}},
		{"name 121 chars", NewItem{Name: strings.Repeat("x", 121), Quantity: 1, PriceCents: 1}},
		{"negative quantity", NewItem{Name: "ok", Quantity: -1, PriceCents: 1}},
		{"negative price", NewItem{Name: "ok", Quantity: 1, PriceCents: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newService(newFakeStore(), &fakePub{}, nil)
			_, err := svc.Create(context.Background(), tt.ni)
			assert.ErrorIs(t, err, ErrInvalidArgument)
		})
	}
}

func TestService_Create_BoundaryValid(t *testing.T) {
	t.Parallel()
	svc := newService(newFakeStore(), &fakePub{}, nil)

	// 120-char name, zero quantity and zero price are all valid boundaries.
	itm, err := svc.Create(context.Background(), NewItem{
		Name: strings.Repeat("x", 120), Quantity: 0, PriceCents: 0,
	})
	require.NoError(t, err)
	assert.Len(t, itm.Name, 120)
}

func TestService_Create_PublishesCreatedEvent(t *testing.T) {
	t.Parallel()
	pub := &fakePub{}
	svc := newService(newFakeStore(), pub, nil)

	itm, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)
	require.Len(t, pub.events, 1)
	assert.Equal(t, EventCreated, pub.events[0].Type)
	assert.Equal(t, itm.ID, pub.events[0].ItemID)
	assert.False(t, pub.events[0].OccurredAt.IsZero())
}

func TestService_Create_PublishFailureStillSucceedsAndLogsError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pub := &fakePub{err: errors.New("broker down")}
	store := newFakeStore()
	svc := newService(store, pub, &buf)

	itm, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err, "best-effort publish: operation must succeed")
	assert.Contains(t, store.items, itm.ID)
	assert.Contains(t, buf.String(), `"level":"ERROR"`, "publish failure must be logged at error")
}

func TestService_QueryByID(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	svc := newService(store, &fakePub{}, nil)
	created, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)

	got, err := svc.QueryByID(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, got)

	_, err = svc.QueryByID(context.Background(), mustID(t))
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = svc.QueryByID(context.Background(), "not-a-uuid")
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func TestService_Query_PageBounds(t *testing.T) {
	t.Parallel()
	svc := newService(newFakeStore(), &fakePub{}, nil)

	tests := []struct {
		name string
		page Page
	}{
		{"page zero", Page{Number: 0, Rows: 20}},
		{"rows zero", Page{Number: 1, Rows: 0}},
		{"rows over max", Page{Number: 1, Rows: 101}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := svc.Query(context.Background(), tt.page)
			assert.ErrorIs(t, err, ErrInvalidArgument)
		})
	}

	_, total, err := svc.Query(context.Background(), Page{Number: 1, Rows: 100})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
}

func TestService_Update(t *testing.T) {
	t.Parallel()
	pub := &fakePub{}
	store := newFakeStore()
	svc := newService(store, pub, nil)
	created, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)

	newName := "gadget"
	newQty := 7
	updated, err := svc.Update(context.Background(), created.ID, UpdateItem{Name: &newName, Quantity: &newQty})
	require.NoError(t, err)
	assert.Equal(t, "gadget", updated.Name)
	assert.Equal(t, 7, updated.Quantity)
	assert.Equal(t, created.PriceCents, updated.PriceCents, "unset field must be untouched")
	assert.True(t, updated.UpdatedAt.After(created.UpdatedAt) || updated.UpdatedAt.Equal(created.UpdatedAt))
	require.Len(t, pub.events, 2)
	assert.Equal(t, EventUpdated, pub.events[1].Type)
}

func TestService_Update_Validation(t *testing.T) {
	t.Parallel()
	svc := newService(newFakeStore(), &fakePub{}, nil)
	id := mustID(t)

	longName := strings.Repeat("x", 121)
	negQty := -1
	negPrice := int64(-1)
	tests := []struct {
		name    string
		id      string
		ui      UpdateItem
		wantErr error
	}{
		{"all nil fields", id, UpdateItem{}, ErrInvalidArgument},
		{"bad uuid", "nope", UpdateItem{Name: ptr("x")}, ErrInvalidArgument},
		{"long name", id, UpdateItem{Name: &longName}, ErrInvalidArgument},
		{"negative quantity", id, UpdateItem{Quantity: &negQty}, ErrInvalidArgument},
		{"negative price", id, UpdateItem{PriceCents: &negPrice}, ErrInvalidArgument},
		{"missing item", id, UpdateItem{Name: ptr("x")}, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.Update(context.Background(), tt.id, tt.ui)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestService_Delete(t *testing.T) {
	t.Parallel()
	pub := &fakePub{}
	store := newFakeStore()
	svc := newService(store, pub, nil)
	created, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), created.ID))
	assert.NotContains(t, store.items, created.ID)
	require.Len(t, pub.events, 2)
	assert.Equal(t, EventDeleted, pub.events[1].Type)

	assert.ErrorIs(t, svc.Delete(context.Background(), created.ID), ErrNotFound)
	assert.ErrorIs(t, svc.Delete(context.Background(), "bad"), ErrInvalidArgument)
}

func TestService_Delete_PublishFailureStillDeletes(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	store := newFakeStore()
	pub := &fakePub{}
	svc := newService(store, pub, &buf)
	created, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)

	pub.err = errors.New("broker down")
	require.NoError(t, svc.Delete(context.Background(), created.ID))
	assert.NotContains(t, store.items, created.ID)
	assert.Contains(t, buf.String(), `"level":"ERROR"`)
}

func TestService_CtxCancellationPropagates(t *testing.T) {
	t.Parallel()
	svc := newService(newFakeStore(), &fakePub{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.Create(ctx, validNewItem())
	assert.ErrorIs(t, err, context.Canceled)
}

func TestService_StoreErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("disk full")
	store := newFakeStore()
	store.createErr = sentinel
	svc := newService(store, &fakePub{}, nil)

	_, err := svc.Create(context.Background(), validNewItem())
	assert.ErrorIs(t, err, sentinel, "store errors must be wrapped, not swallowed")
}

func TestService_NoLogsOnSuccess(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	svc := newService(newFakeStore(), &fakePub{}, &buf)

	_, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)
	assert.Empty(t, buf.String(), "ADR-6: success paths emit no log records")
}

func ptr[T any](v T) *T { return &v }

// mustID returns a fresh valid UUID for tests, failing on the impossible
// CSPRNG error.
func mustID(t *testing.T) string {
	t.Helper()
	id, err := uid.New()
	require.NoError(t, err)
	return id
}

// eventTime sanity: OccurredAt must be recent UTC.
func TestEvent_OccurredAtRecent(t *testing.T) {
	t.Parallel()
	pub := &fakePub{}
	svc := newService(newFakeStore(), pub, nil)
	_, err := svc.Create(context.Background(), validNewItem())
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), pub.events[0].OccurredAt, 5*time.Second)
}
