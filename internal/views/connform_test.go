package views

import (
	"errors"
	"testing"

	"dbtui/internal/config"
)

func TestConnFormValuesFromConnection(t *testing.T) {
	c := config.Connection{
		Name: "prod-pg", Type: config.Postgres, Host: "db.internal", Port: 5432,
		User: "app", DBName: "appdb", AuthSource: "admin", Group: "prod",
	}
	values := ConnFormValuesFromConnection(c)

	want := map[string]string{
		"Host": "db.internal", "Port": "5432", "User": "app",
		"DBName": "appdb", "AuthSource": "admin", "Group": "prod",
	}
	for k, wantV := range want {
		if values[k] != wantV {
			t.Errorf("values[%q] = %q, want %q", k, values[k], wantV)
		}
	}

	if _, hasPassword := values["Password"]; hasPassword && values["Password"] != "" {
		t.Errorf("Password must never be pre-filled from an existing connection, got %q", values["Password"])
	}

	t.Run("zero port omitted rather than rendered as 0", func(t *testing.T) {
		got := ConnFormValuesFromConnection(config.Connection{})
		if got["Port"] != "" {
			t.Errorf("Port = %q, want empty for a zero-value connection", got["Port"])
		}
	})
}

func TestConnFormTunnelValuesFromConnection(t *testing.T) {
	t.Run("nil tunnel", func(t *testing.T) {
		values, useTunnel := ConnFormTunnelValuesFromConnection(config.Connection{})
		if useTunnel {
			t.Fatalf("useTunnel = true, want false for a connection with no tunnel")
		}
		if values["TargetType"] != "pod" {
			t.Errorf("TargetType default = %q, want %q", values["TargetType"], "pod")
		}
	})

	t.Run("populated tunnel", func(t *testing.T) {
		c := config.Connection{Tunnel: &config.TunnelConfig{
			KubeContext: "prod-ctx", Namespace: "db", TargetType: "svc",
			TargetName: "postgres", RemotePort: 5432, LocalPort: 15432,
		}}
		values, useTunnel := ConnFormTunnelValuesFromConnection(c)
		if !useTunnel {
			t.Fatalf("useTunnel = false, want true")
		}
		want := map[string]string{
			"KubeContext": "prod-ctx", "Namespace": "db", "TargetType": "svc",
			"TargetName": "postgres", "RemotePort": "5432", "LocalPort": "15432",
		}
		for k, wantV := range want {
			if values[k] != wantV {
				t.Errorf("values[%q] = %q, want %q", k, values[k], wantV)
			}
		}
	})
}

