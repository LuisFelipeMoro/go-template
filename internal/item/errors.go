// errors.go
package item

import "errors"

// Sentinel domain errors — transports map these to protocol-specific codes
// with errors.Is; never match on error strings.
var (
	// ErrNotFound reports that the requested item does not exist.
	ErrNotFound = errors.New("item not found")

	// ErrInvalidArgument reports a violated business invariant. Wrap it with
	// field detail: fmt.Errorf("name too long: %w", ErrInvalidArgument).
	ErrInvalidArgument = errors.New("invalid argument")
)
