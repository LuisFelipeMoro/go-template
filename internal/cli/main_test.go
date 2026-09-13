package cli

import (
	"testing"

	"go.uber.org/goleak"
)

// The composition root starts the whole process graph, so a leak here means a
// component the lifecycle Runner failed to stop — the exact defect that makes a
// pod hang past its termination grace period and get SIGKILLed mid-drain.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
