package messaging

import (
	"testing"

	"go.uber.org/goleak"
)

// This package starts goroutines, so every test in it is checked for leaks: a
// goroutine that outlives the test that spawned it is a goroutine that would
// outlive a request or a shutdown in production, growing until OOM. Failing
// here is the only cheap place to catch it — pprof at 3am is the expensive one.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
