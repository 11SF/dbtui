# dbtui — Technical Implementation Spec (v1.1, multi-store)

For handing off directly to a model for implementation — covers architecture, data models, interfaces, file structure, and a detailed build order. Supports Postgres and MySQL in v1, with Redis and MongoDB designed in from the start as later milestones (same interfaces, no rework needed).

---

## 0. Product summary

A Go TUI tool, k9s-style, for connecting to databases through `kubectl port-forward` (accessing a DB that normally requires shelling into an nginx pod first). Run queries/commands, view results, manage connections/schema — all keyboard-driven inside the terminal.

- Target OS: macOS (but the structure must also support Linux going forward — avoid OS-specific syscalls)
- Go version: 1.22+
- Binary name: `dbtui`
- Store types: Postgres, MySQL (v1) → Redis (M6), MongoDB (M7)

---

## 1. Dependencies

```go
require (
    github.com/rivo/tview v0.0.0-latest
    github.com/gdamore/tcell/v2 v2.7.x
    github.com/jackc/pgx/v5 v5.x
    github.com/go-sql-driver/mysql v1.8.x
    github.com/redis/go-redis/v9 v9.x          // added for Redis (M6)
    go.mongodb.org/mongo-driver/v2 v2.x         // added for MongoDB (M7)
    github.com/zalando/go-keyring v0.2.x
    github.com/atotto/clipboard v0.1.x
    gopkg.in/yaml.v3 v3.x
    modernc.org/sqlite v1.x      // local history store, cgo-free driver
)
```

---

## 2. Project structure

```
dbtui/
├── cmd/
│   └── dbtui/
│       └── main.go              # entrypoint, flag parsing, bootstrap App
├── internal/
│   ├── app/
│   │   ├── app.go               # App struct: owns tview.Application, Pages, global state
│   │   ├── keybinds.go          # global key capture (: / esc / ctrl+c routing)
│   │   └── statusbar.go         # top bar (connection+tunnel status) + bottom hotkey bar
│   ├── config/
│   │   ├── config.go            # load/save ~/.config/dbtui/connections.yaml
│   │   └── types.go             # Connection, TunnelConfig, DBType, StoreCategory
│   ├── db/
│   │   ├── driver.go            # Client base interface + SQLStore/KVStore/DocumentStore capability interfaces
│   │   ├── factory.go           # NewClient dispatcher
│   │   ├── errors.go
│   │   ├── postgres/
│   │   │   └── postgres.go      # implements SQLStore
│   │   ├── mysql/
│   │   │   └── mysql.go         # implements SQLStore
│   │   ├── redis/
│   │   │   └── redis.go         # implements KVStore   (M6)
│   │   └── mongo/
│   │       └── mongo.go         # implements DocumentStore  (M7)
│   ├── tunnel/
│   │   └── tunnel.go            # kubectl port-forward subprocess manager
│   ├── secrets/
│   │   └── keyring.go           # wrapper over go-keyring (Get/Set/Delete password)
│   ├── history/
│   │   └── store.go             # sqlite-backed query/command history CRUD
│   ├── views/
│   │   ├── connlist.go          # :conn view
│   │   ├── connform.go          # modal: new/edit connection
│   │   ├── browser.go           # :tables view — branches by capability (SQL tree / KV key list / Mongo tree)
│   │   ├── describe.go          # modal: describe table / index info
│   │   ├── query.go             # :query view — branches by input mode (SQL / Redis command / Mongo filter)
│   │   ├── result.go            # result rendering component (row/column grid + document view)
│   │   ├── history.go           # :history view
│   │   └── logs.go              # :logs view
│   └── logging/
│       └── logger.go            # in-memory ring buffer logger, feeds :logs view
├── go.mod
├── go.sum
└── README.md
```

---

## 3. Data models

### 3.1 Config (`internal/config/types.go`)