func TestBuildConnectionFromForm(t *testing.T) {
	t.Run("valid postgres, no tunnel", func(t *testing.T) {
		values := map[string]string{"Host": "localhost", "Port": "5432", "User": "app", "DBName": "appdb"}
		c, err := BuildConnectionFromForm("prod-pg", config.Postgres, values, false, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Name != "prod-pg" || c.Host != "localhost" || c.Port != 5432 || c.Tunnel != nil {
			t.Fatalf("got %+v", c)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		_, err := BuildConnectionFromForm("", config.Postgres, map[string]string{"Host": "x", "Port": "1", "User": "u", "DBName": "d"}, false, false, nil)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "Name" {
			t.Fatalf("got err=%v, want ValidationError{Field: Name}", err)
		}
	})

	t.Run("missing required field for type", func(t *testing.T) {
		_, err := BuildConnectionFromForm("n", config.Postgres, map[string]string{"Port": "1"}, false, false, nil)
		if err == nil {
			t.Fatalf("expected validation error for missing Host")
		}
	})

	t.Run("non-numeric port", func(t *testing.T) {
		values := map[string]string{"Host": "h", "Port": "not-a-number", "User": "u", "DBName": "d"}
		_, err := BuildConnectionFromForm("n", config.Postgres, values, false, false, nil)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "Port" {
			t.Fatalf("got err=%v, want ValidationError{Field: Port}", err)
		}
	})

	t.Run("password required for new connection", func(t *testing.T) {
		values := map[string]string{"Host": "h", "Port": "1", "User": "u", "DBName": "d"}
		_, err := BuildConnectionFromForm("n", config.Postgres, values, true, false, nil)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "Password" {
			t.Fatalf("got err=%v, want ValidationError{Field: Password}", err)
		}
	})

	t.Run("edit without touching password succeeds", func(t *testing.T) {
		values := map[string]string{"Host": "h", "Port": "1", "User": "u", "DBName": "d"}
		_, err := BuildConnectionFromForm("n", config.Postgres, values, false, false, nil)
		if err != nil {
			t.Fatalf("unexpected error on edit with blank password: %v", err)
		}
	})

	t.Run("redis has no User/AuthSource requirement", func(t *testing.T) {
		values := map[string]string{"Host": "h", "Port": "6379"}
		c, err := BuildConnectionFromForm("cache", config.Redis, values, false, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.User != "" {
			t.Fatalf("got User=%q, want empty for redis", c.User)
		}
	})

	t.Run("with tunnel, valid", func(t *testing.T) {
		values := map[string]string{"Host": "localhost", "Port": "5432", "User": "u", "DBName": "d"}
		tunnelValues := map[string]string{
			"KubeContext": "ctx", "Namespace": "ns", "TargetType": "svc",
			"TargetName": "postgres", "RemotePort": "5432", "LocalPort": "15432",
		}
		c, err := BuildConnectionFromForm("n", config.Postgres, values, false, true, tunnelValues)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Tunnel == nil {
			t.Fatalf("expected non-nil Tunnel")
		}
		if c.Tunnel.RemotePort != 5432 || c.Tunnel.LocalPort != 15432 || c.Tunnel.TargetType != "svc" {
			t.Fatalf("got tunnel %+v", c.Tunnel)
		}
	})

	t.Run("with tunnel, missing namespace", func(t *testing.T) {
		values := map[string]string{"Host": "h", "Port": "1", "User": "u", "DBName": "d"}
		tunnelValues := map[string]string{"TargetName": "postgres", "RemotePort": "5432"}
		_, err := BuildConnectionFromForm("n", config.Postgres, values, false, true, tunnelValues)
		if err == nil {
			t.Fatalf("expected error for missing tunnel Namespace")
		}
	})

	t.Run("with tunnel, missing remote port", func(t *testing.T) {
		values := map[string]string{"Host": "h", "Port": "1", "User": "u", "DBName": "d"}
		tunnelValues := map[string]string{"Namespace": "ns", "TargetName": "postgres"}
		_, err := BuildConnectionFromForm("n", config.Postgres, values, false, true, tunnelValues)
		if err == nil {
			t.Fatalf("expected error for missing tunnel RemotePort")
		}
	})

	t.Run("local port optional, auto-assign", func(t *testing.T) {
		values := map[string]string{"Host": "h", "Port": "1", "User": "u", "DBName": "d"}
		tunnelValues := map[string]string{"Namespace": "ns", "TargetName": "postgres", "RemotePort": "5432"}
		c, err := BuildConnectionFromForm("n", config.Postgres, values, false, true, tunnelValues)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Tunnel.LocalPort != 0 {
			t.Fatalf("LocalPort = %d, want 0 (auto-assign)", c.Tunnel.LocalPort)
		}
	})
}

func TestUpsertConnection(t *testing.T) {
	base := []config.Connection{
		{Name: "a", Type: config.Postgres},
		{Name: "b", Type: config.Redis},
	}

	t.Run("append new", func(t *testing.T) {
		got := UpsertConnection(base, config.Connection{Name: "c", Type: config.MySQL}, "")
		if len(got) != 3 || got[2].Name != "c" {
			t.Fatalf("got %+v", got)
		}
		if len(base) != 2 {
			t.Fatalf("input slice mutated, got len %d", len(base))
		}
	})

	t.Run("replace by name (no rename)", func(t *testing.T) {
		got := UpsertConnection(base, config.Connection{Name: "b", Type: config.MongoDB}, "")
		if len(got) != 2 || got[1].Type != config.MongoDB {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("replace by originalName (rename)", func(t *testing.T) {
		got := UpsertConnection(base, config.Connection{Name: "b-renamed", Type: config.Redis}, "b")
		if len(got) != 2 {
			t.Fatalf("got %+v, want len 2 (rename replaces in place)", got)
		}
		if got[1].Name != "b-renamed" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestRemoveConnectionByName(t *testing.T) {
	base := []config.Connection{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}

	got := RemoveConnectionByName(base, "b")
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("got %+v", got)
	}
	if len(base) != 3 {
		t.Fatalf("input slice mutated, got len %d", len(base))
	}

	t.Run("name not present is a no-op", func(t *testing.T) {
		got := RemoveConnectionByName(base, "missing")
		if len(got) != 3 {
			t.Fatalf("got %+v, want unchanged copy", got)
		}
	})

	t.Run("nil slice", func(t *testing.T) {
		got := RemoveConnectionByName(nil, "x")
		if len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})
}
