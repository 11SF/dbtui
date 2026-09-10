# dbtui — Test Case Specification (v1.0)

**Instruction to the implementing model (include this verbatim in your prompt to it):**

> Implement `dbtui` according to `dbtui-technical-spec-merged-en.md`. Every test case in this document (`dbtui-test-cases-en.md`) must be implemented as an actual automated test (Go `testing` package, table-driven where noted) and must **pass 100%** before you report the work as done. Do not skip, comment out, or weaken a test case to make it pass — if a test case cannot pass as written, stop and report which one and why, instead of silently modifying it. Manual/integration checklist items (marked "Manual") cannot be automated in CI; for those, write the steps as a runnable checklist in `TESTING.md` and self-verify against a real Postgres + a real (or kind/minikube) k8s cluster if available, otherwise clearly report them as unverified rather than claiming a pass. Run `go test ./... -v` and `go vet ./...` and paste the full output as part of your final report. A submission with any failing, skipped, or panicking test is incomplete.

---

## 0. Test levels

| Level | Scope | Tooling |
|---|---|---|
| Unit | Single function/type, no external process/network | `go test` |
| Integration (local) | Real SQLite file, real free-port allocation, mocked `exec.Cmd` | `go test` with test helpers |
| Manual | Requires real Postgres/MySQL/Redis/Mongo and/or real `kubectl`+cluster | Checklist in `TESTING.md`, run by hand |

Unit + Integration(local) tests must all be automatable and must pass 100% in CI with no external dependencies running. Manual tests are checklist-only — see §9.

---

## 1. `internal/config` package

### 1.1 `TestLoad_CreatesEmptyFileIfMissing`
- Given: `~/.config/dbtui/connections.yaml` does not exist (use a temp `HOME`/config dir per test via `t.Setenv`)
- When: `config.Load()` is called
- Then: no error; returned `ConfigFile.Connections` is an empty slice (not nil); the file now exists on disk

### 1.2 `TestSaveLoad_RoundTrip`
- Given: a `ConfigFile` with 3 connections covering all four `DBType` values in `Type`, one with `Tunnel` set and one with `Tunnel` nil
- When: `Save()` then `Load()`
- Then: the loaded `ConfigFile` deep-equals the original (use `reflect.DeepEqual` or `go-cmp`); confirm `Tunnel == nil` is preserved for the entry that had no tunnel, and all fields of the entry that had one round-trip exactly

### 1.3 `TestSave_NeverWritesPassword`
- Given: a `Connection` struct (password is never a field on it per the spec — this test exists to guard against a future regression)
- When: `Save()` is called and the resulting YAML file is read back as raw bytes
- Then: assert the raw file content contains none of a list of forbidden substrings (`password:`, `pass:`, `pwd:`) — this is a static guard, not a behavioral test, but it must exist

### 1.4 `TestDBType_Category`
Table-driven, one case per `DBType`:
| Input | Expected `Category()` |
|---|---|
| `Postgres` | `CategorySQL` |
| `MySQL` | `CategorySQL` |
| `Redis` | `CategoryKV` |
| `MongoDB` | `CategoryDocument` |
| `DBType("bogus")` | `""` (empty, not a panic) |

### 1.5 `TestLoad_MalformedYAML_ReturnsError`
- Given: a config file containing invalid YAML (e.g. unbalanced braces)
- When: `Load()`
- Then: returns a non-nil error, does not panic, and does not overwrite/truncate the malformed file

### 1.6 `TestLoad_UnknownFields_Ignored`
- Given: a YAML file with an extra unknown top-level key alongside `connections`
- When: `Load()`
- Then: no error; `Connections` parses correctly (forward-compat guard)

---

## 2. `internal/secrets` package

### 2.1 `TestSetGetDeletePassword_RoundTrip`
- Given: a fake/mock keyring backend injected via an interface (do not hit the real OS keychain in CI — see note below)
- When: `SetPassword("test-conn", "s3cr3t")` then `GetPassword("test-conn")`
- Then: returns `"s3cr3t"`, no error
- When: `DeletePassword("test-conn")` then `GetPassword("test-conn")`
- Then: returns an error indicating "not found" (define and assert a specific sentinel error, e.g. `secrets.ErrNotFound`, rather than asserting on a raw string)

