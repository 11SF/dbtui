import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "./style.css";
import { store, showToast } from "./state";
import { api, onStatus, errorMessage } from "./api";
import type { ViewName } from "./state";

// Devtools are off in a production build, so an uncaught error otherwise
// has nowhere to go. Logging it is the least this can do until something
// actually surfaces it in the UI.
window.addEventListener("error", (e) => console.error("uncaught error", e.error ?? e.message));
window.addEventListener("unhandledrejection", (e) => console.error("unhandled rejection", e.reason));

import { mountStatusStrip } from "./components/statusStrip";
import { mountSidebar } from "./components/sidebar";
import { mountConnectionForm } from "./components/connectionForm";
import { mountCommandPalette } from "./components/commandPalette";
import { mountToast, mountConfirmDialog, mountInfoDialog } from "./components/overlays";

import { mountEmptyState } from "./views/emptyState";
import { mountSqlView } from "./views/sqlView";
import { mountKvView } from "./views/kvView";
import { mountMongoView } from "./views/mongoView";
import { mountHistoryView } from "./views/historyView";
import { mountLogsView } from "./views/logsView";

const appRoot = document.querySelector<HTMLDivElement>("#app")!;
appRoot.innerHTML = "";

const shell = document.createElement("div");
shell.className = "shell";
shell.innerHTML = `
  <div class="shell__statusbar" data-statusbar></div>
  <div class="shell__body">
    <div class="shell__sidebar" data-sidebar></div>
    <div class="shell__main">
      <div class="shell__tabs" data-tabs hidden>
        <button type="button" class="tab" data-view="query">Query</button>
        <button type="button" class="tab" data-view="history">History</button>
        <button type="button" class="tab" data-view="logs">Logs</button>
      </div>
      <div class="shell__content" data-content></div>
    </div>
  </div>
`;
appRoot.appendChild(shell);

const statusbarMount = shell.querySelector<HTMLElement>("[data-statusbar]")!;
const sidebarMount = shell.querySelector<HTMLElement>("[data-sidebar]")!;
const tabsBar = shell.querySelector<HTMLElement>("[data-tabs]")!;
const contentMount = shell.querySelector<HTMLElement>("[data-content]")!;

// Each mount is independent UI; a throw in any one of them must not take
// the rest of the app down with it — without this, a single bad mount
// halts this whole script, leaving the shell painted but *no* listener
// anywhere ever attached (not even the tab bar or keyboard shortcuts
// below), which looks exactly like a frozen, unclickable window.
function mountSafely(name: string, fn: () => void): void {
  try {
    fn();
  } catch (err) {
    console.error(`mount failed: ${name}`, err);
  }
}

mountSafely("statusStrip", () => mountStatusStrip(statusbarMount));
mountSafely("sidebar", () => mountSidebar(sidebarMount));
mountSafely("connectionForm", () => mountConnectionForm(appRoot));
mountSafely("commandPalette", () => mountCommandPalette(appRoot));
mountSafely("toast", () => mountToast(appRoot));
mountSafely("confirmDialog", () => mountConfirmDialog(appRoot));
mountSafely("infoDialog", () => mountInfoDialog(appRoot));

// Every content view is built once and swapped into `contentMount` as
// needed, rather than torn down/rebuilt — this keeps editor/input state
// (and, for logsView, its poll timer) predictable across re-renders.
function mountViewSafely(
  name: string,
  fn: (el: HTMLElement) => MountedView,
): MountedView {
  try {
    return fn(document.createElement("div"));
  } catch (err) {
    console.error(`view mount failed: ${name}`, err);
    const fallback = document.createElement("div");
    fallback.className = "view-error";
    fallback.textContent = `This view failed to load (${name}). See the logs view or devtools console for details.`;
    return { el: fallback };
  }
}

type MountedView = { el: HTMLElement; onEnter?: () => void; onLeave?: () => void };

const empty = mountViewSafely("empty", mountEmptyState);
const sql = mountViewSafely("sql", mountSqlView);
const kv = mountViewSafely("kv", mountKvView);
const mongo = mountViewSafely("mongo", mountMongoView);
const historyV = mountViewSafely("history", mountHistoryView);
const logsV = mountViewSafely("logs", mountLogsView);

let currentKey = "";
let current: MountedView | null = null;

function resolveKey(): string {
  const { status, activeView } = store.state;
  if (status.status !== "connected") return "empty";
  if (activeView === "history") return "history";
  if (activeView === "logs") return "logs";
  switch (status.category) {
    case "sql":
      return "query-sql";
    case "kv":
      return "query-kv";
    case "document":
      return "query-document";
    default:
      return "empty";
  }
}

function viewFor(key: string): MountedView {
  switch (key) {
    case "query-sql":
      return sql;
    case "query-kv":
      return kv;
    case "query-document":
      return mongo;
    case "history":
      return historyV;
    case "logs":
      return logsV;
    default:
      return empty;
  }
}

function renderTabs(): void {
  const connected = store.state.status.status === "connected";
  tabsBar.hidden = !connected;
  tabsBar.querySelectorAll<HTMLButtonElement>(".tab").forEach((btn) => {
    btn.classList.toggle("is-active", btn.dataset.view === store.state.activeView);
  });
}

function renderContent(): void {
  const key = resolveKey();
  renderTabs();
  if (key === currentKey) return;
  current?.onLeave?.();
  contentMount.innerHTML = "";
  current = viewFor(key);
  contentMount.appendChild(current.el);
  current.onEnter?.();
  currentKey = key;
}

tabsBar.addEventListener("click", (e) => {
  const view = (e.target as HTMLElement).dataset.view as ViewName | undefined;
  if (view) store.set((s) => (s.activeView = view));
});

store.subscribe(renderContent);

// Global keyboard shortcuts: Cmd/Ctrl+K opens the command palette from
// anywhere; "n" opens the new-connection form when not typing elsewhere —
// the keyboard-first affordances this app is built around.
document.addEventListener("keydown", (e) => {
  const typing = ["INPUT", "TEXTAREA", "SELECT"].includes((e.target as HTMLElement).tagName);

  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
    e.preventDefault();
    store.set((s) => (s.paletteOpen = true));
    return;
  }
  if (!typing && e.key === "n" && !store.state.connectionFormOpen) {
    e.preventDefault();
    store.set((s) => (s.connectionFormOpen = { mode: "new" }));
  }
});

// Base interactivity (tab clicks, keyboard shortcuts, the initial render)
// is wired above and does not depend on the Wails native bridge being
// ready yet, so it works even if what follows fails. `EventsOn` below is
// the one call in this app that touches `window.runtime` directly — on a
// production build (as opposed to `wails dev`) that bridge object is
// usually injected before page scripts run, but isn't guaranteed to be by
// the time this synchronous top-level code executes, so guard it and
// retry once on the next tick rather than letting an early throw here
// silently drop live status updates for the rest of the session.
function subscribeStatus(retriesLeft = 3): void {
  try {
    onStatus((status) => {
      store.set((s) => (s.status = status));
      if (status.status === "error" && status.error) showToast(status.error);
    });
  } catch (err) {
    console.error("onStatus subscription failed", err);
    if (retriesLeft > 0) setTimeout(() => subscribeStatus(retriesLeft - 1), 200);
  }
}

async function boot(): Promise<void> {
  try {
    const [conns, status] = await Promise.all([api.listConnections(), api.getStatus()]);
    store.set((s) => {
      s.connections = conns;
      s.status = status;
    });
  } catch (err) {
    showToast(errorMessage(err));
  }
}

renderContent();
boot();
subscribeStatus();
