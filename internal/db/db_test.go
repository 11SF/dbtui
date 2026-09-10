package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dbtui/internal/config"
)

// TestNoPasswordInLogs is the automated guard behind spec §7's "Passwords
// must never be logged or appear in any printed error message": every path
// that formats a connection error must route through WrapConnError/
// MaskSecret, and the result must never contain the raw password substring
// (test spec §8.1).
func TestNoPasswordInLogs(t *testing.T) {
	const password = "s3cr3t-p4ssw0rd"

	t.Run("MaskSecret strips every occurrence", func(t *testing.T) {
		in := "postgres://user:" + password + "@host:5432/db failed, retrying with " + password + " again"
		got := MaskSecret(in, password)
		if strings.Contains(got, password) {
			t.Fatalf("MaskSecret left the password in the output: %q", got)
		}
	})

	t.Run("MaskSecret is a no-op for an empty secret", func(t *testing.T) {
		in := "no secret known yet"
		if got := MaskSecret(in, ""); got != in {
			t.Fatalf("MaskSecret(%q, \"\") = %q, want unchanged", in, got)
		}
	})

	t.Run("WrapConnError masks both detail and underlying error text", func(t *testing.T) {
		underlying := errors.New("dial tcp: auth failed for password " + password)
		err := WrapConnError("connect user:"+password+"@host:5432", underlying, password)
		if strings.Contains(err.Error(), password) {
			t.Fatalf("WrapConnError leaked the password: %v", err)
		}
		if !errors.Is(err, ErrDBConn) {
			t.Fatalf("WrapConnError result does not wrap ErrDBConn: %v", err)
		}
	})
}

func TestFactory_NewClient_UnsupportedType_ReturnsError(t *testing.T) {
	_, err := NewClient(context.Background(), config.Connection{Type: config.DBType("bogus")}, "pw")
	if err == nil {
		t.Fatalf("NewClient() error = nil, want non-nil for unsupported type")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("NewClient() error = %q, want it to mention the offending type %q", err.Error(), "bogus")
	}
}

func TestFormatValue(t *testing.T) {
	ts := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, NullValue{}},
		{"int64", int64(42), "42"},
		{"float64", float64(3.14), "3.14"},
		{"bytes", []byte("hello"), "hello"},
		{"time", ts, ts.Format(time.RFC3339)},
		{"bool", true, "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatValue(tt.in)
			if got != tt.want {
				t.Fatalf("FormatValue(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSQLStore_AutoLimitHeuristic(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantSQL    string
		wantCapped bool
	}{
		{
			name:       "bare select",
			in:         "SELECT * FROM users",
			wantSQL:    "SELECT * FROM users LIMIT 1000",
			wantCapped: true,
		},
		{
			name:       "lowercase select",
			in:         "select id from t",
			wantSQL:    "select id from t LIMIT 1000",
			wantCapped: true,
		},
		{
			name:       "existing limit",
			in:         "SELECT * FROM users LIMIT 50",
			wantSQL:    "SELECT * FROM users LIMIT 50",
			wantCapped: false,
		},
		{
			name:       "existing limit and offset",
			in:         "SELECT * FROM users LIMIT 50 OFFSET 10",
			wantSQL:    "SELECT * FROM users LIMIT 50 OFFSET 10",
			wantCapped: false,
		},
		{
			name:       "update statement",
			in:         "UPDATE users SET x=1",
			wantSQL:    "UPDATE users SET x=1",
			wantCapped: false,
		},
		{
			name:       "insert statement",
			in:         "INSERT INTO t VALUES (1)",
			wantSQL:    "INSERT INTO t VALUES (1)",
			wantCapped: false,
		},
		{
			name:       "leading and trailing whitespace",
			in:         "  SELECT * FROM users  ",
			wantSQL:    "SELECT * FROM users LIMIT 1000",
			wantCapped: true,
		},
		{
			name:       "multi-statement left untouched",
			in:         "SELECT * FROM users; SELECT * FROM orders",
			wantSQL:    "SELECT * FROM users; SELECT * FROM orders",
			wantCapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSQL, gotCapped := ApplyAutoLimit(tt.in)
			if gotSQL != tt.wantSQL {
				t.Errorf("ApplyAutoLimit(%q) sql = %q, want %q", tt.in, gotSQL, tt.wantSQL)
			}
			if strings.Contains(gotSQL, "  LIMIT") {
				t.Errorf("ApplyAutoLimit(%q) produced double space before LIMIT: %q", tt.in, gotSQL)
			}
			if gotCapped != tt.wantCapped {
				t.Errorf("ApplyAutoLimit(%q) capped = %v, want %v", tt.in, gotCapped, tt.wantCapped)
			}
		})
	}
}

