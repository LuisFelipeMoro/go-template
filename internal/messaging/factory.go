// factory.go
package messaging

import (
	"fmt"
	"io"
)

// New builds the Publisher/Consumer pair named by driver, plus an io.Closer to
// release it on shutdown. It takes a plain driver string (not a config type) so
// this foundation package never imports application code.
//
// To add a broker: implement Publisher and Consumer in this package (or a
// sub-package) — no broker SDK types may appear in those interfaces — and add
// one case. To detach messaging entirely, use "none"; to remove a broker,
// delete its implementation and its case. Callers depend on the interfaces, so
// nothing upstream changes.
func New(driver string) (Publisher, Consumer, io.Closer, error) {
	switch driver {
	case "memory":
		m := NewMemory()
		return m, m, m, nil
	case "none":
		d := Discard{}
		return d, d, nopCloser{}, nil
	default:
		return nil, nil, nil, fmt.Errorf("unknown bus driver %q (supported: memory, none)", driver)
	}
}
