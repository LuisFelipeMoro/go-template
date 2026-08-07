package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoop_AlwaysMisses(t *testing.T) {
	t.Parallel()
	c := NewNoop()
	require.NoError(t, c.Set(context.Background(), "k", []byte("v"), time.Minute))
	v, ok, err := c.Get(context.Background(), "k")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, v)
	require.NoError(t, c.Delete(context.Background(), "k"))
}

func TestMemory_SetGetDelete(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	ctx := context.Background()

	_, ok, err := c.Get(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, ok, "unset key misses")

	require.NoError(t, c.Set(ctx, "k", []byte("hello"), time.Minute))
	v, ok, err := c.Get(ctx, "k")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []byte("hello"), v)

	require.NoError(t, c.Delete(ctx, "k"))
	_, ok, err = c.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, ok, "deleted key misses")
}

func TestMemory_Expiry(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "k", []byte("v"), time.Minute))
	now = now.Add(30 * time.Second)
	_, ok, err := c.Get(ctx, "k")
	require.NoError(t, err)
	assert.True(t, ok, "unexpired entry is a hit")

	now = now.Add(31 * time.Second) // past the 60s TTL
	_, ok, err = c.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, ok, "expired entry is a miss")
}

func TestMemory_ReturnsCopy(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	ctx := context.Background()
	src := []byte("data")
	require.NoError(t, c.Set(ctx, "k", src, 0))
	src[0] = 'X' // mutate the caller's slice after Set

	v, _, _ := c.Get(ctx, "k")
	assert.Equal(t, []byte("data"), v, "cache stores a copy, not the caller's slice")
	v[0] = 'Y' // mutate the returned slice
	again, _, _ := c.Get(ctx, "k")
	assert.Equal(t, []byte("data"), again, "cache returns a copy, not its own slice")
}

// fakeDoer is an in-memory redisDoer for exercising RedisCache without a server.
type fakeDoer struct {
	store  map[string]string
	getErr error
}

func newFakeDoer() *fakeDoer { return &fakeDoer{store: map[string]string{}} }

func (f *fakeDoer) Get(_ context.Context, key string) (string, bool, error) {
	if f.getErr != nil {
		return "", false, f.getErr
	}
	v, ok := f.store[key]
	return v, ok, nil
}
func (f *fakeDoer) Set(_ context.Context, key, value string, _ time.Duration) error {
	f.store[key] = value
	return nil
}
func (f *fakeDoer) Del(_ context.Context, key string) error {
	delete(f.store, key)
	return nil
}
func (f *fakeDoer) Close() error { return nil }

func TestRedisCache_RoundTrip(t *testing.T) {
	t.Parallel()
	r := &RedisCache{doer: newFakeDoer()}
	ctx := context.Background()

	_, ok, err := r.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, r.Set(ctx, "k", []byte("v"), time.Minute))
	v, ok, err := r.Get(ctx, "k")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []byte("v"), v)

	require.NoError(t, r.Delete(ctx, "k"))
	_, ok, err = r.Get(ctx, "k")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRedisCache_GetSurfacesBackendError(t *testing.T) {
	t.Parallel()
	fake := newFakeDoer()
	fake.getErr = errors.New("connection refused")
	r := &RedisCache{doer: fake}

	_, ok, err := r.Get(context.Background(), "k")
	require.Error(t, err)
	assert.False(t, ok)
}

func TestFactory_New(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name    string
		driver  string
		wantErr bool
	}{
		{"empty defaults to noop", "", false},
		{"none", "none", false},
		{"memory", "memory", false},
		{"unknown", "bogus", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, closer, err := New(ctx, Config{Driver: tt.driver})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, c)
			require.NoError(t, closer.Close())
		})
	}
}
