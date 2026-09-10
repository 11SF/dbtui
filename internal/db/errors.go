package db

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors so callers can distinguish network/timeout errors from
// query/syntax errors via errors.Is, and so the status bar can show a
// specific message per spec §7.
var (
	ErrTunnelTimeout = errors.New("tunnel connection timed out")
	ErrQueryTimeout  = errors.New("query timed out")
	ErrDBConn        = errors.New("database connection error")
)

// TimeoutError wraps ErrTunnelTimeout or ErrQueryTimeout with the operation
// that timed out.
type TimeoutError struct {
	Op  string
	Err error
}

func (e *TimeoutError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *TimeoutError) Unwrap() error { return e.Err }

// ConnError wraps ErrDBConn with a human-readable detail message. Detail
// must already have any secret masked (see MaskSecret) before being passed
// in — this type does not do the masking itself, since it doesn't always
// know the password in scope.
type ConnError struct {
	Detail string
	Err    error
}

func (e *ConnError) Error() string { return fmt.Sprintf("%s: %v", e.Detail, e.Err) }
func (e *ConnError) Unwrap() error { return e.Err }

// QueryError wraps a query/syntax error returned by a driver, as opposed to
// a connection or timeout failure.
type QueryError struct {
	Query string
	Err   error
}

func (e *QueryError) Error() string { return fmt.Sprintf("query error: %v", e.Err) }
func (e *QueryError) Unwrap() error { return e.Err }

// WrapConnError builds a ConnError from a raw driver error, masking every
// occurrence of password in both the detail and the underlying error text
// (spec §7: passwords must never be logged or appear in any printed error
// message). Driver packages (postgres, mysql, redis, mongo) should route
// every connection failure through this instead of wrapping the raw error
// directly.
func WrapConnError(detail string, err error, password string) error {
	maskedDetail := MaskSecret(detail, password)
	maskedErr := fmt.Errorf("%w: %s", ErrDBConn, MaskSecret(err.Error(), password))
	return &ConnError{Detail: maskedDetail, Err: maskedErr}
}

// MaskSecret replaces every occurrence of secret in s with "****". Used
// wherever a connection string or error might otherwise embed a raw
// password before being logged or printed (spec §7: "Passwords must never
// be logged or appear in any printed error message"). A blank secret is a
// no-op so callers don't need to special-case a not-yet-known password.
func MaskSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "****")
}
