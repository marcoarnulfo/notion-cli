package notion

import (
	"errors"
	"fmt"
)

// Sentinel errors callers match with errors.Is.
var (
	ErrUnauthorized = errors.New("notion: unauthorized")
	ErrNotFound     = errors.New("notion: object not found")
	ErrRateLimited  = errors.New("notion: rate limited")
	// ErrAmbiguousWrite marks a non-idempotent write whose outcome is unknown:
	// a transport error or a 500/502/504 that may have been applied before the
	// failure. Callers surface it as "re-run to converge" rather than retrying
	// automatically, which could duplicate.
	ErrAmbiguousWrite = errors.New("notion: write outcome unknown; re-run to converge")
)

// APIError is a structured Notion error response. It deliberately carries no
// request context beyond status, code, message and the Retry-After header so
// that a token can never end up inside it.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter string // raw Retry-After header, seconds, empty when absent
}

func (e *APIError) Error() string {
	return fmt.Sprintf("notion: %s (%d): %s", e.Code, e.Status, e.Message)
}

// Is lets errors.Is match an APIError against the sentinels above.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == 401 || e.Status == 403
	case ErrNotFound:
		return e.Status == 404
	case ErrRateLimited:
		return e.Status == 429
	}
	return false
}
