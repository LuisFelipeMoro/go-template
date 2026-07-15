package auth

import (
	"context"
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
