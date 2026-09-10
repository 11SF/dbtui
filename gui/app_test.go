package main

import (
	"errors"
	"testing"

	"dbtui/internal/config"
	"dbtui/internal/secrets"
)

// fakeSecrets is an in-memory stand-in for the secrets package, so these
// tests never touch the real OS keychain.
type fakeSecrets struct{ store map[string]string }

func newFakeSecrets() *fakeSecrets { return &fakeSecrets{store: map[string]string{}} }

func (f *fakeSecrets) install(t *testing.T) {
	t.Helper()
	origGet, origSet, origDel := secretsGetPassword, secretsSetPassword, secretsDeletePassword
	secretsGetPassword = func(name string) (string, error) {
		pw, ok := f.store[name]
		if !ok {
			return "", secrets.ErrNotFound
		}
		return pw, nil
	}
	secretsSetPassword = func(name, pw string) error {
		f.store[name] = pw
		return nil
	}
	secretsDeletePassword = func(name string) error {
		if _, ok := f.store[name]; !ok {
			return secrets.ErrNotFound
		}
		delete(f.store, name)
		return nil
	}
	t.Cleanup(func() {
		secretsGetPassword, secretsSetPassword, secretsDeletePassword = origGet, origSet, origDel
	})
}

func newTestApp(t *testing.T) (*App, *fakeSecrets) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp+"/.config")
	t.Setenv("USERPROFILE", tmp)

	fs := newFakeSecrets()
	fs.install(t)

	a := NewApp(&config.ConfigFile{}, nil, nil)
	return a, fs
}

func TestSaveConnection_New(t *testing.T) {
	a, fs := newTestApp(t)

	err := a.SaveConnection("", config.Connection{Name: "db1", Type: config.Postgres, Host: "localhost", Port: 5432}, "s3cr3t")
	if err != nil {
		t.Fatalf("SaveConnection() error = %v", err)
	}

	conns, err := a.ListConnections()
	if err != nil || len(conns) != 1 || conns[0].Name != "db1" {
		t.Fatalf("ListConnections() = %+v, %v", conns, err)
	}
	if fs.store["db1"] != "s3cr3t" {
		t.Fatalf("password not stored: %+v", fs.store)
	}
}

// TestSaveConnection_EditBlankPassword_KeepsExisting is the core contract
// the old TUI connform had: leaving Password blank on an edit must not
// clobber the already-stored password.
func TestSaveConnection_EditBlankPassword_KeepsExisting(t *testing.T) {
	a, fs := newTestApp(t)

	if err := a.SaveConnection("", config.Connection{Name: "db1", Type: config.Postgres}, "original-pw"); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	if err := a.SaveConnection("db1", config.Connection{Name: "db1", Type: config.Postgres, Host: "newhost"}, ""); err != nil {
		t.Fatalf("edit save: %v", err)
	}

	if fs.store["db1"] != "original-pw" {
		t.Fatalf("password changed on blank-password edit: got %q, want %q", fs.store["db1"], "original-pw")
	}
	conns, _ := a.ListConnections()
	if len(conns) != 1 || conns[0].Host != "newhost" {
		t.Fatalf("edit did not apply: %+v", conns)
	}
}

// TestSaveConnection_RenameBlankPassword_MigratesStoredPassword: renaming a
// connection while leaving Password blank must move the existing secret to
// the new keyring key, not orphan it under the old name.
func TestSaveConnection_RenameBlankPassword_MigratesStoredPassword(t *testing.T) {
	a, fs := newTestApp(t)

	if err := a.SaveConnection("", config.Connection{Name: "old-name", Type: config.Postgres}, "pw"); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	if err := a.SaveConnection("old-name", config.Connection{Name: "new-name", Type: config.Postgres}, ""); err != nil {
		t.Fatalf("rename save: %v", err)
	}

	if _, ok := fs.store["old-name"]; ok {
		t.Errorf("old keyring entry still present: %+v", fs.store)
	}
	if fs.store["new-name"] != "pw" {
		t.Errorf("password not migrated to new name: %+v", fs.store)
	}
}

func TestSaveConnection_EmptyName_ReturnsError(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.SaveConnection("", config.Connection{}, "pw"); err == nil {
		t.Fatal("want error for empty connection name, got nil")
	}
}

func TestDeleteConnection_RemovesConfigAndPassword(t *testing.T) {
	a, fs := newTestApp(t)
	if err := a.SaveConnection("", config.Connection{Name: "db1", Type: config.Postgres}, "pw"); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	if err := a.DeleteConnection("db1"); err != nil {
		t.Fatalf("DeleteConnection() error = %v", err)
	}

	conns, _ := a.ListConnections()
	if len(conns) != 0 {
		t.Errorf("connection not removed from config: %+v", conns)
	}
	if _, ok := fs.store["db1"]; ok {
		t.Errorf("password not removed from keyring: %+v", fs.store)
	}
}

func TestDeleteConnection_Nonexistent_NoError(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.DeleteConnection("nope"); err != nil {
		t.Fatalf("DeleteConnection() on nonexistent connection: %v", err)
	}
}

func TestUpsertConnection_ReplacesByOriginalName(t *testing.T) {
	conns := []config.Connection{{Name: "a"}, {Name: "b"}}
	out := upsertConnection(conns, config.Connection{Name: "renamed"}, "a")
	if len(out) != 2 || out[0].Name != "renamed" || out[1].Name != "b" {
		t.Fatalf("got %+v", out)
	}
}

func TestUpsertConnection_AppendsWhenNoMatch(t *testing.T) {
	out := upsertConnection(nil, config.Connection{Name: "a"}, "")
	if len(out) != 1 || out[0].Name != "a" {
		t.Fatalf("got %+v", out)
	}
}

func TestRemoveConnectionByName(t *testing.T) {
	conns := []config.Connection{{Name: "a"}, {Name: "b"}}
	out := removeConnectionByName(conns, "a")
	if len(out) != 1 || out[0].Name != "b" {
		t.Fatalf("got %+v", out)
	}
}

func TestStatusPayload_Nil(t *testing.T) {
	p := statusPayload(nil)
	if p.Status != string(StatusDisconnected) {
		t.Fatalf("got %+v", p)
	}
}

func TestStatusPayload_WithError(t *testing.T) {
	ac := &ActiveConnection{
		Conn:      config.Connection{Name: "db1", Type: config.Postgres},
		Status:    StatusError,
		LastError: errors.New("connection refused"),
	}
	p := statusPayload(ac)
	if p.Status != string(StatusError) || p.Error == "" {
		t.Fatalf("got %+v", p)
	}
}