**Implementation note required from the model:** `go-keyring` talks to the real OS keychain with no built-in mock. The `secrets` package must define its own small interface (e.g. `type backend interface { Set, Get, Delete }`) with the real `go-keyring` calls behind the default implementation, and an in-memory fake implementing the same interface for tests. This is a design requirement, not optional — without it, 2.1 cannot run in CI.

### 2.2 `TestGetPassword_NonexistentConnection_ReturnsNotFound`
- Given: a connection name never set
- When: `GetPassword("never-set")`
- Then: returns `secrets.ErrNotFound` (or equivalent sentinel), not a generic error

### 2.3 `TestSetPassword_EmptyConnName_ReturnsError`
- When: `SetPassword("", "x")`
- Then: returns a validation error, does not silently store under an empty key

---

## 3. `internal/db` package — interfaces and SQL implementation

### 3.1 `TestFactory_NewClient_UnsupportedType_ReturnsError`
- Given: `Connection{Type: DBType("bogus")}`
- When: `db.NewClient(ctx, conn, "pw")`
- Then: returns a non-nil error containing the offending type name; does not panic

### 3.2 Result formatting — `TestFormatValue` (table-driven)
Covers the helper that turns a raw driver value into the `any` stored in `QueryResult.Rows`:
| Input Go value | Expected rendered form |
|---|---|
| `nil` | `nil` / renders as `NULL` in the result view (assert whatever sentinel the code uses, e.g. a typed `NullValue{}`) |
| `int64(42)` | `"42"` |
| `float64(3.14)` | `"3.14"` |
| `[]byte("hello")` | `"hello"` |
| `time.Time{...}` | RFC3339-formatted string |
| `bool(true)` | `"true"` |

### 3.3 `TestSQLStore_AutoLimitHeuristic` (table-driven, pure function — extract the regex/heuristic into a standalone function e.g. `db.ApplyAutoLimit(sql string) (string, bool)` so it's unit-testable without a real DB)
| Input SQL | Expected output SQL | Expected `capped` bool |
|---|---|---|
| `SELECT * FROM users` | `SELECT * FROM users LIMIT 1000` | `true` |
| `select id from t` (lowercase) | `select id from t LIMIT 1000` | `true` |
| `SELECT * FROM users LIMIT 50` | unchanged | `false` |
| `SELECT * FROM users LIMIT 50 OFFSET 10` | unchanged | `false` |
| `UPDATE users SET x=1` | unchanged | `false` |
| `INSERT INTO t VALUES (1)` | unchanged | `false` |
| `  SELECT * FROM users  ` (leading/trailing whitespace) | trimmed + `LIMIT 1000` appended correctly, no double space | `true` |
| `SELECT * FROM users; SELECT * FROM orders` (multi-statement) | documented behavior — either: only wraps the first statement, or leaves untouched and relies on query timeout as the safety net. **Model must pick one, document it in a comment, and this test asserts that documented behavior** — do not leave this case unhandled/undefined |

### 3.4 `TestPostgres_Query_Success` (Manual/Integration — requires real Postgres, see §9)
### 3.5 `TestPostgres_Query_Timeout` (Manual/Integration)
### 3.6 `TestPostgres_ListSchemas_ListTables_DescribeTable` (Manual/Integration)

These three require a live Postgres instance. In CI, either:
(a) spin up Postgres via `testcontainers-go` if network egress in the build environment allows pulling the image, or
(b) if not available in this sandboxed environment, mark these `t.Skip("requires live postgres — see TESTING.md")` and list them explicitly in the Manual checklist (§9). The model must state clearly which of (a)/(b) it used and why.

### 3.7 `TestKVStore_ScanKeys_Pagination` (table-driven against a fake `KVStore` used only to validate the browser's pagination-consuming code in §7, not the real Redis driver)
- Given: a fake `KVStore` whose `ScanKeys` returns cursor `0` on the final page
- When: the browser's "load next page" logic calls `ScanKeys` repeatedly
- Then: it stops calling once `nextCursor == 0`; the accumulated key list has no duplicates and preserves order across pages

