package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bindTarget mirrors the shape a real request model has: required scalars, an
// optional pointer, and a nested struct.
type bindTarget struct {
	Name       string  `json:"name"`
	Quantity   int     `json:"quantity"`
	PriceCents int64   `json:"price_cents"`
	Note       *string `json:"note"`
	Meta       struct {
		Source string `json:"source"`
	} `json:"meta"`
}

// BindJSON is the single place untrusted bytes become Go values, which makes it
// the highest-value fuzz target in the template: every handler funnels through
// it, and it is the layer that must not panic, must not hang, and must never
// let a malformed body reach a handler as if it had parsed.
//
// The invariants asserted for EVERY input:
//
//  1. no panic — a panic here is a 500 at best and, without Recovery, a dropped
//     connection;
//  2. ok == true implies the chain was NOT aborted and nothing was written, so
//     the handler is free to respond;
//  3. ok == false implies a 4xx envelope was already written and the chain was
//     aborted, so a handler that returns on !ok can never double-write.
//
// Malformed JSON, deep nesting, huge numbers, duplicate keys, unknown members,
// invalid UTF-8 and NUL bytes are all just "another input" here — which is the
// point, since no table would think to include all of them.
func FuzzBindJSON(f *testing.F) {
	f.Add(`{"name":"widget","quantity":1,"price_cents":100}`)
	f.Add(`{}`)
	f.Add(``)
	f.Add(`null`)
	f.Add(`[]`)
	f.Add(`{"name":`)
	f.Add(`{"unknown_field":1}`)
	f.Add(`{"name":"a","name":"b"}`)
	f.Add(`{"quantity":99999999999999999999999999}`)
	f.Add(`{"note":null}`)
	f.Add(`{"meta":{"source":"x"}}`)
	f.Add(`{"name":"\ud800"}`)
	f.Add("{\"name\":\"\x00\"}")
	f.Add(strings.Repeat(`{"meta":`, 64) + `{}` + strings.Repeat(`}`, 64))

	gin.SetMode(gin.TestMode)

	f.Fuzz(func(t *testing.T, body string) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(body))

		_, ok := BindJSON[bindTarget](c)

		if ok {
			if c.IsAborted() {
				t.Fatalf("reported success but aborted the chain, body=%q", body)
			}
			if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
				t.Fatalf("reported success but already wrote %d/%q, body=%q", rec.Code, rec.Body.String(), body)
			}
			return
		}

		if !c.IsAborted() {
			t.Fatalf("reported failure without aborting the chain, body=%q", body)
		}
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("failure wrote status %d, want a 4xx, body=%q", rec.Code, body)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("failure wrote no error envelope, body=%q", body)
		}
	})
}

// The body limit is what stands between the process and an OOM bomb. Two
// distinct guarantees are fuzzed here, because conflating them hides a bug:
//
//  1. BindJSON must NEVER accept a body larger than the cap, whatever it
//     contains. This is the safety property.
//  2. A body that is VALID JSON and exceeds the cap must report 413, not a
//     generic parse error — otherwise an operator reads "malformed JSON body"
//     and blames the client for sending a payload the server chose to refuse.
//
// The second only holds for well-formed JSON: garbage bytes fail at the first
// token, before the reader ever reaches the cap, and 400 is the honest answer
// there. The fuzzer found exactly that case, which is why the padding is grown
// inside a valid document rather than thrown at the decoder raw.
func FuzzBindJSONRespectsBodyLimit(f *testing.F) {
	const limit = 64

	f.Add(uint16(0))
	f.Add(uint16(8))
	f.Add(uint16(limit))
	f.Add(uint16(4096))

	gin.SetMode(gin.TestMode)

	f.Fuzz(func(t *testing.T, padding uint16) {
		// Bound the allocation; the shape, not the size, is what varies.
		body := `{"name":"` + strings.Repeat("x", int(padding%8192)) + `"}`

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(body))
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)

		_, ok := BindJSON[bindTarget](c)

		if len(body) > limit {
			if ok {
				t.Fatalf("accepted a %d-byte body past the %d-byte cap", len(body), limit)
			}
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("valid JSON of %d bytes over the %d-byte cap reported %d, want 413",
					len(body), limit, rec.Code)
			}
			return
		}

		if !ok {
			t.Fatalf("rejected a well-formed %d-byte body inside the %d-byte cap: %d %q",
				len(body), limit, rec.Code, rec.Body.String())
		}
	})
}

// Garbage that exceeds the cap must still never be accepted — the status it
// reports is allowed to be either 413 or 400 depending on whether the decoder
// reached the cap before the first syntax error, but acceptance is never an
// option.
func FuzzBindJSONNeverAcceptsOverCapBytes(f *testing.F) {
	const limit = 64

	f.Add(strings.Repeat("x", limit+1))
	f.Add(`{"name":"` + strings.Repeat("x", 512) + `"}`)
	f.Add(strings.Repeat("{", 1024))
	f.Add("")

	gin.SetMode(gin.TestMode)

	f.Fuzz(func(t *testing.T, body string) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(body))
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)

		if _, ok := BindJSON[bindTarget](c); ok && len(body) > limit {
			t.Fatalf("accepted a %d-byte body past the %d-byte cap", len(body), limit)
		}
	})
}
