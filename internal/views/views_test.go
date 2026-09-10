package views

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dbtui/internal/app"
	"dbtui/internal/config"
	"dbtui/internal/db"
)

// --- fakes implementing exactly one capability interface each, plus a
// bare-Client fake implementing none, for test spec §7.1/§7.2. ---

type bareClient struct{}

func (bareClient) Kind() config.DBType        { return config.DBType("bare") }
func (bareClient) Ping(context.Context) error { return nil }
func (bareClient) Close() error               { return nil }

type fakeSQLClient struct{ bareClient }

func (fakeSQLClient) Query(context.Context, string) (*db.QueryResult, error)    { return nil, nil }
func (fakeSQLClient) ListSchemas(context.Context) ([]string, error)             { return nil, nil }
func (fakeSQLClient) ListTables(context.Context, string) ([]db.TableRef, error) { return nil, nil }
func (fakeSQLClient) DescribeTable(context.Context, string, string) (*db.TableDescription, error) {
	return nil, nil
}

type fakeKVClient struct{ bareClient }

func (fakeKVClient) ListDatabases(context.Context) ([]int, error) { return nil, nil }
func (fakeKVClient) ScanKeys(context.Context, int, string, uint64, int) ([]string, uint64, error) {
	return nil, 0, nil
}
func (fakeKVClient) KeyType(context.Context, int, string) (string, error)       { return "", nil }
func (fakeKVClient) GetValue(context.Context, int, string) (*db.KVValue, error) { return nil, nil }
func (fakeKVClient) TTL(context.Context, int, string) (time.Duration, error)    { return 0, nil }
func (fakeKVClient) RunCommand(context.Context, int, []string) (any, error)     { return nil, nil }

type fakeDocClient struct{ bareClient }

func (fakeDocClient) ListDatabases(context.Context) ([]string, error)           { return nil, nil }
func (fakeDocClient) ListCollections(context.Context, string) ([]string, error) { return nil, nil }
func (fakeDocClient) Find(context.Context, string, string, string, int) (*db.DocResult, error) {
	return nil, nil
}
func (fakeDocClient) CountDocuments(context.Context, string, string, string) (int64, error) {
	return 0, nil
}
func (fakeDocClient) IndexInfo(context.Context, string, string) ([]db.IndexInfo, error) {
	return nil, nil
}

