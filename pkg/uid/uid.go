// Package uid generates and validates RFC 4122 version-4 UUIDs using only the
// standard library (crypto/rand), so the template carries no UUID dependency.
// A little copying beats a little dependency.
package uid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// New returns a random (version 4) UUID in canonical 8-4-4-4-12 form. It errors
// only if the system CSPRNG is unavailable — an unrecoverable environment
// fault, surfaced as a value rather than a panic.
func New() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10 (RFC 4122)

	var buf [36]byte
	encodeCanonical(&buf, b)
	return string(buf[:]), nil
}

// Validate reports whether s is a canonical UUID: 36 chars, hyphens at 8-13-18-
// 23, all other positions lowercase or uppercase hex. It does not restrict the
// version, matching the leniency callers expect when accepting external ids.
func Validate(s string) error {
	if len(s) != 36 {
		return fmt.Errorf("uid %q: must be 36 characters", s)
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return fmt.Errorf("uid %q: expected '-' at position %d", s, i)
			}
		default:
			if !isHex(byte(r)) {
				return fmt.Errorf("uid %q: non-hex character at position %d", s, i)
			}
		}
	}
	return nil
}

// encodeCanonical writes the 8-4-4-4-12 hyphenated hex form of b into buf.
func encodeCanonical(buf *[36]byte, b [16]byte) {
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
