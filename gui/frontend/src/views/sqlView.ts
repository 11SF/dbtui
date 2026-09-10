// SQL mode (postgres/mysql): schema/table tree + query editor + result
// grid. Selecting a table runs SELECT * ... LIMIT 100, same as the old
// TUI's browser; the editor stays free-text so the user can then refine
// the query and re-run with Cmd/Ctrl+Enter.
import { store, showToast } from "../state";
import { api, errorMessage, type TableRef } from "../api";
import { formatTableDescription } from "../format";
import { ResultGrid } from "../components/resultGrid";
import { infoDialog } from "../components/overlays";

export function mountSqlView(root: HTMLElement): { el: HTMLElement; onEnter: () => void } {
  const el = document.createElement("div");
  el.className = "sql-view";
  el.innerHTML = `
    <div class="tree-pane">
      <div class="tree-pane__header" data-db-header hidden>Databases</div>
      <div class="tree" data-db-tree hidden></div>
      <div class="tree-pane__header">Schemas</div>
      <div class="tree" data-tree></div>
    </div>
    <div class="query-pane">
      <textarea class="editor" spellcheck="false" placeholder="SELECT * FROM ...  (Cmd/Ctrl + Enter to run)"></textarea>
      <div class="query-pane__toolbar">
        <button type="button" class="btn btn--primary" data-act="run">Run ⌘⏎</button>
        <button type="button" class="btn" data-act="export">Export CSV</button>
      </div>
      <div class="result-slot"></div>
    </div>`;
  root.appendChild(el);

  const tree = el.querySelector<HTMLElement>("[data-tree]")!;
  const dbHeader = el.querySelector<HTMLElement>("[data-db-header]")!;
  const dbTree = el.querySelector<HTMLElement>("[data-db-tree]")!;
  const editor = el.querySelector<HTMLTextAreaElement>(".editor")!;
  const grid = new ResultGrid();
  el.querySelector(".result-slot")!.appendChild(grid.el);

  // "Show all databases" (set per-connection in the connection form) adds
  // this tier above the schema tree — Postgres/MySQL both require a fresh
  // connection to change database (no in-session USE-equivalent in
  // SQLStore), which api.switchDatabase handles server-side.
  const renderDatabases = () => {
    const show = store.state.status.showAllDatabases;
    dbHeader.hidden = !show;
    dbTree.hidden = !show;
    if (!show) return;
    const current = store.state.status.dbName;
    dbTree.innerHTML = store.state.sqlDatabases
      .map((name) => {
        const isCurrent = name === current;
        return `
          <div class="tree__row${isCurrent ? " tree__row--current" : ""}" data-db="${attr(name)}" tabindex="0">
            <span>${escapeHtml(name)}</span>
            ${isCurrent ? `<span class="tree__kind">current</span>` : ""}
          </div>`;
      })
      .join("");
  };

  async function loadDatabases(): Promise<void> {
    try {
      const dbs = (await api.listSQLDatabases()) ?? [];
      store.set((s) => (s.sqlDatabases = dbs));
    } catch (err) {
      showToast(errorMessage(err));
    }
  }

  async function switchDatabase(name: string): Promise<void> {
    if (name === store.state.status.dbName) return;
    try {
      await api.switchDatabase(name);
    } catch (err) {
      showToast(errorMessage(err));
      return;
    }
    // The "status" event from switchDatabase already updates
    // store.state.status (including the new dbName); the schema tree
    // itself belongs to the database just left behind, so reload it the
    // same way onEnter does for a fresh connection.
    store.set((s) => {
      s.schemas = [];
      s.tablesBySchema = {};
      s.expandedSchemas = new Set();
    });
    grid.clear();
    try {
      const schemas = (await api.listSchemas()) ?? [];
      store.set((s) => (s.schemas = schemas));
    } catch (err) {
      showToast(errorMessage(err));
    }
  }

  dbTree.addEventListener("click", (e) => {
    const row = (e.target as HTMLElement).closest<HTMLElement>("[data-db]");
    if (row) switchDatabase(row.dataset.db!);
  });

  const renderTree = () => {
    const { schemas, tablesBySchema, expandedSchemas } = store.state;
    tree.innerHTML = schemas
      .map((schema) => {
        const open = expandedSchemas.has(schema);
        const tables = tablesBySchema[schema];
        return `
          <div class="tree__schema">
            <div class="tree__row" data-schema="${attr(schema)}" tabindex="0">
              <span class="tree__caret">${open ? "▾" : "▸"}</span>
              <span>${escapeHtml(schema)}</span>
            </div>
            ${
              open
                ? `<div class="tree__children">
                    ${
                      tables
                        ? tables.map((t) => tableRowHtml(schema, t)).join("")
                        : `<div class="tree__loading">loading…</div>`
                    }
                   </div>`
                : ""
            }
          </div>`;
      })
      .join("");
  };

  async function loadTables(schema: string): Promise<void> {
    try {
      const tables = (await api.listTables(schema)) ?? [];
      store.set((s) => (s.tablesBySchema[schema] = tables));
    } catch (err) {
      showToast(errorMessage(err));
    }
  }

  async function runQuery(sql: string): Promise<void> {
    if (!sql.trim()) return;
    try {
      const res = await api.runQuery(sql);
      grid.setResult(res);
    } catch (err) {
      grid.showError(errorMessage(err));
    }
  }

  tree.addEventListener("click", async (e) => {
    const target = e.target as HTMLElement;
    const infoBtn = target.closest<HTMLElement>("[data-describe]");
    if (infoBtn) {
      e.stopPropagation();
      const [schema, table] = infoBtn.dataset.describe!.split(" ");
      try {
        const desc = await api.describeTable(schema, table);
        infoDialog(`${schema}.${table}`, formatTableDescription(desc));
      } catch (err) {
        showToast(errorMessage(err));
      }
      return;
    }
    const tableRow = target.closest<HTMLElement>("[data-table]");
    if (tableRow) {
      const [schema, table] = tableRow.dataset.table!.split(" ");
      const sql = `SELECT * FROM ${schema}.${table} LIMIT 100`;
      editor.value = sql;
      await runQuery(sql);
      return;
    }
    const schemaRow = target.closest<HTMLElement>("[data-schema]");
    if (schemaRow) {
      const schema = schemaRow.dataset.schema!;
      const alreadyOpen = store.state.expandedSchemas.has(schema);
      store.set((s) => {
        if (alreadyOpen) s.expandedSchemas.delete(schema);
        else s.expandedSchemas.add(schema);
      });
      if (!alreadyOpen && !store.state.tablesBySchema[schema]) await loadTables(schema);
    }
  });

  editor.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      runQuery(editor.value);
    }
  });

  el.addEventListener("click", async (e) => {
    const act = (e.target as HTMLElement).dataset.act;
    if (act === "run") runQuery(editor.value);
    if (act === "export") {
      try {
        const path = await api.exportResultCSV();
        showToast(`Exported to ${path}`, "info");
      } catch (err) {
        showToast(errorMessage(err));
      }
    }
  });

  store.subscribe(renderTree);
  store.subscribe(renderDatabases);
  store.subscribe(() => {
    if (store.state.pendingLoadQuery !== null && store.state.status.category === "sql") {
      editor.value = store.state.pendingLoadQuery;
      store.state.pendingLoadQuery = null;
    }
  });

  const onEnter = () => {
    store.set((s) => {
      s.schemas = [];
      s.tablesBySchema = {};
      s.expandedSchemas = new Set();
      s.sqlDatabases = [];
    });
    grid.clear();
    api
      .listSchemas()
      .then((schemas) => store.set((s) => (s.schemas = schemas ?? [])))
      .catch((err) => showToast(errorMessage(err)));
    if (store.state.status.showAllDatabases) loadDatabases();
  };

  return { el, onEnter };
}

function tableRowHtml(schema: string, t: TableRef): string {
  const key = `${schema} ${t.Name}`;
  return `
    <div class="tree__row tree__row--leaf" data-table="${attr(key)}" tabindex="0">
      <span>${escapeHtml(t.Name)}</span>
      <span class="tree__kind">${escapeHtml(t.Kind)}</span>
      <button type="button" class="btn btn--icon" data-describe="${attr(key)}" title="Describe">ⓘ</button>
    </div>`;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function attr(s: string): string {
  return escapeHtml(s).replace(/"/g, "&quot;");
}
