# dbtui — Manual / Integration Testing Checklist

`dbtui` is now a native desktop GUI (Wails v2: Go backend in `internal/` + `gui/`, TypeScript/HTML/CSS frontend in `gui/frontend/`), replacing the earlier terminal UI. This checklist covers what needs real infrastructure or a live interactive session that `go test` cannot exercise in this sandbox. Automated coverage for everything else lives in each package's `_test.go` files.

**Environment this was last run in**: macOS sandbox with `docker` and `kubectl` binaries present but **no live Kubernetes cluster**, **no live Postgres/MySQL/Redis/MongoDB server** except a throwaway `testcontainers-go` Postgres spun up for the automated integration tests below, **no OS keychain interaction attempted** (would leave real secrets in the operator's keychain from an unattended run), and **no display/TTY to run the actual GUI window in** — `wails build` produces a real, working `.app` bundle and `wails dev` would launch it on a machine with a display, but nothing here can click through it. Every item below that needs a live window is therefore `[not verified — no display in this sandbox]`, not a guess dressed up as a pass.

## What's automated

- `internal/config`, `internal/secrets`, `internal/db` (+ postgres/mysql/redis/mongo drivers), `internal/tunnel`, `internal/history`, `internal/logging` — the full backend, unchanged from before the GUI pivot: `go test ./internal/...` covers config round-trips, keyring CRUD against a fake backend, the SQL auto-limit heuristic, KV/Doc pure dispatch logic, tunnel free-port/backoff/lifecycle against a fake process runner, history CRUD, and a ring-buffer logger — all with `-race` clean.
- `internal/db/postgres` additionally runs 3 tests against a **real** `testcontainers-go` Postgres container (not mocked): query success, query timeout, schema/table/describe listing.
- `gui/` (the Wails Go bindings): connection CRUD including the rename + blank-password-keeps-existing-password contract, `IsDestructiveRedisCommand` (case-insensitive whole-token match, not substring), CSV/JSONL export formatting, and panic-safety checks on the bound methods.
- Frontend: `tsc --noEmit` and the real `npm run build` (which Wails' own build runs) both pass with zero errors under `strict`/`noUnusedLocals`/`noUnusedParameters`. This proves the TypeScript is internally consistent and every binding call matches the generated `.d.ts` types — it does not prove the UI behaves correctly when clicked, which is what the items below cover.
- `go run .../wails build` (from `gui/`) produces a real, code-signed, non-trivial (~25MB) `darwin/arm64` `dbtui-gui.app` bundle — confirmed to exist and be a valid Mach-O executable, not just "the build command exited 0."

## Manual checklist

1. **App launches and renders**: open `gui/build/bin/dbtui-gui.app` on a machine with a display; confirm a native window opens showing the sidebar/status-strip/empty-state shell with no blank white screen or console errors
   **[not verified — no display in this sandbox]**
   Verified instead: `wails build` completes without error, the frontend production bundle exists (`gui/frontend/dist/`), and `main.ts`'s startup path (`boot()` → `ListConnections()`/`GetStatus()`) has explicit `.catch()` error handling so a backend failure surfaces as a toast rather than an unhandled rejection or blank screen.

2. **Connection CRUD through the actual form UI**: press `+`/`n`, fill in a postgres connection, save, confirm it appears in the sidebar; edit it (rename + leave password blank), confirm the rename took effect and the old password still works; delete it, confirm both the sidebar entry and its keyring password are gone
   **[not verified — no display in this sandbox]**
   The rename/blank-password-keeps-existing-password contract is covered by a real Go test (`gui`'s `TestSaveConnection_*` cases) against the actual `SaveConnection` binding, not a mock — so the *logic* the form UI calls into is verified; the form's own DOM wiring (`connectionForm.ts`: type-based field visibility, tunnel-toggle reveal, submit → `saveConnection` → refresh sidebar) was verified by static code review only, not a live click-through.

3. **Connect flow + live status**: connect to a real Postgres (direct, no tunnel — e.g. `docker run -p 5432:5432 postgres:16-alpine`), confirm the status strip shows live progress (connecting → connected) via the `"status"` Wails event, run a query, confirm the result grid renders, disconnect
   **[not verified — no display in this sandbox]**
   The `Connect`/`Disconnect`/event-emission code path is the same one `internal/db/postgres`'s live-container integration tests exercise underneath (`db.NewClient` → `SQLStore.Query`), so the backend half is proven against a real database; the frontend's `onStatus()` subscription and status-strip re-render were not observed live.

4. **Postgres/MySQL via kubectl tunnel**: real k8s cluster (or `kind`) with a Postgres/MySQL pod reachable via `kubectl port-forward` → connect via dbtui, run a query, disconnect, confirm the tunnel subprocess is killed (`ps aux | grep port-forward` shows nothing after)
   **[not verified — requires a live Kubernetes cluster, which this environment does not have]**
   `internal/tunnel`'s subprocess lifecycle (start/stop/kill, free-port allocation) is covered by unit tests against a fake process runner; a real `kubectl port-forward` process was never spawned or killed here.

5. **Tunnel auto-reconnect**: kill the `kubectl port-forward` subprocess while connected → confirm dbtui detects it and reconnects within the documented 1s/2s/4s backoff, with the status strip reflecting each transition
   **[not verified — same live-cluster gap as item 4]**
   The retry/backoff logic itself is unit-tested against a fake runner and fake clock (`TestTunnel_AutoReconnect_*`), not against a real killed process.

6. **Redis**: connect, switch DB index 0–15, browse keys (type/TTL columns), open a key's value, run `SET`/`GET`/`FLUSHDB` via the command input, confirm the confirm dialog appears for `FLUSHDB` and the command only runs after confirming
   **[not verified — no live Redis server in this environment]**
   `IsDestructiveRedisCommand` and the `RunKVCommand` → `{needsConfirm: true}` → re-call-with-`confirmed=true` contract are both covered by real Go tests against the actual binding logic; `internal/db/redis` builds and vets cleanly but was never driven against a live Redis server, and the confirm-dialog UI wiring (`kvView.ts`) was not clicked through.

7. **MongoDB**: connect, browse database → collection tree, run a `Find` with a non-trivial filter (`{"age": {"$gt": 30}}`), confirm the document view renders and Export → `.jsonl` produces valid JSON Lines
   **[not verified — no live MongoDB server in this environment]**
   `internal/db/mongo` builds and vets cleanly; the JSONL export formatting (`ExportJSONL`/`compactJSONLines`) is unit-tested against synthetic `DocResult` data, not a real `Find()` call.

8. **Password never on disk in plaintext**: after saving a connection with a password, `grep -r "<the password>" ~/.config/dbtui/` and confirm zero matches
   **[not verified interactively, but strongly covered by automated tests]**
   `config.Connection` has no password field at all — `config.Save()` structurally cannot serialize one, enforced by `TestSave_NeverWritesPassword`. Combined with `TestSetGetDeletePassword_RoundTrip` (the real password only ever goes through the keyring `backend` interface), this is automated-equivalent coverage.

9. **Large result set**: run a query against a table with >1000 rows, confirm the "(capped at 1000)" footer note appears and the result grid stays responsive
   **[not verified interactively (needs a live window + a >1000-row table), but the underlying logic is covered]**
   `TestSQLStore_AutoLimitHeuristic` covers the `LIMIT 1000` heuristic and `QueryResult.Capped`; `resultGrid.ts`'s footer renders `" (capped at 1000)"` when `Capped` is true (static review only — the >1000-row grid was never scrolled through live, so real rendering performance at that size is unverified, though the grid is a plain HTML `<table>` with no virtualization, which is a known scaling risk worth watching if this becomes a real pain point).

10. **Keychain integration**: delete a connection, confirm the OS keychain entry is also removed (`security find-generic-password -s dbtui -a <name>` should fail afterward)
    **[not verified — deliberately not run against the real OS keychain]**
    `secrets.DeletePassword`'s contract is covered against the in-memory fake backend (`TestGetPassword_NonexistentConnection_ReturnsNotFound`); running this against the real keychain from an unattended sandbox run isn't appropriate without being asked.

11. **Command palette (⌘K)**: open via the shortcut from anywhere in the app, filter by typing, arrow-key navigate, Enter to run a "Connect: X" / "Go to History" / etc. action
    **[not verified — no display in this sandbox]**
    `commandPalette.ts`'s action list, substring filter, and keyboard navigation were reviewed statically and compile cleanly; never exercised in a live window.

12. **Light/dark theme**: toggle the OS appearance setting while the app is open (or relaunch under each), confirm the palette swaps consistently (background, text, accent, and all four status colors stay legible and recognizable)
    **[not verified — no display in this sandbox]**
    Both token sets are defined in `style.css` via `prefers-color-scheme`; never rendered and visually inspected.

## Known gaps

- The GUI has never been opened and clicked through in a live window in this environment — no display/TTY here. Everything above marked "no display in this sandbox" is verified only by: passing Go tests against the same logic the UI calls into, `tsc`/`vite build` compiling without error, a real `wails build` producing a valid signed app bundle, and static code review of the wiring. That is meaningfully short of an actual interactive pass — the first real session with a display should work through this checklist properly.
- The result grid (`resultGrid.ts`) is a plain HTML table with no row virtualization. At the spec's 1000-row cap this is very unlikely to matter, but it's worth knowing if result caps ever get raised.
- No dedicated "connect via tunnel" happy-path was exercised even indirectly (item 3's live-container test bypasses the tunnel hop entirely, same as the old TUI's checklist noted) — tunnel start/stop is only proven against a fake process runner.
