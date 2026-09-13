package uid

import "testing"

// Every request that arrives without an X-Request-ID pays for one New, so this
// sits on the hot path of the middleware chain. ReportAllocs is the number that
// matters: an allocation regression here multiplies by request rate.
func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := New(); err != nil {
			b.Fatal(err)
		}
	}
}

// Validate runs on every path-parameter id, so it is on the read path of every
// single-item endpoint. It must stay allocation-free: it only indexes into the
// string it was given.
func BenchmarkValidate(b *testing.B) {
	cases := map[string]string{
		"valid":      "11111111-1111-4111-8111-111111111111",
		"wrong_len":  "11111111-1111-4111-8111-11111111111",
		"bad_hex":    "1111111z-1111-4111-8111-111111111111",
		"bad_hyphen": "11111111_1111-4111-8111-111111111111",
	}
	for name, id := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = Validate(id)
			}
		})
	}
}
