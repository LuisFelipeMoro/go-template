// uid_test.go
package uid

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_CanonicalFormAndValid(t *testing.T) {
	t.Parallel()
	id, err := New()
	require.NoError(t, err)
	assert.Len(t, id, 36)
	assert.NoError(t, Validate(id), "generated id must validate")

	// Version nibble is 4, variant nibble is 8/9/a/b.
	assert.Equal(t, byte('4'), id[14], "version must be 4")
	assert.Contains(t, "89ab", string(id[19]), "variant must be RFC 4122")
}

func TestNew_Unique(t *testing.T) {
	t.Parallel()
	const n = 1000
	seen := make(map[string]struct{}, n)
	for range n {
		id, err := New()
		require.NoError(t, err)
		_, dup := seen[id]
		require.False(t, dup, "generated a duplicate id")
		seen[id] = struct{}{}
	}
}

func TestNew_ConcurrentRaceClean(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := New()
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid lowercase", "a2b7172e-cca4-4fa6-89e2-5f65d45e850f", false},
		{"valid uppercase", "A2B7172E-CCA4-4FA6-89E2-5F65D45E850F", false},
		{"too short", "a2b7172e-cca4-4fa6-89e2", true},
		{"too long", "a2b7172e-cca4-4fa6-89e2-5f65d45e850ff", true},
		{"missing hyphen", "a2b7172eXcca4-4fa6-89e2-5f65d45e850f", true},
		{"non-hex", "z2b7172e-cca4-4fa6-89e2-5f65d45e850f", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Validate(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
