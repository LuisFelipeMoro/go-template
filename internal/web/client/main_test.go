package client

import (
	"testing"

	"go.uber.org/goleak"
)

// The outbound client owns retry/backoff timers and keep-alive connections; a
// leak here is a goroutine per failed call, which grows with error rate exactly
// when the service is least able to absorb it.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
