package uid

import (
	"strings"
	"testing"
)

// Validate is the gate every externally-supplied id passes through, so it is
// exactly the surface a fuzzer should own: it indexes into the string by byte
// offset, which is a panic waiting to happen if a length check ever regresses.
//
// The invariants fuzzed here are the ones no table of hand-picked cases can
// assert, because they must hold for EVERY input:
//
//  1. it never panics, whatever bytes arrive;
//  2. anything it accepts really is the canonical shape — 36 bytes, hyphens at
//     8/13/18/23, ASCII hex everywhere else. A validator that accepts something
//     outside that shape is worse than no validator, because callers downstream
//     have already stopped checking.
func FuzzValidate(f *testing.F) {
	f.Add("11111111-1111-4111-8111-111111111111")
	f.Add("00000000-0000-0000-0000-000000000000")
	f.Add("FFFFFFFF-FFFF-4FFF-BFFF-FFFFFFFFFFFF")
	f.Add("")
	f.Add("not-a-uuid")
	f.Add("11111111-1111-4111-8111-11111111111")   // 35 chars
	f.Add("11111111-1111-4111-8111-1111111111111") // 37 chars
	f.Add("11111111_1111_4111_8111_111111111111")  // wrong separators
	// A multibyte rune whose truncated low byte looks like ASCII hex — the case
	// the implementation comments call out as the reason it iterates bytes.
	f.Add("1111111а-1111-4111-8111-111111111111")

	f.Fuzz(func(t *testing.T, s string) {
		err := Validate(s)
		if err != nil {
			return // rejection is always an acceptable answer
		}

		if len(s) != 36 {
			t.Fatalf("accepted a %d-byte id: %q", len(s), s)
		}
		for i := range len(s) {
			switch i {
			case 8, 13, 18, 23:
				if s[i] != '-' {
					t.Fatalf("accepted %q with %q at separator position %d", s, s[i], i)
				}
			default:
				if !isHex(s[i]) {
					t.Fatalf("accepted %q with non-hex byte %q at position %d", s, s[i], i)
				}
			}
		}
		// Byte length 36 plus ASCII-only content means the rune count must agree;
		// a mismatch would mean a multibyte rune slipped through.
		if n := len([]rune(s)); n != 36 {
			t.Fatalf("accepted %q: 36 bytes but %d runes, so it is not ASCII", s, n)
		}
	})
}

// Every id New produces must satisfy Validate. Fuzzing the round trip guards the
// pair against drifting apart — a generator and a validator maintained
// separately is how an id format quietly forks.
func FuzzNewIsAlwaysValid(f *testing.F) {
	f.Add(uint8(1))

	f.Fuzz(func(t *testing.T, n uint8) {
		// Bound the work; the byte only varies how many ids one run checks.
		for range int(n%16) + 1 {
			id, err := New()
			if err != nil {
				t.Skip("system CSPRNG unavailable")
			}
			if err := Validate(id); err != nil {
				t.Fatalf("New produced an id Validate rejects: %q: %v", id, err)
			}
			if strings.ToLower(id) != id {
				t.Fatalf("New produced a non-lowercase id: %q", id)
			}
			if id[14] != '4' {
				t.Fatalf("New produced a non-v4 id: %q", id)
			}
			if v := id[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
				t.Fatalf("New produced an id with the wrong RFC 4122 variant: %q", id)
			}
		}
	})
}
