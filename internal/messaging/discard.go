// discard.go
package messaging

import (
	"context"
	"io"
)

// Discard is a no-op Publisher and Consumer for services that do not use
// messaging (BUS_DRIVER=none). Publish drops the message; Consume blocks until
// its context is cancelled, delivering nothing. The zero value is ready to use.
type Discard struct{}

// Compile-time proof that Discard satisfies both messaging contracts.
var (
	_ Publisher = Discard{}
	_ Consumer  = Discard{}
)

// Publish drops the message and reports success.
func (Discard) Publish(context.Context, Message) error { return nil }

// Consume blocks until ctx is done, then returns nil — no messages are ever
// delivered.
func (Discard) Consume(ctx context.Context, _ string, _ Handler) error {
	<-ctx.Done()
	return nil
}

// nopCloser is an io.Closer that does nothing, paired with Discard so the
// factory can return a uniform Closer regardless of driver.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// Ensure nopCloser satisfies io.Closer.
var _ io.Closer = nopCloser{}