func TestBrowserView_CapabilityDispatch(t *testing.T) {
	tests := []struct {
		name    string
		client  db.Client
		want    config.StoreCategory
		wantErr bool
	}{
		{"sql only", fakeSQLClient{}, config.CategorySQL, false},
		{"kv only", fakeKVClient{}, config.CategoryKV, false},
		{"document only", fakeDocClient{}, config.CategoryDocument, false},
		{"none", bareClient{}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectBrowserMode(tt.client)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (category=%v)", got)
				}
				if !errors.Is(err, ErrUnsupportedClient) {
					t.Fatalf("expected ErrUnsupportedClient, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryView_ModeSelection(t *testing.T) {
	tests := []struct {
		name    string
		dbType  config.DBType
		want    config.StoreCategory
		wantErr bool
	}{
		{"postgres", config.Postgres, config.CategorySQL, false},
		{"mysql", config.MySQL, config.CategorySQL, false},
		{"redis", config.Redis, config.CategoryKV, false},
		{"mongodb", config.MongoDB, config.CategoryDocument, false},
		{"unknown", config.DBType("bogus"), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectQueryMode(tt.dbType)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (mode=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResultView_DocResultFormatting(t *testing.T) {
	res := &db.DocResult{Documents: []string{
		`{"a": 1}`,
		`{"b": 2}`,
		`{"c": 3}`,
	}}
	got := FormatDocResult(res)

	for _, doc := range res.Documents {
		if !strings.Contains(got, doc) {
			t.Fatalf("output missing document %q; got: %q", doc, got)
		}
	}

	// order preserved
	iA := strings.Index(got, res.Documents[0])
	iB := strings.Index(got, res.Documents[1])
	iC := strings.Index(got, res.Documents[2])
	if !(iA < iB && iB < iC) {
		t.Fatalf("documents out of order in output: %q", got)
	}

	// separated by the documented divider, no truncation
	parts := strings.Split(got, docDivider)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts split on divider, got %d: %q", len(parts), got)
	}
	for i, want := range res.Documents {
		if parts[i] != want {
			t.Fatalf("part %d = %q, want %q (truncation?)", i, parts[i], want)
		}
	}
}

func TestDestructiveCommandDetection(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"FLUSHDB", true},
		{"flushall", true},
		{"DEL somekey", true},
		{"UNLINK a b c", true},
		{"GET somekey", false},
		{"SET x 1", false},
		{"DELAY_SOMETHING", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := IsDestructiveRedisCommand(tt.input)
			if got != tt.want {
				t.Fatalf("IsDestructiveRedisCommand(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestHistoryEntry_PreviewTruncation(t *testing.T) {
	t.Run("ascii over 60 chars truncates to exactly 60 runes plus ellipsis", func(t *testing.T) {
		long := strings.Repeat("a", 100)
		got := TruncatePreview(long)
		runes := []rune(got)
		if string(runes[len(runes)-3:]) != "..." {
			t.Fatalf("expected ellipsis suffix, got %q", got)
		}
		if len(runes)-3 != 60 {
			t.Fatalf("expected 60 runes before ellipsis, got %d in %q", len(runes)-3, got)
		}
	})

	t.Run("short string unchanged", func(t *testing.T) {
		short := "SELECT 1"
		if got := TruncatePreview(short); got != short {
			t.Fatalf("got %q, want unchanged %q", got, short)
		}
	})

	t.Run("thai multi-byte runes never split", func(t *testing.T) {
		// Repeat a Thai phrase well past 60 runes; every rune in the input
		// is multi-byte in UTF-8, so any byte-based truncation bug would
		// produce invalid UTF-8 (replacement characters) in the output.
		thai := strings.Repeat("ทดสอบภาษาไทยสำหรับการตัดคำ", 5)
		got := TruncatePreview(thai)
		if !isValidUTF8(got) {
			t.Fatalf("truncated output is not valid UTF-8: %q", got)
		}
		runes := []rune(got)
		if string(runes[len(runes)-3:]) != "..." {
			t.Fatalf("expected ellipsis suffix, got %q", got)
		}
		if len(runes)-3 != 60 {
			t.Fatalf("expected 60 runes before ellipsis, got %d", len(runes)-3)
		}
	})
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestConnList_StatusColorMapping(t *testing.T) {
	tests := []struct {
		status app.ConnStatus
		want   string
	}{
		{app.StatusConnected, "green"},
		{app.StatusDisconnected, "gray"},
		{app.StatusError, "red"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := StatusColor(tt.status)
			if got != tt.want {
				t.Fatalf("StatusColor(%v) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}

	t.Run("in-progress statuses share a distinct color not used by connected/disconnected/error", func(t *testing.T) {
		used := map[string]bool{"green": true, "gray": true, "red": true}
		tunnelConnecting := StatusColor(app.StatusTunnelConnecting)
		dbConnecting := StatusColor(app.StatusDBConnecting)
		if tunnelConnecting != dbConnecting {
			t.Fatalf("StatusTunnelConnecting=%q and StatusDBConnecting=%q should share one in-progress color", tunnelConnecting, dbConnecting)
		}
		if used[tunnelConnecting] {
			t.Fatalf("in-progress color %q must not be reused from connected/disconnected/error", tunnelConnecting)
		}
	})
}

func TestConnForm_FieldVisibilityByType(t *testing.T) {
	tests := []struct {
		dbType  config.DBType
		present []string
		absent  []string
	}{
		{config.Postgres, []string{"Host", "Port", "User", "Password", "DBName"}, []string{"AuthSource"}},
		{config.MySQL, []string{"Host", "Port", "User", "Password", "DBName"}, []string{"AuthSource"}},
		{config.Redis, []string{"Host", "Port", "Password"}, []string{"User", "AuthSource"}},
		{config.MongoDB, []string{"Host", "Port", "User", "Password", "DBName", "AuthSource"}, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.dbType), func(t *testing.T) {
			got := visibleFields(tt.dbType)
			set := map[string]bool{}
			for _, f := range got {
				set[f] = true
			}
			for _, want := range tt.present {
				if !set[want] {
					t.Errorf("expected field %q present for %s, got %v", want, tt.dbType, got)
				}
			}
			for _, notWant := range tt.absent {
				if set[notWant] {
					t.Errorf("expected field %q absent for %s, got %v", notWant, tt.dbType, got)
				}
			}
		})
	}
}
