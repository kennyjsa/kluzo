package ports

import "errors"

// Errors adapters return, wrapped with context. Callers match them with
// errors.Is; the outbox executor uses IsPermanent to decide between retrying
// and failing an effect (docs/contracts/data-model.md).
var (
	// ErrNotFound means the referenced resource does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid means the tracker rejected the request as malformed or
	// impossible (for example a PR without commits, or a second orch/* label).
	ErrInvalid = errors.New("invalid request")
	// ErrPermanent means retrying cannot help (bad or insufficient credentials).
	ErrPermanent = errors.New("permanent failure")
)

// IsPermanent reports whether err is a failure that retrying cannot fix:
// invalid credentials, a missing resource or a request the tracker rejects.
func IsPermanent(err error) bool {
	return errors.Is(err, ErrPermanent) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid)
}
