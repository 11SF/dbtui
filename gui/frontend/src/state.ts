import type { Connection, QueryResult, DocResult, StatusPayload } from "./api";

export type ViewName = "query" | "history" | "logs";

export interface AppState {
  connections: Connection[];
  status: StatusPayload;
  activeView: ViewName;

  // SQL-mode tree + result
  schemas: string[];
  tablesBySchema: Record<string, import("./api").TableRef[]>;
  expandedSchemas: Set<string>;
  /** Only populated (and only shown) when status.showAllDatabases is set
   * on the active connection — see sqlView.ts's "Databases" tier. */
  sqlDatabases: string[];
  lastQueryResult: QueryResult | null;
  lastQueryError: string | null;
  lastQueryMeta: { ms: number; rows: number; capped: boolean } | null;

  // Document-mode tree + result
  mongoDatabases: string[];
  collectionsByDb: Record<string, string[]>;
  expandedMongoDbs: Set<string>;
  lastDocResult: DocResult | null;
  lastDocError: string | null;
  mongoTarget: string; // "database.collection"

  // KV-mode
  redisDbIndex: number;
  redisKeys: string[];
  redisKeyMeta: Record<string, { type: string; ttl: number }>;
  lastKVResult: string | null;
  lastKVError: string | null;

  connectionFormOpen: { mode: "new" } | { mode: "edit"; original: Connection } | null;
  deleteConfirm: string | null;
  paletteOpen: boolean;
  toast: { text: string; kind: "error" | "info" } | null;

  /** Set by History's "load into editor" action, consumed (and cleared)
   * by whichever query view is currently mounted. */
  pendingLoadQuery: string | null;
}

type Listener = () => void;

const initialStatus: StatusPayload = { name: "", type: "", category: "", status: "disconnected" } as StatusPayload;

class Store {
  private listeners = new Set<Listener>();
  state: AppState = {
    connections: [],
    status: initialStatus,
    activeView: "query",

    schemas: [],
    tablesBySchema: {},
    expandedSchemas: new Set(),
    sqlDatabases: [],
    lastQueryResult: null,
    lastQueryError: null,
    lastQueryMeta: null,

    mongoDatabases: [],
    collectionsByDb: {},
    expandedMongoDbs: new Set(),
    lastDocResult: null,
    lastDocError: null,
    mongoTarget: "",

    redisDbIndex: 0,
    redisKeys: [],
    redisKeyMeta: {},
    lastKVResult: null,
    lastKVError: null,

    connectionFormOpen: null,
    deleteConfirm: null,
    paletteOpen: false,
    toast: null,
    pendingLoadQuery: null,
  };

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /** Mutate state in place inside fn, then notify subscribers. Kept coarse
   * (whole-app notify) rather than field-level diffing — this app's scope
   * doesn't need more, and each view only re-renders the DOM it owns. */
  set(fn: (s: AppState) => void): void {
    fn(this.state);
    this.listeners.forEach((l) => l());
  }
}

export const store = new Store();

let toastTimer: ReturnType<typeof setTimeout> | undefined;

export function showToast(text: string, kind: "error" | "info" = "error"): void {
  store.set((s) => (s.toast = { text, kind }));
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => store.set((s) => (s.toast = null)), 5000);
}
