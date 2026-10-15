package security

import "errors"

// SafeError is safe to show to an MCP client. Err is for logs only.
type SafeError struct {
	Public string
	Err    error
}

func (e *SafeError) Error() string {
	if e.Public != "" {
		return e.Public
	}
	return "request failed"
}

func (e *SafeError) Unwrap() error { return e.Err }

// Public returns the client-facing message.
func Public(err error) string {
	var safe *SafeError
	if errors.As(err, &safe) && safe.Public != "" {
		return safe.Public
	}
	return "query failed"
}
