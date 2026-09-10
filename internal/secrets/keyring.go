// Package secrets stores/retrieves connection passwords in the OS keyring.
//
// go-keyring talks to the real OS keychain with no built-in mock, so the
// real keyring calls are hidden behind a small backend interface. Tests
// inject an in-memory fake implementing the same interface instead of
// touching the real OS keychain.
package secrets

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const service = "dbtui"

// ErrNotFound is returned when no password is stored for a connection name.
var ErrNotFound = errors.New("secrets: password not found")

// backend is the minimal surface secrets needs from a keyring implementation.
type backend interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

// osKeyringBackend delegates to the real go-keyring package.
type osKeyringBackend struct{}

func (osKeyringBackend) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (osKeyringBackend) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyringBackend) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// current is the active backend; tests swap it out via setBackend.
var current backend = osKeyringBackend{}

// setBackend overrides the active backend and returns a restore func, so
// tests can run in isolation without touching the real OS keychain.
func setBackend(b backend) (restore func()) {
	prev := current
	current = b
	return func() { current = prev }
}

// SetPassword stores password under the given connection name.
func SetPassword(connName, password string) error {
	if connName == "" {
		return fmt.Errorf("secrets: connection name must not be empty")
	}
	if err := current.Set(service, connName, password); err != nil {
		return fmt.Errorf("secrets: set password for %q: %w", connName, err)
	}
	return nil
}

// GetPassword retrieves the password stored for the given connection name.
// Returns ErrNotFound if none is stored.
func GetPassword(connName string) (string, error) {
	pw, err := current.Get(service, connName)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("secrets: get password for %q: %w", connName, err)
	}
	return pw, nil
}

// DeletePassword removes the password stored for the given connection name.
func DeletePassword(connName string) error {
	if err := current.Delete(service, connName); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("secrets: delete password for %q: %w", connName, err)
	}
	return nil
}
