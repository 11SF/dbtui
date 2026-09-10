// Package db defines the store-agnostic client interfaces every database
// driver (postgres, mysql, redis, mongo) implements, plus the pure
// formatting/heuristic helpers shared across drivers.
package db

import (
	"context"
	"time"

	"dbtui/internal/config"
)

// Client is the minimum every store type must implement.
type Client interface {
	Kind() config.DBType
	Ping(ctx context.Context) error
	Close() error
}

// --- SQL (Postgres, MySQL) ---

type SQLStore interface {
	Client
	Query(ctx context.Context, sql string) (*QueryResult, error)
	ListSchemas(ctx context.Context) ([]string, error)
	ListTables(ctx context.Context, schema string) ([]TableRef, error)
	DescribeTable(ctx context.Context, schema, table string) (*TableDescription, error)
}

type QueryResult struct {
	Columns  []string
	Rows     [][]any // each cell converted to string before rendering (see FormatValue)
	Duration time.Duration
	Capped   bool // true if the auto-LIMIT heuristic kicked in
}

type TableRef struct {
	Schema string
	Name   string
	Kind   string // "table" | "view"
}

type TableDescription struct {
	Columns     []ColumnInfo
	PrimaryKeys []string
	ForeignKeys []ForeignKeyInfo
	Indexes     []IndexInfo
}

type ColumnInfo struct {
	Name     string
	DataType string
	Nullable bool
	Default  string
}

type ForeignKeyInfo struct {
	Column         string
	RefSchema      string
	RefTable       string
	RefColumn      string
	ConstraintName string
}

type IndexInfo struct {
	Name    string
	Columns []string
	Unique  bool
}

// --- Key-Value (Redis) — M6 ---

type KVStore interface {
	Client
	ListDatabases(ctx context.Context) ([]int, error) // Redis DB indexes 0-15
	ScanKeys(ctx context.Context, db int, pattern string, cursor uint64, count int) (keys []string, nextCursor uint64, err error)
	KeyType(ctx context.Context, db int, key string) (string, error) // string/hash/list/set/zset/stream
	GetValue(ctx context.Context, db int, key string) (*KVValue, error)
	TTL(ctx context.Context, db int, key string) (time.Duration, error) // -1 = no expiry
	RunCommand(ctx context.Context, db int, args []string) (any, error)
}

type KVValue struct {
	Type   string // "string" | "hash" | "list" | "set" | "zset" | "stream"
	String string
	Hash   map[string]string
	List   []string
	Set    []string
	ZSet   []ZMember
}

type ZMember struct {
	Member string
	Score  float64
}

// --- Document (MongoDB) — M7 ---

type DocumentStore interface {
	Client
	ListDatabases(ctx context.Context) ([]string, error)
	ListCollections(ctx context.Context, database string) ([]string, error)
	Find(ctx context.Context, database, collection string, filterJSON string, limit int) (*DocResult, error)
	CountDocuments(ctx context.Context, database, collection string, filterJSON string) (int64, error)
	IndexInfo(ctx context.Context, database, collection string) ([]IndexInfo, error)
}

type DocResult struct {
	Documents []string // each doc pre-formatted as pretty JSON string
	Duration  time.Duration
}
