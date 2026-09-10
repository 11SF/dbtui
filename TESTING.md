# dbtui — Manual / Integration Testing Checklist

This is the manual checklist from `dbtui-test-cases-en.md` §9 — items that need real infrastructure (a live k8s cluster + `kubectl`, real Postgres/MySQL/Redis/MongoDB servers, the OS keychain, or an interactive TUI session) that automated `go test` cannot exercise in CI. Automated coverage for everything else lives in each package's `_test.go` files (see the final report / `go test ./... -v` output).

Environment available at the time this checklist was last run: macOS sandbox with `docker` and `kubectl` binaries present, but **no live Kubernetes cluster** (no `kind`/`minikube` cluster was created, and no `~/.kube/config` context points at a reachable cluster), and no real OS keychain interaction was attempted (that would leave real secrets in the user's keychain from an automated run, which is not something to do without being asked). Where a step could be exercised without k8s (e.g. talking to a docker-run database directly, bypassing the tunnel), that substitute is noted explicitly — it verifies the store-driver code path, not the tunnel path, and is marked accordingly.

1. **Postgres via tunnel**: real k8s cluster (or `kind`) with a Postgres pod + `kubectl port-forward` reachable → connect via dbtui, run `SELECT 1`, confirm result renders, confirm tunnel subprocess is killed on disconnect (`ps aux | grep port-forward` shows nothing after disconnect)
   **[not verified — requires a live Kubernetes cluster reachable via `kubectl`, which this environment does not have]**
   The full connect → browse → query → disconnect code path is implemented and wired end-to-end (`app.Connect`, `views.NewBrowserPage`, `views.NewQueryPage`, `app.Disconnect`), including starting `internal/tunnel` when `Connection.Tunnel != nil`, but it has only been exercised against a real Postgres via `internal/db/postgres`'s testcontainers-backed test suite (`CREATE TABLE` / `INSERT` / `SELECT`) — which validates the `SQLStore.Query` code path but bypasses the tunnel hop, since there's no k8s cluster here to `port-forward` to. The tunnel subprocess lifecycle itself (`internal/tunnel`) is covered by `TestTunnel_Start_Success`, `TestTunnel_Stop_GracefulThenForceKill`, etc. against a fake process runner — real `kubectl port-forward` process management was not exercised. `app.Disconnect` stopping a real tunnel subprocess (so `ps aux | grep port-forward` comes up empty afterward) was not observed live.

2. **Tunnel auto-reconnect**: manually `kill` the `kubectl port-forward` subprocess while connected → confirm dbtui detects it and reconnects within the documented backoff window, status bar reflects each state transition
   **[not verified — requires a live cluster + tunnel to kill, same gap as item 1]**
   The retry/backoff logic itself (3 attempts, 1s/2s/4s, `StatusConnected`/`StatusError` outcome) is covered by `TestTunnel_AutoReconnect_RetriesWithBackoff` and `TestTunnel_AutoReconnect_GivesUpAfterMaxRetries` against a fake runner and a fake clock — not against a real killed `kubectl` process.

3. **MySQL**: same as (1) against a MySQL pod
   **[not verified — requires a live Kubernetes cluster; additionally, no automated live-MySQL integration test was written]**
   Per the test spec's Definition of Done, only Postgres (§3.4-3.6) is required to have a live-DB automated test; MySQL's driver (`internal/db/mysql`) shares the same `database/sql`-based implementation pattern as Postgres and compiles/vets cleanly, but has not been exercised against a real MySQL server in this environment.

4. **Redis**: connect, browse keys across DB indexes 0-15, run `SET`/`GET`/`FLUSHDB` via `:query` and confirm the confirm-modal appears for `FLUSHDB`
   **[not verified — no live Redis server in this environment, and no k8s cluster to tunnel to one]**
   `views.newKVBrowser` (key list with type/TTL columns, via `db.ScanAllKeys` pagination) and `views.QueryPage`'s KV mode (redis-cli-style input, `IsDestructiveRedisCommand` confirm-modal gate before `RunCommand`) are both implemented against the real `internal/db/redis` driver, which builds and vets cleanly. The parts that don't need a live server are automated: `TestDestructiveCommandDetection` (the `FLUSHDB` confirm-modal gate) and `TestKVStore_ScanKeys_Pagination` (cursor-pagination logic against a fake `KVStore`) both pass. No live Redis server was available to drive the real end-to-end flow.

5. **MongoDB**: connect, browse database→collection tree, run a `Find` with a non-trivial filter (`{"age": {"$gt": 30}}`), confirm document view renders and `.jsonl` export produces valid JSON Lines
   **[not verified — no live MongoDB server in this environment]**
   `views.newDocumentBrowser` (database → collection `TreeView`, `Enter` on a collection running `Find`) and `views.QueryPage`'s Document mode (`db.collection` target field + JSON filter `TextArea`, `Ctrl+R` to run) are both implemented against the real `internal/db/mongo` driver, which builds and vets cleanly. `TestDocumentStore_Find_DefaultLimit` covers the pure default-limit logic; `TestResultView_DocResultFormatting` and the `ExportJSONL`/document-formatting helpers are covered against synthetic `DocResult` data, not a live `Find()` call. No live MongoDB server was available to drive the real end-to-end flow or produce a real `.jsonl` export to inspect.

6. **Password never on disk in plaintext**: after configuring a connection with a password, `grep -r "<the password>" ~/.config/dbtui/` and confirm zero matches
   **[not verified interactively, but strongly covered by automated tests]**
   `config.Connection` structurally has no password field at all (see `internal/config/types.go`), so `config.Save()` cannot serialize one — enforced by `TestSave_NeverWritesPassword`, a static guard over the actual YAML bytes written to disk. Combined with `TestNoPasswordInLogs` (masking in error/log paths) and `TestSetGetDeletePassword_RoundTrip` (the real password only ever goes through the keyring `backend` interface), this is automated-equivalent coverage, though it was not run against a literal `~/.config/dbtui/` directory in an interactive session.

7. **Crash recovery**: type a long query into `:query`, force-kill the app process, relaunch, confirm the scratch buffer restored the in-progress query
   **[implemented, not verified live]**
   `internal/views/scratch.go` now implements the spec §5.6 behavior: SQL-mode `:query`'s `TextArea` debounce-saves to `~/.config/dbtui/scratch.sql` 500ms after typing settles (`scratchDebouncer`, a `time.Timer` reset on every keystroke via `SetChangedFunc`, not a per-keystroke `time.Sleep`), and `NewQueryPage` pre-fills the `TextArea` from that file on construction if it's non-empty. Covered by real automated tests: `TestSaveLoadScratch_RoundTrip` (write, then load, byte-for-byte), `TestScratchDebouncer_CoalescesRapidTriggers` (5 rapid triggers → exactly 1 save, with the *latest* text — simulates fast typing), `TestScratchDebouncer_Stop_CancelsPendingSave`. What's not verified: an actual force-`kill -9` of a running `dbtui` process followed by a real relaunch in a live terminal — no TTY in this sandbox to do that click-through.

8. **Large result set**: run a query against a table with >1000 rows, confirm the "(capped at 1000)" footer appears and the UI does not freeze/lag
   **[not verified interactively (needs a real running TUI + a >1000-row table), but the underlying logic is fully covered]**
   `TestSQLStore_AutoLimitHeuristic` (the `LIMIT 1000` heuristic) and `ResultFooter`'s `"(capped at 1000)"` suffix logic are both unit-tested. Real end-to-end UI responsiveness against a >1000-row live table was not observed.

9. **Keychain integration**: delete a connection, confirm the corresponding OS keychain entry is also removed (`security find-generic-password -s dbtui -a <name>` on macOS should fail afterward)
   **[not verified — deliberately not run against the real OS keychain]**
   Running this for real would leave (or attempt to delete) actual entries in the operator's macOS keychain from an unattended test run, which isn't appropriate to do without being asked. `secrets.DeletePassword`'s contract (returns `ErrNotFound` on a second delete / for a name never set) is covered by `TestGetPassword_NonexistentConnection_ReturnsNotFound` against the in-memory fake backend from `TestSetGetDeletePassword_RoundTrip`.

## What's actually wired up (not just pure functions)

Beyond the pure/extracted functions test spec §7 requires, this build includes real, working `tview` glue for the core flow: `:conn` (Enter to connect — including starting the `kubectl` tunnel when configured, `d` to disconnect, `n`/`e`/`x` to create/edit/delete a saved connection via the `ConnFormView` modal), `:tables` (SQL schema/table tree with `Enter` to run `SELECT * ... LIMIT 100` and `d` to describe; Redis key list; Mongo database/collection tree), `:query` (SQL `Ctrl+R`, Redis-style `Enter` with the destructive-command confirm modal, Mongo `Ctrl+R` against a `db.collection` target field), result `y`/`Y`/`e` (clipboard yank, CSV/`.jsonl` export to `~/.config/dbtui/exports/`), `:history`, and `:logs`. None of this was driven inside a real terminal session in this environment (see gaps below), but it is real code, not stubs — it builds, vets, and its extracted logic is unit-tested.

## Known gaps (see final implementation report for full detail)

- The full interactive TUI was never driven end-to-end in a real terminal session in this environment (no display to attach to) — its wiring was verified by reading the code and by `go build`/`go vet`/`go test` passing, not by a live click-through.

The two gaps previously listed here — the `scratch.sql` autosave and the `n`/`e`/`x` connection-form modal wiring — are now implemented (see items 7 and the note below) and covered by automated tests (`internal/views/scratch_test.go`, `internal/views/connform_test.go`).

## Connection CRUD (`n`/`e`/`x`)

`internal/views/connform.go` now wires a real `tview.Form` modal (`ConnFormView`) over `:conn`: `n` opens it empty, `e` pre-fills it from the selected connection (via `ConnFormValuesFromConnection`/`ConnFormTunnelValuesFromConnection`), `x` shows a confirm modal then removes the connection (`RemoveConnectionByName`) and its keyring entry. The Type dropdown and "use kubectl tunnel" checkbox rebuild the form's field list live. Submit validates via `BuildConnectionFromForm` (wrapping the existing `ValidateConnForm`), then persists via `config.Save` + `secrets.SetPassword`; edits that leave Password blank keep the existing stored password untouched (password is never read back for display). All of the state-transition/validation logic (`ConnFormValuesFromConnection`, `BuildConnectionFromForm`, `UpsertConnection`, `RemoveConnectionByName`) is unit-tested without a `tview` runtime; the `tview.Form`/`Modal` wiring itself was not driven live (same no-TTY caveat as everywhere else in this checklist).
