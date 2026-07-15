// memory.go
package cache

import (
	"context"
	"sync"
	"time"
)

// Memory is an in-process TTL cache: a safe default for single-instance or
// test use, and the reference implementation of Cache. Expiry is lazy (checked
// on read); construct via NewMemory. It is not a substitute for Redis in a
// multi-replica deployment, where each instance would hold its own copy.
type Memory struct {
	mu      sync.RWMutex
	entries map[string]entry
	now     func() time.Time
}

type entry struct {
	value   []byte
	expires time.Time // zero means no expiry
}

// NewMemory returns an empty in-memory cache.
func NewMemory() *Memory {
	return &Memory{entries: make(map[string]entry), now: time.Now}
}

// Get returns the value for key when present and unexpired.
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	e, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if !e.expires.IsZero() && m.now().After(e.expires) {
		m.mu.Lock()
		// Re-check under the write lock: a concurrent Set may have refreshed it.
		if cur, still := m.entries[key]; still && cur.expires.Equal(e.expires) {
			delete(m.entries, key)
		}
		m.mu.Unlock()
		return nil, false, nil
	}
	// Copy so callers cannot mutate cached bytes.
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, true, nil
}

// Set stores value under key. A non-positive ttl means no expiry.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	stored := make([]byte, len(value))
	copy(stored, value)
	var expires time.Time
	if ttl > 0 {
		expires = m.now().Add(ttl)
	}
	m.mu.Lock()
	m.entries[key] = entry{value: stored, expires: expires}
	m.mu.Unlock()
	return nil
}

// Delete removes key if present.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
	return nil
}
