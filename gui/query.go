package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbtui/internal/config"
	"dbtui/internal/db"
	"dbtui/internal/history"
	"dbtui/internal/secrets"
)

func (a *App) recordHistory(e *history.HistoryEntry) {
	if a.hist != nil {
		_ = a.hist.Insert(e)
	}
}

func (a *App) requireSQL() (db.SQLStore, *ActiveConnection, error) {
	ac := a.getActive()
	if ac == nil || ac.Client == nil {
		return nil, ac, fmt.Errorf("gui: no active connection")
	}
	store, ok := ac.Client.(db.SQLStore)
	if !ok {
		return nil, ac, fmt.Errorf("gui: active connection does not support SQL queries")
	}
	return store, ac, nil
}

func (a *App) requireKV() (db.KVStore, *ActiveConnection, error) {
	ac := a.getActive()
	if ac == nil || ac.Client == nil {
		return nil, ac, fmt.Errorf("gui: no active connection")
	}
	store, ok := ac.Client.(db.KVStore)
	if !ok {
		return nil, ac, fmt.Errorf("gui: active connection does not support KV commands")
	}
	return store, ac, nil
}

func (a *App) requireDoc() (db.DocumentStore, *ActiveConnection, error) {
	ac := a.getActive()
	if ac == nil || ac.Client == nil {
		return nil, ac, fmt.Errorf("gui: no active connection")
	}
	store, ok := ac.Client.(db.DocumentStore)
	if !ok {
		return nil, ac, fmt.Errorf("gui: active connection does not support document queries")
	}
	return store, ac, nil
}

// --- SQL mode ---

func (a *App) ListSchemas() ([]string, error) {
	store, _, err := a.requireSQL()
	if err != nil {
		return nil, err
	}
	return store.ListSchemas(context.Background())
}

func (a *App) ListTables(schema string) ([]db.TableRef, error) {
	store, _, err := a.requireSQL()
	if err != nil {
		return nil, err
	}
	return store.ListTables(context.Background(), schema)
}

func (a *App) DescribeTable(schema, table string) (*db.TableDescription, error) {
	store, _, err := a.requireSQL()
	if err != nil {
		return nil, err
	}
	return store.DescribeTable(context.Background(), schema, table)
}

// ListSQLDatabases lists every database on the active SQL server (not just
// the one currently connected to) — the backing call for a connection's
// "show all databases" mode. Both Postgres and MySQL expose this via a
// plain SELECT that the existing SQLStore.Query already runs, so no new
// capability-interface method is needed.
func (a *App) ListSQLDatabases() ([]string, error) {
	store, ac, err := a.requireSQL()
	if err != nil {
		return nil, err
	}

	var sql string
	switch ac.Conn.Type {
	case config.Postgres:
		sql = `SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname`
	case config.MySQL:
		sql = `SELECT schema_name FROM information_schema.schemata ORDER BY schema_name`
	default:
		return nil, fmt.Errorf("gui: listing databases is not supported for %s", ac.Conn.Type)
	}

	ctx, cancel := context.WithTimeout(context.Background(), ac.Conn.QueryTimeout())
	defer cancel()
	res, err := store.Query(ctx, sql)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) > 0 {
			names = append(names, fmt.Sprintf("%v", row[0]))
		}
	}
	return names, nil
}

// SwitchDatabase reconnects the active SQL connection to a different
// database on the same server (Postgres and MySQL both require a fresh
// connection to change database — there's no in-session USE equivalent
// in the SQLStore interface), reusing the existing tunnel if one is
// active rather than restarting it. Leaves the prior connection active if
// anything fails partway.
func (a *App) SwitchDatabase(name string) error {
	ac := a.getActive()
	if ac == nil || ac.Status != StatusConnected {
		return fmt.Errorf("gui: no active connection to switch database on")
	}
	if _, ok := ac.Client.(db.SQLStore); !ok {
		return fmt.Errorf("gui: active connection does not support switching databases")
	}

	password, err := secretsGetPassword(ac.Conn.Name)
	if err != nil && err != secrets.ErrNotFound {
		return err
	}

	newConn := ac.Conn
	newConn.DBName = name
	dialConn := newConn
	if ac.TunnelProc != nil {
		dialConn.Host = "localhost"
		dialConn.Port = ac.TunnelProc.LocalPort
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialConn.QueryTimeout())
	defer cancel()
	client, err := db.NewClient(ctx, dialConn, password)
	if err != nil {
		return err
	}

	old := ac.Client
	a.setActive(&ActiveConnection{
		Conn:        newConn,
		Status:      StatusConnected,
		TunnelProc:  ac.TunnelProc,
		Client:      client,
		ConnectedAt: time.Now(),
	})
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// RunQuery runs sql against the active SQL connection, recording a history
// entry whether it succeeds or fails, and remembers the result as the
// export target for ExportResultCSV.
func (a *App) RunQuery(sql string) (*db.QueryResult, error) {
	store, ac, err := a.requireSQL()
	if err != nil {
		a.recordHistory(&history.HistoryEntry{Timestamp: time.Now(), Query: sql, Error: err.Error()})
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), ac.Conn.QueryTimeout())
	defer cancel()

	entry := &history.HistoryEntry{Timestamp: time.Now(), ConnName: ac.Conn.Name, Query: sql}
	res, qerr := store.Query(ctx, sql)
	if qerr != nil {
		entry.Error = qerr.Error()
		a.recordHistory(entry)
		return nil, qerr
	}
	entry.DurationMs = res.Duration.Milliseconds()
	entry.RowCount = len(res.Rows)
	a.recordHistory(entry)

	a.mu.Lock()
	a.lastResult = res
	a.lastDocs = nil
	a.mu.Unlock()
	return res, nil
}