```go
type DBType string

const (
    Postgres DBType = "postgres"
    MySQL    DBType = "mysql"
    Redis    DBType = "redis"
    MongoDB  DBType = "mongodb"
)

type StoreCategory string

const (
    CategorySQL      StoreCategory = "sql"
    CategoryKV       StoreCategory = "kv"
    CategoryDocument StoreCategory = "document"
)

func (t DBType) Category() StoreCategory {
    switch t {
    case Postgres, MySQL:
        return CategorySQL
    case Redis:
        return CategoryKV
    case MongoDB:
        return CategoryDocument
    }
    return ""
}

type Connection struct {
    Name     string     `yaml:"name"`
    Type     DBType     `yaml:"type"`
    Host     string     `yaml:"host"`      // "localhost" when using a tunnel
    Port     int        `yaml:"port"`      // remote db port (5432/3306/6379/27017)
    User     string     `yaml:"user,omitempty"`     // unused for Redis
    DBName   string     `yaml:"dbname,omitempty"`   // SQL: database name; Redis: DB index as string e.g. "0"; Mongo: optional default database
    AuthSource string   `yaml:"auth_source,omitempty"` // Mongo only, defaults to "admin"
    // password is NOT stored here — kept in the OS keyring under key = "dbtui:" + Name
    Tunnel   *TunnelConfig `yaml:"tunnel,omitempty"` // nil = direct connect, no k8s
    Group    string     `yaml:"group,omitempty"`     // for grouping dev/staging/prod
}

type TunnelConfig struct {
    KubeContext string `yaml:"kube_context"`
    Namespace   string `yaml:"namespace"`
    TargetType  string `yaml:"target_type"` // "pod" | "svc"
    TargetName  string `yaml:"target_name"`
    RemotePort  int    `yaml:"remote_port"`
    LocalPort   int    `yaml:"local_port,omitempty"` // 0 = auto-assign a free port
}

type ConfigFile struct {
    Connections []Connection `yaml:"connections"`
}
```

File location: `~/.config/dbtui/connections.yaml` (use `os.UserConfigDir()`, do not hardcode the path). Tunnel config is identical across all store types — tunneling is orthogonal to store category.

### 3.2 Runtime connection state (`internal/app/app.go`)

```go
type ConnStatus int

const (
    StatusDisconnected ConnStatus = iota
    StatusTunnelConnecting
    StatusTunnelUp
    StatusDBConnecting
    StatusConnected
    StatusError
)

type ActiveConnection struct {
    Conn        config.Connection
    Status      ConnStatus
    TunnelProc  *tunnel.Tunnel   // nil if not using a tunnel
    Client      db.Client        // nil until StatusConnected; type-assert to SQLStore/KVStore/DocumentStore as needed
    LastError   error
    ConnectedAt time.Time
}
```

