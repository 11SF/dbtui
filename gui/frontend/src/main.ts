import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "./style.css";
import { store, showToast } from "./state";
import { api, onStatus, errorMessage } from "./api";
import type { ViewName } from "./state";

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

mountStatusStrip(statusbarMount);
mountSidebar(sidebarMount);
mountConnectionForm(appRoot);
mountCommandPalette(appRoot);
mountToast(appRoot);
mountConfirmDialog(appRoot);
mountInfoDialog(appRoot);

// Every content view is built once and swapped into `contentMount` as
// needed, rather than torn down/rebuilt — this keeps editor/input state
// (and, for logsView, its poll timer) predictable across re-renders.
const empty = mountEmptyState(document.createElement("div"));
const sql = mountSqlView(document.createElement("div"));
const kv = mountKvView(document.createElement("div"));
const mongo = mountMongoView(document.createElement("div"));
const historyV = mountHistoryView(document.createElement("div"));
const logsV = mountLogsView(document.createElement("div"));

type MountedView = { el: HTMLElement; onEnter?: () => void; onLeave?: () => void };
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

onStatus((status) => {
  store.set((s) => (s.status = status));
  if (status.status === "error" && status.error) showToast(status.error);
});

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

boot();
renderContent();
