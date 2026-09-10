package secrets

import (
	"errors"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeBackend is an in-memory backend implementing the same interface as
// osKeyringBackend, so tests never touch the real OS keychain.
type fakeBackend struct {
	mu    sync.Mutex
	store map[string]string // key = service+"\x00"+user
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{store: make(map[string]string)}
}

func (f *fakeBackend) key(service, user string) string { return service + "\x00" + user }

func (f *fakeBackend) Set(service, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[f.key(service, user)] = password
	return nil
}

func (f *fakeBackend) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pw, ok := f.store[f.key(service, user)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return pw, nil
}

func (f *fakeBackend) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.key(service, user)
	if _, ok := f.store[k]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.store, k)
	return nil
}

func withFakeBackend(t *testing.T) *fakeBackend {
	t.Helper()
	fb := newFakeBackend()
	restore := setBackend(fb)
	t.Cleanup(restore)
	return fb
}

func TestSetGetDeletePassword_RoundTrip(t *testing.T) {
	withFakeBackend(t)

	if err := SetPassword("test-conn", "s3cr3t"); err != nil {
		t.Fatalf("SetPassword() error = %v", err)
	}

	got, err := GetPassword("test-conn")
	if err != nil {
		t.Fatalf("GetPassword() error = %v, want nil", err)
	}
	if got != "s3cr3t" {
		t.Fatalf("GetPassword() = %q, want %q", got, "s3cr3t")
	}

	if err := DeletePassword("test-conn"); err != nil {
		t.Fatalf("DeletePassword() error = %v", err)
	}

	_, err = GetPassword("test-conn")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPassword() after delete error = %v, want ErrNotFound", err)
	}
}

func TestGetPassword_NonexistentConnection_ReturnsNotFound(t *testing.T) {
	withFakeBackend(t)

	_, err := GetPassword("never-set")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPassword() error = %v, want ErrNotFound", err)
	}
}

func TestSetPassword_EmptyConnName_ReturnsError(t *testing.T) {
	fb := withFakeBackend(t)

	err := SetPassword("", "x")
	if err == nil {
		t.Fatalf("SetPassword(\"\", ...) error = nil, want validation error")
	}
	if len(fb.store) != 0 {
		t.Fatalf("expected nothing stored under empty key, store = %v", fb.store)
	}
}
