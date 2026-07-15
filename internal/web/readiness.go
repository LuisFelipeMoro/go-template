// readiness.go
package web

import "sync/atomic"

// Readiness is a goroutine-safe readiness flag driving the /readyz probe. It
// starts not-ready; the server flips it ready once listening and not-ready when
// shutdown begins, so Kubernetes removes the pod from Service endpoints before
// the drain.
type Readiness struct {
	ready atomic.Bool
}

// NewReadiness returns a not-ready flag.
func NewReadiness() *Readiness {
	return &Readiness{}
}

// SetReady sets the readiness state.
func (r *Readiness) SetReady(ready bool) {
	r.ready.Store(ready)
}

// Ready reports whether the service is ready for traffic.
func (r *Readiness) Ready() bool {
	return r.ready.Load()
}