### 3.8 `TestKVValue_TypeDispatch` (table-driven)
Given a `KVValue` with each `Type` set (`string`, `hash`, `list`, `set`, `zset`), assert the result-rendering function (extracted as a pure function, e.g. `renderKVValue(v *KVValue) string` or similar) picks the correct field and ignores the others, and does not panic when the "wrong" fields are left zero-valued.

### 3.9 `TestDocumentStore_Find_DefaultLimit` (unit test on the pure request-building logic, not a live Mongo call)
- Given: `limit == 0` passed to the internal request builder
- Then: the effective limit applied is `100`
- Given: `limit == 5000`
- Then: the effective limit is left at `5000` (no client-side cap beyond the "default if unset" rule — confirms the code doesn't silently clamp an explicit value)

---

## 4. `internal/tunnel` package

### 4.1 `TestTunnel_FreePortAllocation`
- When: `cfg.LocalPort == 0` and `Start()` is invoked with a fake process runner (see 4.2 note)
- Then: the chosen local port is > 0, and two concurrent calls to the port-picking helper never return the same port (run 20 iterations in a loop, assert no collisions) — this can be tested directly against the `net.Listen("tcp", ":0")`-based helper without spawning `kubectl` at all if that helper is extracted as its own function, e.g. `tunnel.PickFreePort() (int, error)`

**Implementation note required from the model:** `Start()` must not call `exec.Command("kubectl", ...)` directly inline — extract a small `runner` interface (`Start() error`, `Wait() error`, `Kill() error`, exposes `Stderr` as an `io.Writer` target) so tests can inject a fake that never actually spawns `kubectl`. Without this, 4.2–4.5 cannot run in CI.

### 4.2 `TestTunnel_Start_Success`
- Given: a fake runner whose fake "process" immediately makes `localhost:<port>` acceptable (spin up a real `net.Listen` in the test to simulate the forwarded port coming up)
- When: `tunnel.Start(ctx, cfg)`
- Then: returns a `*Tunnel` with no error, `LocalPort` matches the listener's port

### 4.3 `TestTunnel_Start_TimeoutIfPortNeverOpens`
- Given: a fake runner that starts successfully but never opens the local port
- When: `tunnel.Start(ctx, cfg)` with a short test-only timeout override (inject the 10s timeout as a parameter/field, not a hardcoded constant, so tests don't take 10 real seconds)
- Then: returns an error mentioning timeout, and the fake runner's `Kill()` was called exactly once

### 4.4 `TestTunnel_Stop_GracefulThenForceKill`
- Given: a fake runner whose `Signal(SIGTERM)` doesn't cause it to exit
- When: `tunnel.Stop()`
- Then: after the grace period (override to a short test value), `Kill()` is called; `Stop()` returns without hanging the test

### 4.5 `TestTunnel_IsAlive`
- Given: a fake runner reporting "still running"
- Then: `IsAlive()` returns `true`
- Given: the fake runner reporting "exited"
- Then: `IsAlive()` returns `false`

### 4.6 `TestTunnel_AutoReconnect_RetriesWithBackoff`
- Given: a fake runner where `IsAlive()` flips to `false` after connection, and the injected `Start` function fails twice then succeeds on the 3rd attempt
- When: the auto-reconnect goroutine runs (drive it via a test clock/ticker abstraction, not real `time.Sleep`, so the test is fast and deterministic)
- Then: exactly 3 start attempts are made, with the documented backoff intervals passed to the clock abstraction (1s/2s/4s), and status ends at `StatusConnected`

### 4.7 `TestTunnel_AutoReconnect_GivesUpAfterMaxRetries`
- Given: injected `Start` always fails
- Then: after 3 attempts, status is set to `StatusError` and no further attempts are made

---

## 5. `internal/history` package

### 5.1 `TestHistoryStore_InsertAndList`
- Given: an in-memory or temp-file SQLite DB
- When: 3 `HistoryEntry` records inserted with distinct timestamps
- Then: `List()` returns them ordered by `timestamp DESC`

### 5.2 `TestHistoryStore_List_RespectsLimit`
- Given: 10 records inserted
- When: `List(limit=5)`
- Then: returns exactly 5, the 5 most recent

### 5.3 `TestHistoryStore_FilterByConnName`
- Given: records across 2 different `ConnName` values
- When: filtering by one conn name
- Then: only matching records returned

### 5.4 `TestHistoryStore_StoresErrorField`
- Given: an entry representing a failed query (`Error` non-empty, `RowCount == 0`)
- Then: round-trips correctly through insert+list

### 5.5 `TestHistoryStore_SchemaCreatedIfMissing`
- Given: a brand-new empty SQLite file
- When: `history.Open(path)`
- Then: the `history` table and index exist afterward (query `sqlite_master`), no error

---

## 6. `internal/logging` package

### 6.1 `TestRingBuffer_CapsAtMaxLines`
- Given: a ring buffer with capacity 500
- When: 600 lines written
- Then: `Lines()` returns exactly 500, and they are the **last** 500 written (i.e., the oldest 100 were dropped, not the newest)

### 6.2 `TestRingBuffer_ConcurrentWrites_NoRace`
- When: 50 goroutines each write 20 lines concurrently
- Then: run with `go test -race`; no data race reported; final line count is capped correctly at the buffer size

### 6.3 `TestRingBuffer_ImplementsIOWriter`
- Confirms the logger can be used directly as `cmd.Stderr` (i.e., satisfies `io.Writer`) and that partial/multi-line writes in one `Write()` call are split into separate log lines correctly

---

## 7. `internal/views` package — logic extracted as pure functions

View code that's wired directly into `tview` primitives is hard to unit test meaningfully; the requirement here is that **all non-trivial branching/formatting logic inside views must be extracted into pure, standalone functions** so it can be tested without a running terminal. Specifically:

### 7.1 `TestBrowserView_CapabilityDispatch`
- Given: a fake `db.Client` that also implements `SQLStore` only
- When: the dispatch function (`selectBrowserMode(client db.Client) StoreCategory` or equivalent, extracted so it doesn't require building the actual `tview` tree) is called
- Then: returns `CategorySQL`
- Repeat for a fake implementing only `KVStore` → `CategoryKV`, and only `DocumentStore` → `CategoryDocument`
- Given: a fake implementing none of the three capability interfaces (only `Client`) → the function returns an explicit "unsupported" error/zero-value rather than panicking on a failed type assertion

### 7.2 `TestQueryView_ModeSelection`
- Same pattern as 7.1 but for the function that decides `QueryView.mode` from the active connection's `DBType.Category()`

### 7.3 `TestResultView_DocResultFormatting`
- Given: a `DocResult` with 3 pretty-JSON document strings
- When: the pure formatting function that turns `DocResult` into the divider-separated display text is called
- Then: output contains all 3 documents in order, separated by the documented divider, with no truncation

### 7.4 `TestDestructiveCommandDetection` (table-driven — Redis confirm-modal gate from spec §5.6/§10)
| Input command | Expect confirm required? |
|---|---|
| `FLUSHDB` | true |
| `flushall` (lowercase) | true |
| `DEL somekey` | true |
| `UNLINK a b c` | true |
| `GET somekey` | false |
| `SET x 1` | false |
| `DELAY_SOMETHING` (should not false-positive on substring "DEL") | false — assert the match is a whole-command-token match, not a naive substring check |

### 7.5 `TestHistoryEntry_PreviewTruncation`
- Given: a query string longer than 60 chars
- When: the preview-truncation helper used by `views/history.go` runs
- Then: result is exactly 60 chars (or 60 + ellipsis, per whatever the model documents) and does not cut a multi-byte UTF-8 rune in half (test with a string containing Thai characters to catch byte-vs-rune bugs, since this tool has a Thai-speaking user)

### 7.6 `TestConnList_StatusColorMapping` (table-driven)
| `ConnStatus` | Expected color tag substring |
|---|---|
| `StatusConnected` | `green` |
| `StatusDisconnected` | `gray` (or documented equivalent) |
| `StatusError` | `red` |
| `StatusTunnelConnecting` / `StatusDBConnecting` | a distinct "in progress" color, not reused from the above three |

### 7.7 `TestConnForm_FieldVisibilityByType` (table-driven, pure function e.g. `visibleFields(t config.DBType) []string`)
| `DBType` | Fields expected present | Fields expected absent |
|---|---|---|
| `postgres` | Host, Port, User, Password, DBName | AuthSource |
| `mysql` | Host, Port, User, Password, DBName | AuthSource |
| `redis` | Host, Port, Password | User, AuthSource |
| `mongodb` | Host, Port, User, Password, DBName, AuthSource | — |

---

## 8. Cross-cutting / regression tests

### 8.1 `TestNoPasswordInLogs`
- Given: a `Connection` with a known password value flowing into any function that formats a connection string or error for logging (e.g. a DB connect failure)
- Then: assert the logged/error string never contains the raw password substring, wherever this formatting happens

### 8.2 `TestPanicSafety_AllPublicEntryPoints`
- For every exported constructor/handler function reachable from a key event (connect, run query, run command, disconnect, export), pass deliberately malformed/nil/zero-value input and assert **no panic** — either via `recover()` wrapping in the test, or by asserting the function returns an error instead of panicking. This is the automated enforcement of the "must never panic" rule in spec §7.

### 8.3 `go vet ./...` must produce no output.
### 8.4 `go test ./... -race` must pass with no data races, given the concurrent tunnel/history/logging access described above.

---

## 9. Manual / integration checklist (`TESTING.md`, not automatable in CI)

To be written by the model as an actual step-by-step checklist file, and self-run wherever the environment allows (state clearly which steps were actually executed vs. which require infra not available in this environment):

1. **Postgres via tunnel**: real k8s cluster (or `kind`) with a Postgres pod + `kubectl port-forward` reachable → connect via dbtui, run `SELECT 1`, confirm result renders, confirm tunnel subprocess is killed on disconnect (`ps aux | grep port-forward` shows nothing after disconnect)
2. **Tunnel auto-reconnect**: manually `kill` the `kubectl port-forward` subprocess while connected → confirm dbtui detects it and reconnects within the documented backoff window, status bar reflects each state transition
3. **MySQL**: same as (1) against a MySQL pod
4. **Redis**: connect, browse keys across DB indexes 0-15, run `SET`/`GET`/`FLUSHDB` via `:query` and confirm the confirm-modal appears for `FLUSHDB`
5. **MongoDB**: connect, browse database→collection tree, run a `Find` with a non-trivial filter (`{"age": {"$gt": 30}}`), confirm document view renders and `.jsonl` export produces valid JSON Lines
6. **Password never on disk in plaintext**: after configuring a connection with a password, `grep -r "<the password>" ~/.config/dbtui/` and confirm zero matches
7. **Crash recovery**: type a long query into `:query`, force-kill the app process, relaunch, confirm the scratch buffer restored the in-progress query
8. **Large result set**: run a query against a table with >1000 rows, confirm the "(capped at 1000)" footer appears and the UI does not freeze/lag
9. **Keychain integration**: delete a connection, confirm the corresponding OS keychain entry is also removed (`security find-generic-password -s dbtui -a <name>` on macOS should fail afterward)

---

## 10. Definition of done (submission gate)

The model's implementation is only considered complete when **all** of the following are true:

- [ ] `go build ./...` succeeds with no errors
- [ ] `go vet ./...` produces no output
- [ ] `go test ./... -v` — every test listed in §1-§8 either **passes** or is an explicitly-documented `t.Skip` for a Manual/Integration case listed in §3.4-3.6, with the reason stated in the skip message
- [ ] `go test ./... -race` passes with no data race reports
- [ ] `TESTING.md` exists and contains the full checklist from §9, with each item marked `[verified]`, `[not verified — requires <infra>]`, or `[failed — <description>]` — no item may be left unmarked
- [ ] No test case from this document was deleted, renamed to something unrecognizable, or had its assertions weakened to force a pass — the model must call out explicitly, in its final report, any case it could not implement as specified and why

If any box above is unchecked, the model must report the work as **incomplete** rather than done, and list exactly what remains.
