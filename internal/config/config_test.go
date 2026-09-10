package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// isolateConfigDir points os.UserConfigDir() (and therefore Path()) at a
// fresh temp directory for the duration of the test, on every platform this
// might run on.
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("USERPROFILE", tmp) // windows, in case tests ever run there
	return tmp
}

func TestLoad_CreatesEmptyFileIfMissing(t *testing.T) {
	isolateConfigDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Connections == nil {
		t.Fatalf("Connections = nil, want empty non-nil slice")
	}
	if len(cfg.Connections) != 0 {
		t.Fatalf("Connections = %v, want empty", cfg.Connections)
	}

	path, err := Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to exist at %s: %v", path, err)
	}
}

func sampleConfig() *ConfigFile {
	return &ConfigFile{
		Connections: []Connection{
			{
				Name:   "pg-dev",
				Type:   Postgres,
				Host:   "localhost",
				Port:   5432,
				User:   "postgres",
				DBName: "app",
				Tunnel: &TunnelConfig{
					KubeContext: "dev-cluster",
					Namespace:   "default",
					TargetType:  "svc",
					TargetName:  "postgres",
					RemotePort:  5432,
					LocalPort:   15432,
				},
				Group: "dev",
			},
			{
				Name:   "mysql-staging",
				Type:   MySQL,
				Host:   "localhost",
				Port:   3306,
				User:   "root",
				DBName: "app",
				Tunnel: nil,
				Group:  "staging",
			},
			{
				Name:       "mongo-prod",
				Type:       MongoDB,
				Host:       "localhost",
				Port:       27017,
				User:       "admin",
				DBName:     "app",
				AuthSource: "admin",
				Tunnel: &TunnelConfig{
					KubeContext: "prod-cluster",
					Namespace:   "db",
					TargetType:  "pod",
					TargetName:  "mongo-0",
					RemotePort:  27017,
				},
				Group: "prod",
			},
			{
				Name:   "redis-cache",
				Type:   Redis,
				Host:   "localhost",
				Port:   6379,
				DBName: "0",
				Group:  "dev",
			},
		},
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	isolateConfigDir(t)

	original := sampleConfig()
	if err := Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !reflect.DeepEqual(original, loaded) {
		t.Fatalf("round trip mismatch:\noriginal = %+v\nloaded   = %+v", original, loaded)
	}

	// explicitly confirm the nil-tunnel entry stayed nil
	for _, c := range loaded.Connections {
		if c.Name == "mysql-staging" && c.Tunnel != nil {
			t.Fatalf("mysql-staging.Tunnel = %+v, want nil", c.Tunnel)
		}
		if c.Name == "pg-dev" {
			if c.Tunnel == nil {
				t.Fatalf("pg-dev.Tunnel = nil, want set")
			}
			want := original.Connections[0].Tunnel
			if !reflect.DeepEqual(c.Tunnel, want) {
				t.Fatalf("pg-dev.Tunnel = %+v, want %+v", c.Tunnel, want)
			}
		}
	}
}

func TestSave_NeverWritesPassword(t *testing.T) {
	isolateConfigDir(t)

	if err := Save(sampleConfig()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	path, err := Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	forbidden := []string{"password:", "pass:", "pwd:"}
	lower := strings.ToLower(string(raw))
	for _, f := range forbidden {
		if strings.Contains(lower, f) {
			t.Fatalf("config file unexpectedly contains forbidden substring %q:\n%s", f, raw)
		}
	}
}

func TestDBType_Category(t *testing.T) {
	tests := []struct {
		name  string
		input DBType
		want  StoreCategory
	}{
		{"postgres", Postgres, CategorySQL},
		{"mysql", MySQL, CategorySQL},
		{"redis", Redis, CategoryKV},
		{"mongodb", MongoDB, CategoryDocument},
		{"bogus", DBType("bogus"), StoreCategory("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.input.Category()
			if got != tt.want {
				t.Fatalf("%q.Category() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoad_MalformedYAML_ReturnsError(t *testing.T) {
	isolateConfigDir(t)

	path, err := Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	malformed := "connections: [{name: foo, type: postgres\n"
	if err := os.WriteFile(path, []byte(malformed), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err = Load()
	if err == nil {
		t.Fatalf("Load() error = nil, want non-nil for malformed YAML")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != malformed {
		t.Fatalf("malformed file was modified by Load():\nbefore = %q\nafter  = %q", malformed, after)
	}
}

func TestLoad_UnknownFields_Ignored(t *testing.T) {
	isolateConfigDir(t)

	path, err := Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `
future_feature:
  enabled: true
connections:
  - name: pg-dev
    type: postgres
    host: localhost
    port: 5432
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(cfg.Connections) != 1 || cfg.Connections[0].Name != "pg-dev" {
		t.Fatalf("Connections = %+v, want single pg-dev entry", cfg.Connections)
	}
}
