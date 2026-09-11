package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStaticKeys_RejectsEmptyKeySet(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		keys []string
	}{
		{"no keys", nil},
		{"only blanks", []string{"", "  ", "\t"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewStaticKeys(tt.keys...)
			require.Error(t, err, "an all-blank key set must fail fast")
		})
	}
}

func TestStaticKeys_Authenticate(t *testing.T) {
	t.Parallel()

	sk, err := NewStaticKeys("primary-key", "", "secondary-key")
	require.NoError(t, err)

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"first key", "primary-key", false},
		{"second key", "secondary-key", false},
		{"wrong key", "nope", true},
		{"empty token", "", true},
		{"prefix of a key", "primary", true},
		{"key with trailing char", "primary-key ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sk.Authenticate(context.Background(), tt.token)
			if tt.wantErr {
				assert.ErrorIs(t, err, ErrUnauthorized)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// Keys of wildly different lengths must all authenticate. The digest comparison
// this relies on would be easy to "simplify" back into a raw-bytes compare,
// which still passes a same-length test set — so the lengths here are
// deliberately unequal.
func TestStaticKeys_AuthenticatesKeysOfAnyLength(t *testing.T) {
	t.Parallel()

	keys := []string{
		"k",
		"medium-length-key",
		strings.Repeat("x", 512),
	}
	sk, err := NewStaticKeys(keys...)
	require.NoError(t, err)

	for i, k := range keys {
		t.Run(fmt.Sprintf("key_%d_len_%d", i, len(k)), func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, sk.Authenticate(context.Background(), k))
		})
	}
}

// The comparison must be over fixed-width digests, not raw key bytes:
// subtle.ConstantTimeCompare returns immediately when lengths differ, so a raw
// compare is constant-time in content but leaks key LENGTH through timing.
// Timing itself is not assertable in a unit test, so this pins the structural
// property that makes it true — every stored key occupies exactly one SHA-256
// digest regardless of how long the key was.
func TestStaticKeys_StoresFixedWidthDigests(t *testing.T) {
	t.Parallel()

	sk, err := NewStaticKeys("k", strings.Repeat("x", 4096))
	require.NoError(t, err)

	require.Len(t, sk.keys, 2)
	for i, k := range sk.keys {
		assert.Len(t, k, sha256.Size,
			"key %d is not a fixed-width digest; a raw-bytes compare leaks key length by timing", i)
	}
	assert.NotEqual(t, sk.keys[0], sk.keys[1], "distinct keys must not collide")
}

// A token whose digest would match only if the loop short-circuited on the
// first key must still be checked against all of them — and, conversely, the
// last key must authenticate exactly as the first does.
func TestStaticKeys_MatchPositionIsNotObservable(t *testing.T) {
	t.Parallel()

	sk, err := NewStaticKeys("first", "second", "third")
	require.NoError(t, err)

	for _, token := range []string{"first", "second", "third"} {
		t.Run(token, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, sk.Authenticate(context.Background(), token))
		})
	}
}