// --- KV mode (Redis) ---

// destructiveCommands is the case-insensitive whole-first-token gate a
// Redis command line must pass before RunKVCommand executes it without
// confirmation — moved from the old TUI's internal/views/query.go verbatim.
var destructiveCommands = map[string]bool{
	"FLUSHDB":  true,
	"FLUSHALL": true,
	"DEL":      true,
	"UNLINK":   true,
}

// IsDestructiveRedisCommand reports whether commandLine's leading token is
// FLUSHDB/FLUSHALL/DEL/UNLINK (case-insensitive whole-token match, never a
// substring check — "DELAY_SOMETHING" must not false-positive on "DEL").
func IsDestructiveRedisCommand(commandLine string) bool {
	fields := strings.Fields(commandLine)
	if len(fields) == 0 {
		return false
	}
	return destructiveCommands[strings.ToUpper(fields[0])]
}

func (a *App) ScanKeys(dbIndex int, pattern string) ([]string, error) {
	store, _, err := a.requireKV()
	if err != nil {
		return nil, err
	}
	if pattern == "" {
		pattern = "*"
	}
	return db.ScanAllKeys(context.Background(), store, dbIndex, pattern, 500)
}

func (a *App) KeyType(dbIndex int, key string) (string, error) {
	store, _, err := a.requireKV()
	if err != nil {
		return "", err
	}
	return store.KeyType(context.Background(), dbIndex, key)
}

// TTLSeconds returns the key's TTL in seconds, or -1 if it has no expiry.
func (a *App) TTLSeconds(dbIndex int, key string) (int64, error) {
	store, _, err := a.requireKV()
	if err != nil {
		return 0, err
	}
	d, err := store.TTL(context.Background(), dbIndex, key)
	if err != nil {
		return 0, err
	}
	return int64(d.Seconds()), nil
}

func (a *App) GetValue(dbIndex int, key string) (*db.KVValue, error) {
	store, _, err := a.requireKV()
	if err != nil {
		return nil, err
	}
	return store.GetValue(context.Background(), dbIndex, key)
}

func (a *App) ListRedisDatabases() ([]int, error) {
	store, _, err := a.requireKV()
	if err != nil {
		return nil, err
	}
	return store.ListDatabases(context.Background())
}

// KVCommandResult is RunKVCommand's return shape: either the raw redis
// reply, or NeedsConfirm == true when the command is destructive and
// confirmed == false — the frontend shows a confirm dialog and retries
// with confirmed == true instead of receiving an opaque error.
type KVCommandResult struct {
	Result       any  `json:"result,omitempty"`
	NeedsConfirm bool `json:"needsConfirm"`
}

func (a *App) RunKVCommand(dbIndex int, commandLine string, confirmed bool) (*KVCommandResult, error) {
	if IsDestructiveRedisCommand(commandLine) && !confirmed {
		return &KVCommandResult{NeedsConfirm: true}, nil
	}

	store, ac, err := a.requireKV()
	if err != nil {
		a.recordHistory(&history.HistoryEntry{Timestamp: time.Now(), Query: commandLine, Error: err.Error()})
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), ac.Conn.QueryTimeout())
	defer cancel()

	entry := &history.HistoryEntry{Timestamp: time.Now(), ConnName: ac.Conn.Name, Query: commandLine}
	args := strings.Fields(commandLine)
	result, rerr := store.RunCommand(ctx, dbIndex, args)
	if rerr != nil {
		entry.Error = rerr.Error()
		a.recordHistory(entry)
		return nil, rerr
	}
	entry.RowCount = 1
	a.recordHistory(entry)
	return &KVCommandResult{Result: result}, nil
}

// --- Document mode (MongoDB) ---

func (a *App) ListMongoDatabases() ([]string, error) {
	store, _, err := a.requireDoc()
	if err != nil {
		return nil, err
	}
	return store.ListDatabases(context.Background())
}

func (a *App) ListCollections(database string) ([]string, error) {
	store, _, err := a.requireDoc()
	if err != nil {
		return nil, err
	}
	return store.ListCollections(context.Background(), database)
}

func (a *App) IndexInfo(database, collection string) ([]db.IndexInfo, error) {
	store, _, err := a.requireDoc()
	if err != nil {
		return nil, err
	}
	return store.IndexInfo(context.Background(), database, collection)
}

// Find runs a filter against database.collection, recording history and
// remembering the result as the export target for ExportDocResultJSONL.
func (a *App) Find(database, collection, filterJSON string) (*db.DocResult, error) {
	store, ac, err := a.requireDoc()
	if err != nil {
		a.recordHistory(&history.HistoryEntry{Timestamp: time.Now(), Query: filterJSON, Error: err.Error()})
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), ac.Conn.QueryTimeout())
	defer cancel()

	entry := &history.HistoryEntry{Timestamp: time.Now(), ConnName: ac.Conn.Name, Query: filterJSON}
	res, ferr := store.Find(ctx, database, collection, filterJSON, 0)
	if ferr != nil {
		entry.Error = ferr.Error()
		a.recordHistory(entry)
		return nil, ferr
	}
	entry.DurationMs = res.Duration.Milliseconds()
	entry.RowCount = len(res.Documents)
	a.recordHistory(entry)

	a.mu.Lock()
	a.lastDocs = res
	a.lastResult = nil
	a.mu.Unlock()
	return res, nil
}