The app supports **one active connection at a time** in v1 (mirroring k9s's single-context focus). Supporting multiple simultaneous connections is a v2 concern.

### 3.3 Query/command history record (`internal/history/store.go`)

```go
type HistoryEntry struct {
    ID         int64
    Timestamp  time.Time
    ConnName   string
    Query      string   // SQL text, Redis command line, or Mongo filter JSON — stored as-is per category
    DurationMs int64
    RowCount   int
    Error      string // empty if successful
}
```

Same table/schema regardless of store category — one history mechanism for all.

```sql
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    conn_name TEXT NOT NULL,
    query TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    row_count INTEGER NOT NULL,
    error TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_history_timestamp ON history(timestamp DESC);
```
File: `~/.local/share/dbtui/history.db` (on Linux/macOS via `os.UserCacheDir()`, or the same config dir for simplicity)

---

## 4. Core interfaces

### 4.1 `internal/db/driver.go`

```go
// Client is the minimum every store type must implement.
type Client interface {
    Kind() config.DBType
    Ping(ctx context.Context) error
    Close() error
}
```

Capability interfaces — a driver implements whichever apply. Views use a type assertion (`client.(SQLStore)`) to decide what to render, never a switch on `DBType` directly (keeps view code driver-agnostic).

```go
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
    Rows     [][]any       // each cell converted to string before rendering (formatValue helper)
    Duration time.Duration
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

// --- Key-Value (Redis) — M6 ---
type KVStore interface {
    Client
    ListDatabases(ctx context.Context) ([]int, error)          // Redis DB indexes 0-15
    ScanKeys(ctx context.Context, db int, pattern string, cursor uint64, count int) (keys []string, nextCursor uint64, err error)
    KeyType(ctx context.Context, db int, key string) (string, error) // string/hash/list/set/zset/stream
    GetValue(ctx context.Context, db int, key string) (*KVValue, error) // dispatches by type internally
    TTL(ctx context.Context, db int, key string) (time.Duration, error) // -1 = no expiry
    RunCommand(ctx context.Context, db int, args []string) (any, error) // raw redis-cli style command
}

type KVValue struct {
    Type   string   // "string" | "hash" | "list" | "set" | "zset" | "stream"
    String string   // populated when Type == "string"
    Hash   map[string]string
    List   []string
    Set    []string
    ZSet   []ZMember
}
type ZMember struct { Member string; Score float64 }

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
```

Factory:

```go
// internal/db/factory.go
func NewClient(ctx context.Context, conn config.Connection, password string) (Client, error) {
    switch conn.Type {
    case config.Postgres:
        return postgres.NewClient(ctx, conn, password)
    case config.MySQL:
        return mysql.NewClient(ctx, conn, password)
    case config.Redis:
        return redis.NewClient(ctx, conn, password)
    case config.MongoDB:
        return mongo.NewClient(ctx, conn, password)
    }
    return nil, fmt.Errorf("unsupported db type: %s", conn.Type)
}
```

**Query timeout**: every `Query()`/`Find()`/`RunCommand()` call must be bound to a `context.WithTimeout` (default 30s, configurable per connection — add a `QueryTimeoutSec int` field on `Connection` if needed)

**Result caps** (per category, to keep the terminal from hanging on huge results):
- SQL: auto-append `LIMIT 1000` if the query is a bare `SELECT` with no existing `LIMIT` (simple regex heuristic, no full SQL parsing needed); surface "capped at 1000 rows" in the result footer
- KV: `ScanKeys` caps `count` at 500 per page, paginated via cursor (load next page on scroll-to-bottom, never load the whole keyspace at once)
- Document: `Find` defaults `limit` to 100 if unset

### 4.2 `internal/tunnel/tunnel.go`

```go
type Tunnel struct {
    cmd       *exec.Cmd
    LocalPort int
    stderr    *bytes.Buffer // wired to the logging ring buffer via io.MultiWriter
}

func Start(ctx context.Context, cfg config.TunnelConfig) (*Tunnel, error)
// 1. if cfg.LocalPort == 0: find a free port via net.Listen("tcp", ":0"), close it immediately, use that port
// 2. target := cfg.TargetType + "/" + cfg.TargetName  (e.g. "svc/postgres" or "pod/nginx-xxx")
// 3. args := []string{"port-forward", target, fmt.Sprintf("%d:%d", localPort, cfg.RemotePort), "-n", cfg.Namespace}
//    if cfg.KubeContext != "": append "--context", cfg.KubeContext
// 4. cmd := exec.CommandContext(ctx, "kubectl", args...)
// 5. redirect cmd.Stderr to the ring buffer logger
// 6. cmd.Start()
// 7. poll: loop net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", localPort), 500ms) every 300ms until success or a 10s timeout
// 8. on timeout: kill the process, return an error with the captured stderr content

func (t *Tunnel) Stop() error
// cmd.Process.Signal(syscall.SIGTERM), fall back to Kill() if it hasn't died within 3s

func (t *Tunnel) IsAlive() bool
// cmd.ProcessState == nil && the process hasn't exited — used to check before auto-reconnect
```

**Auto-reconnect**: a separate goroutine polls `IsAlive()` every 2s while a connection is active; if it dies and the prior status was `StatusConnected`, retry `Start()` up to 3 times (exponential backoff 1s/2s/4s) before setting `StatusError`. Tunnel logic is identical for every store type — no branching needed here.

### 4.3 `internal/secrets/keyring.go`

```go
const service = "dbtui"

func SetPassword(connName, password string) error  // keyring.Set(service, connName, password)
func GetPassword(connName string) (string, error)   // keyring.Get(service, connName)
func DeletePassword(connName string) error
```

---

## 5. View architecture (tview)

`App.Pages` is the root `*tview.Pages` — each view is a page named after its command (`"conn"`, `"tables"`, `"query"`, `"history"`, `"logs"`)

### 5.1 Global layout (shared by every view)

```
┌─ statusbar (top, height=1) ───────────────────┐
│ conn: prod-db [postgres] ● connected | tunnel: localhost:15432 │
├─ main content (Pages, flex=1) ─────────────────┤
│                                                  │
│              [current view content]             │
│                                                  │
├─ command line (bottom, height=1, hidden by default) ┤
├─ hotkey bar (bottom, height=1) ────────────────┤
│ <:> cmd  </> filter  <esc> back  <q> quit       │
└──────────────────────────────────────────────────┘
```

The root layout is a vertical `tview.Flex` with the 4 sections above, built once in `app.go` — `Pages` is the part that swaps content. The status bar always includes the store category tag (`[postgres]`, `[redis]`, `[mongodb]`) so it's clear which mode you're in without opening the browser.

### 5.2 Global key capture (`app.go` → `SetInputCapture`)

```go
switch event.Rune() {
case ':':
    showCommandLine()  // focus the bottom InputField; Enter → parseCommand() → SwitchToPage()
    return nil
case '/':
    if currentView.SupportsFilter() { showFilterLine() }
    return nil
}
switch event.Key() {
case tcell.KeyEsc:
    goBack() // pop the view history stack
case tcell.KeyCtrlC:
    confirmQuit()
}
```

Command mapping: `:conn`, `:tables`, `:query`, `:history`, `:logs`, `:q` (quit) — same commands regardless of store category; `:tables` opens the browser in whichever mode fits the active connection.

View history stack: `[]string` tracking pages visited in order; `Esc` pops it and calls `SwitchToPage(prev)`

### 5.3 `views/connlist.go`

- Single-column `tview.Table` showing: `● name (type) host:port [group]`
- Status colors: green = connected, gray = disconnected, red = error (via tview color tags `[green]●[white]`)
- Keybinds: `Enter` connect, `d` disconnect, `n` new (pushes the connform modal), `e` edit, `x` delete (confirm modal first), `/` filter by name

### 5.4 `views/connform.go`

- Modal (`tview.Form`) layered over connlist via `Pages.AddPage(..., true, true)`, sized smaller than fullscreen
- `Type` dropdown: `postgres`, `mysql`, `redis`, `mongodb`
- Field visibility per type:
  - Postgres/MySQL: Host, Port, User, Password (masked input, never saved to the struct — call `secrets.SetPassword` separately on submit), DBName
  - Redis: Host, Port, Password (User hidden — optional ACL username, add as an "advanced" field later if needed), DB index defaults to `0`
  - MongoDB: Host, Port, User, Password, DBName (optional — Mongo can browse all databases without one), plus an optional `AuthSource` advanced field (defaults to `admin`)
- "Use kubectl tunnel" checkbox → toggles extra fields: KubeContext, Namespace, TargetType (dropdown pod/svc), TargetName, RemotePort, LocalPort (optional). Identical for all four types.
- Submit → validate required fields for the selected type → `config.Save()` → `secrets.SetPassword()` → close modal → refresh connlist

### 5.5 `views/browser.go` (`:tables` command)

One view, three render modes selected by type-asserting the active `db.Client`:

```go
func NewBrowserView(app *App) *BrowserView {
    switch client := app.Active.Client.(type) {
    case db.SQLStore:
        return newSQLBrowser(client)       // TreeView: schema → table
    case db.KVStore:
        return newKVBrowser(client)        // flat filtered key list + type/ttl columns
    case db.DocumentStore:
        return newDocumentBrowser(client)  // TreeView: database → collection
    }
}
```

**SQL browser (Postgres/MySQL):**
- Layout: horizontal `Flex` — left `tview.TreeView` (schema → table), right a placeholder or column preview
- Load schema list on entering the view (`ListSchemas` → `ListTables` lazily per expanded schema node)
- `Enter` on a table node → auto-generate `SELECT * FROM schema.table LIMIT 100` → push the `views/result.go` component with the result
- `d` on a table node → open the `describe.go` modal showing `DescribeTable()` results (column/type/nullable/default, PK/FK/index sections)

**KV browser (Redis):**
- Top: DB index selector (`0-15`, switch via `n`/`p` or number keys)
- Main: `tview.Table` of keys — columns `type | key | ttl`, loaded via `ScanKeys` cursor pagination (next page on scroll-to-bottom, not all at once)
- `/` filters by key pattern (re-triggers `ScanKeys` with the pattern server-side, not client-side filtering — Redis key spaces can be huge)
- `Enter` on a key → `GetValue()` → render in a modal based on `KVValue.Type` (string: plain text; hash: key/value table; list/set: line list; zset: member+score table)

**Document browser (MongoDB):**
- Same `TreeView` shape as the SQL browser: database → collection
- `Enter` on a collection → small inline input (default `{}`) for a JSON filter → `Find()` → push result view
- `d` on a collection → `IndexInfo()` in a modal (reuses the describe-modal shell)

### 5.6 `views/query.go` (`:query` command)

Branches by **input mode** rather than being three separate views:

```go
type QueryView struct {
    mode   config.StoreCategory
    input  tview.Primitive   // TextArea for SQL, single-line InputField for Redis, TextArea (JSON) for Mongo
    result *ResultView
}
```

- **SQL mode**: multi-line `TextArea` (top) + result table (bottom, ~40/60 split). `Ctrl+R`/`Ctrl+Enter` runs `Query(ctx, sql)` in a goroutine, renders via `tview.QueueUpdateDraw`, records a `HistoryEntry`. Auto-saves text to `~/.config/dbtui/scratch.sql` on a 500ms debounce, to survive a crash.
- **KV mode (Redis)**: single-line `InputField` styled like `redis-cli` (`127.0.0.1:6379[db2]> `), submits on `Enter`, calls `RunCommand(ctx, db, strings.Fields(input))`. Same history mechanism, one `HistoryEntry` per command. No auto-LIMIT logic needed — Redis commands are already bounded, or the user runs `SCAN`/`KEYS` which routes through the browser instead. Destructive commands matching `FLUSHDB|FLUSHALL|DEL|UNLINK` (case-insensitive prefix) require a confirm modal before executing; everything else runs immediately.
- **Document mode (MongoDB)**: multi-line `TextArea` containing a JSON filter (not full SQL) + a top field for target `database.collection` (pre-filled if entered via the browser's `Enter`). `Ctrl+R` runs `Find()`.

### 5.7 `views/result.go` (shared component, not a standalone page)

```go
type ResultView struct {
    *tview.Table
    lastResult *db.QueryResult
}

func NewResultView() *ResultView
func (r *ResultView) SetResult(res *db.QueryResult)
// clear the previous table, set the header row (bold), fill rows, SetFixed(1, 0) to keep the header from scrolling away

func (r *ResultView) SetDocResult(res *db.DocResult)
// renders as a scrollable list of pretty-printed JSON blocks, one per document, divider line between them
// (document data doesn't fit a flat row/column grid once nesting is involved)

func (r *ResultView) Keybinds()
// y = yank cell/current doc, Y = yank row (tab-separated) — SQL/KV only
// e = export: CSV for SQL result, .jsonl for document result
```

Footer text: `%d rows • %dms%s`, appending `" (capped at 1000)"` if the SQL auto-limit kicked in, or the equivalent cap note for KV/document results.

### 5.8 `views/history.go`

- `tview.Table`: timestamp | conn | duration | rows | query/command preview (truncated to 60 chars)
- `/` filter (client-side on the loaded list; reload from sqlite each time the view is entered)
- `Enter` → copy the text into `query.go`'s input (TextArea or InputField, whichever matches the active connection's mode) + switch to the `:query` page

### 5.9 `views/logs.go`

- Read-only `tview.TextView` bound to the ring buffer logger (`logging.Logger`, keeps the last 500 lines, thread-safe)
- Auto-scroll to bottom on new log lines (unless the user has scrolled up manually — check `GetScrollOffset`)

---

## 6. Startup flow (`main.go`)

```go
func main() {
    cfg, err := config.Load()               // creates an empty file if none exists
    logger := logging.NewRingBuffer(500)
    app := app.NewApp(cfg, logger)           // builds tview.Application + layout + Pages
    app.RegisterPage("conn", views.NewConnList(app))
    app.RegisterPage("tables", views.NewBrowserView(app))   // built lazily once a connection is active
    app.RegisterPage("query", views.NewQueryView(app))       // built lazily once a connection is active
    app.RegisterPage("history", views.NewHistoryView(app))
    app.RegisterPage("logs", views.NewLogsView(app))
    app.SwitchToPage("conn")                 // always start on the connection list
    if err := app.Run(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

---

## 7. Error handling conventions

- Any error from DB/tunnel/config **must never panic** — surface it in the status bar (red, auto-clears after 5s) plus the full detail in `:logs`
- Distinguish network/timeout errors from query/syntax errors (wrap with custom error types `ErrTunnelTimeout`, `ErrQueryTimeout`, `ErrDBConn` in `internal/db/errors.go`) so the status bar can show a clear, specific message — applies the same way across SQL errors, Redis command errors, and Mongo filter errors
- Passwords must never be logged or appear in any printed error message (mask as `****` before formatting a connection string anywhere in logs)

---

## 8. Testing scope (v1)

- Unit tests: `config` (load/save round-trip), `db` query-result formatting, `tunnel` free-port allocation logic (mock `exec.Cmd` by extracting a `runner` interface to inject a fake)
- No integration tests against real kubectl/DB in CI (requires a live cluster) — use a manual test checklist instead during M1-M2, and again per store type when M6/M7 land

---

## 9. Implementation order

1. `config` package (types + load/save YAML, including `DBType`/`StoreCategory` from the start) + unit tests
2. `secrets` package (keyring wrapper)
3. `db` package: `Client` base interface + `SQLStore` capability interface + postgres implementation (mysql can follow in a later milestone) — write the interface split now even though only one implementation exists yet
4. `app` package: layout shell (statusbar with category tag, pages, hotkey bar, global key capture, command-line parser) — no real views yet, use a placeholder page to test navigation
5. `views/connlist.go` + `views/connform.go` — full connection CRUD working (still no tunnel, manual connect only)
6. Manual DB connect (no tunnel) → `views/query.go` SQL mode + `views/result.go` — run a query end-to-end
7. `tunnel` package + wire it into the connlist connect flow — **M2 complete here**
8. `views/browser.go` SQL mode + `describe.go` — **M3**
9. `history` package + `views/history.go` + CSV export + clipboard yank — **M4**
10. `views/logs.go`, tunnel auto-reconnect, keybinding polish, mysql driver — **M5**
11. `internal/db/redis/` implementing `KVStore` + `views/browser.go` KV branch + `views/query.go` KV mode + destructive-command confirm modal — **M6**
12. `internal/db/mongo/` implementing `DocumentStore` + browser/query Document branches + `ResultView.SetDocResult` + `.jsonl` export — **M7**

Rationale for Redis before Mongo: Redis' driver surface is smaller (no query language, no nested documents) — a good forcing function to validate the capability-interface split before tackling Mongo's more complex `Find`/JSON rendering.

---

## 10. Open decisions (defaults given below — use these if no answer is provided)

- Keybinding style: **default = arrow keys + enter/esc** (no vim-style `hjkl` in v1, to keep scope down)
- Multi kube-context: **default = store `kube_context` per connection** (no global context switcher like k9s in v1) — sufficient since each connection is typically already tied to its own context/cluster
- DB type for v1: **start with Postgres** — get every interface fully working through postgres first, then add mysql, then Redis, then MongoDB, without changing the core interfaces
- Redis destructive commands: **default = require a confirm modal for anything matching `FLUSHDB|FLUSHALL|DEL|UNLINK`** (case-insensitive prefix match); everything else runs immediately. Revisit if this proves annoying in practice.
