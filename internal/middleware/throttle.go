// throttle.go
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/luisfelipecoelho/go-template/internal/web"
)

// Throttle bounds the number of requests processed concurrently. It is
// backpressure, not a rate limit: a request-per-second quota belongs at the
// border (gateway/ingress), enforced globally across replicas. This only
// protects a single instance from resource exhaustion under a spike by shedding
// excess load fast. Semaphore is the built-in Limiter; any concurrency limiter
// can be injected.
type Limiter interface {
	// Acquire reserves a slot, returning false when none is free (shed the
	// request). A true result must be paired with exactly one Release.
	Acquire() bool
	// Release returns a slot reserved by a prior Acquire.
	Release()
}

// Throttle sheds requests that exceed the concurrency limit with 503 and a
// Retry-After hint. Include it in the chain only when throttling is enabled.
func Throttle(l Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.Acquire() {
			c.Header("Retry-After", "1")
			web.Error(c, http.StatusServiceUnavailable, web.CodeOverloaded, "server busy, retry shortly")
			return
		}
		defer l.Release()
		c.Next()
	}
}

// Semaphore is a fixed-capacity concurrency limiter backed by a buffered
// channel. Acquire never blocks: a full semaphore fails fast so callers shed
// load instead of queueing unboundedly. The zero value is unusable; construct
// via NewSemaphore.
type Semaphore struct {
	slots chan struct{}
}

// NewSemaphore builds a Semaphore permitting maxInFlight concurrent holders.
func NewSemaphore(maxInFlight int) *Semaphore {
	return &Semaphore{slots: make(chan struct{}, maxInFlight)}
}

// Acquire reserves a slot without blocking.
func (s *Semaphore) Acquire() bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees a previously acquired slot.
func (s *Semaphore) Release() { <-s.slots }