// --- fakes shared by 3.7/3.8/3.9 ---

type fakeKVStore struct {
	pages [][]string // one slice of keys per ScanKeys call, in order
	calls int
}

func (f *fakeKVStore) Kind() config.DBType            { return config.Redis }
func (f *fakeKVStore) Ping(ctx context.Context) error { return nil }
func (f *fakeKVStore) Close() error                   { return nil }

func (f *fakeKVStore) ListDatabases(ctx context.Context) ([]int, error) { return []int{0}, nil }

func (f *fakeKVStore) ScanKeys(ctx context.Context, dbIndex int, pattern string, cursor uint64, count int) ([]string, uint64, error) {
	idx := int(cursor)
	if idx >= len(f.pages) {
		return nil, 0, nil
	}
	f.calls++
	keys := f.pages[idx]
	next := uint64(idx + 1)
	if next >= uint64(len(f.pages)) {
		next = 0
	}
	return keys, next, nil
}

func (f *fakeKVStore) KeyType(ctx context.Context, dbIndex int, key string) (string, error) {
	return "string", nil
}
func (f *fakeKVStore) GetValue(ctx context.Context, dbIndex int, key string) (*KVValue, error) {
	return &KVValue{Type: "string", String: "v"}, nil
}
func (f *fakeKVStore) TTL(ctx context.Context, dbIndex int, key string) (time.Duration, error) {
	return -1, nil
}
func (f *fakeKVStore) RunCommand(ctx context.Context, dbIndex int, args []string) (any, error) {
	return nil, nil
}

func TestKVStore_ScanKeys_Pagination(t *testing.T) {
	fake := &fakeKVStore{
		pages: [][]string{
			{"key:1", "key:2"},
			{"key:3"},
			{"key:4", "key:5"},
		},
	}

	got, err := ScanAllKeys(context.Background(), fake, 0, "key:*", 2)
	if err != nil {
		t.Fatalf("ScanAllKeys() error = %v", err)
	}

	want := []string{"key:1", "key:2", "key:3", "key:4", "key:5"}
	if len(got) != len(want) {
		t.Fatalf("ScanAllKeys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ScanAllKeys()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	if fake.calls != len(fake.pages) {
		t.Fatalf("ScanKeys called %d times, want exactly %d (should stop once nextCursor == 0)", fake.calls, len(fake.pages))
	}

	seen := make(map[string]bool)
	for _, k := range got {
		if seen[k] {
			t.Fatalf("duplicate key %q in accumulated result %v", k, got)
		}
		seen[k] = true
	}
}

func TestKVValue_TypeDispatch(t *testing.T) {
	tests := []struct {
		name string
		v    *KVValue
		want string
	}{
		{
			name: "string",
			v:    &KVValue{Type: "string", String: "hello", Hash: map[string]string{"ignored": "x"}},
			want: "hello",
		},
		{
			name: "hash",
			v:    &KVValue{Type: "hash", Hash: map[string]string{"field": "value"}, String: "ignored"},
			want: "field: value",
		},
		{
			name: "list",
			v:    &KVValue{Type: "list", List: []string{"a", "b"}, String: "ignored"},
			want: "a\nb",
		},
		{
			name: "set",
			v:    &KVValue{Type: "set", Set: []string{"x", "y"}, String: "ignored"},
			want: "x\ny",
		},
		{
			name: "zset",
			v:    &KVValue{Type: "zset", ZSet: []ZMember{{Member: "m1", Score: 1}, {Member: "m2", Score: 2}}, String: "ignored"},
			want: "m1\nm2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("RenderKVValue panicked: %v", r)
				}
			}()
			got := RenderKVValue(tt.v)
			if got != tt.want {
				t.Fatalf("RenderKVValue(%+v) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}

func TestDocumentStore_Find_DefaultLimit(t *testing.T) {
	if got := EffectiveFindLimit(0); got != 100 {
		t.Fatalf("EffectiveFindLimit(0) = %d, want 100", got)
	}
	if got := EffectiveFindLimit(5000); got != 5000 {
		t.Fatalf("EffectiveFindLimit(5000) = %d, want 5000 (explicit values must not be clamped)", got)
	}
}
