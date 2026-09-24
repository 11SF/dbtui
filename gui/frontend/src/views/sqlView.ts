// SQL mode (postgres/mysql): schema/table tree + query editor + result
// grid. Selecting a table runs SELECT * ... LIMIT 100, same as the old
// TUI's browser; the editor stays free-text so the user can then refine
// the query and re-run with Cmd/Ctrl+Enter.
import { store, showToast } from "../state";
import { api, errorMessage, type TableRef } from "../api";
import { formatTableDescription } from "../format";
import { ResultGrid, type EditContext } from "../components/resultGrid";
import { infoDialog } from "../components/overlays";
import { attachAutocomplete, type Suggestion } from "../components/autocomplete";

const SQL_KEYWORDS = [
  "SELECT", "FROM", "WHERE", "JOIN", "LEFT JOIN", "RIGHT JOIN", "INNER JOIN",
  "ON", "GROUP BY", "ORDER BY", "HAVING", "LIMIT", "OFFSET", "AS", "AND",
  "OR", "NOT", "IN", "LIKE", "ILIKE", "IS NULL", "IS NOT NULL", "BETWEEN",
  "INSERT INTO", "VALUES", "UPDATE", "SET", "DELETE FROM", "DISTINCT",
  "COUNT", "SUM", "AVG", "MIN", "MAX", "UNION", "UNION ALL", "EXISTS",
  "CASE", "WHEN", "THEN", "ELSE", "END",
];

// Words preceding a table reference — used to decide when it's worth
// lazily fetching a table's columns for suggestions.
const TABLE_CONTEXT_RE = /\b(?:from|join|into|update)\s+([a-zA-Z_][\w]*(?:\.[a-zA-Z_][\w]*)?)/gi;

const WHERE_KEYWORDS = ["AND", "OR", "NOT", "LIKE", "ILIKE", "IN", "IS NULL", "IS NOT NULL", "BETWEEN"];
const ORDER_KEYWORDS = ["ASC", "DESC"];

// A result is only safe to edit in place when it maps 1:1 onto a single
// real table — a plain "SELECT ... FROM table [WHERE ...]" with no JOIN (a
// join's columns don't belong to one table, so there's no single row to
// UPDATE). The FROM target may or may not be schema-qualified (MySQL
// queries commonly aren't); resolveTableKey below resolves a bare name.
const SIMPLE_SELECT_RE = /^\s*select\b[\s\S]*?\bfrom\s+([a-zA-Z_]\w*)(?:\.([a-zA-Z_]\w*))?\b/i;

/** Quotes an identifier for interpolation into generated SQL (UPDATE
 * column names only — everywhere else in this file matches the rest of
 * the app's style of interpolating schema/table names bare). Columns come
 * from DescribeTable rather than the user's own typing, so unlike
 * schema/table they can plausibly need case-preserving quotes. */
function quoteIdent(name: string, dbType: string): string {
  if (dbType === "mysql") return "`" + name.replace(/`/g, "``") + "`";
  return '"' + name.replace(/"/g, '""') + '"';
}

/** Quotes a value for interpolation into generated SQL. Doubling the quote
 * is valid in both engines; MySQL additionally treats backslash as an
 * escape character by default, so a literal backslash right before the
 * closing quote there would otherwise swallow it. */
function sqlLiteral(value: string, dbType: string): string {
  let v = value;
  if (dbType === "mysql") v = v.replace(/\\/g, "\\\\");
  return `'${v.replace(/'/g, "''")}'`;
}

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
      <div class="quick-filter" data-quick-filter hidden>
        <span class="quick-filter__table" data-qf-table></span>
        <label class="quick-filter__field">
          <span>WHERE</span>
          <input type="text" data-qf-where placeholder="status = 'active'" spellcheck="false" />
        </label>
        <label class="quick-filter__field">
          <span>ORDER BY</span>
          <input type="text" data-qf-order placeholder="created_at DESC" spellcheck="false" />
        </label>
        <button type="button" class="btn btn--primary" data-qf-apply>Apply ⏎</button>
        <button type="button" class="btn btn--ghost" data-qf-clear>Clear</button>
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

  // Quick filter — a DataGrip-style shortcut for the common "browse a
  // table" case: fill in WHERE/ORDER BY fragments instead of writing the
  // full SELECT. It targets whichever table was last picked from the tree
  // (or run via a bare "SELECT * FROM schema.table" in the editor) and
  // stays in sync with the generated SQL, which lands in the editor too so
  // it's visible and still hand-editable.
  const quickFilterBar = el.querySelector<HTMLElement>("[data-quick-filter]")!;
  const qfTableLabel = el.querySelector<HTMLElement>("[data-qf-table]")!;
  const qfWhere = el.querySelector<HTMLInputElement>("[data-qf-where]")!;
  const qfOrder = el.querySelector<HTMLInputElement>("[data-qf-order]")!;
  let quickFilterTable: { schema: string; table: string } | null = null;

  function showQuickFilter(schema: string, table: string): void {
    quickFilterTable = { schema, table };
    qfTableLabel.textContent = `${schema}.${table}`;
    qfWhere.value = "";
    qfOrder.value = "";
    quickFilterBar.hidden = false;
    loadColumns(schema, table);
  }

  function quickFilterColumns(): Suggestion[] {
    if (!quickFilterTable) return [];
    const key = `${quickFilterTable.schema}.${quickFilterTable.table}`;
    return (columnsByTable[key] ?? []).map((c) => ({ label: c, detail: "column" }));
  }

  function hideQuickFilter(): void {
    quickFilterTable = null;
    quickFilterBar.hidden = true;
  }

  function applyQuickFilter(): void {
    if (!quickFilterTable) return;
    const { schema, table } = quickFilterTable;
    const where = qfWhere.value.trim();
    const order = qfOrder.value.trim();
    let sql = `SELECT * FROM ${schema}.${table}`;
    if (where) sql += ` WHERE ${where}`;
    if (order) sql += ` ORDER BY ${order}`;
    sql += ` LIMIT 100`;
    editor.value = sql;
    runQuery(sql);
  }

  quickFilterBar.addEventListener("click", (e) => {
    const act = (e.target as HTMLElement).closest<HTMLElement>("[data-qf-apply],[data-qf-clear]");
    if (!act) return;
    if (act.hasAttribute("data-qf-apply")) applyQuickFilter();
    if (act.hasAttribute("data-qf-clear")) {
      qfWhere.value = "";
      qfOrder.value = "";
      applyQuickFilter();
    }
  });
  for (const input of [qfWhere, qfOrder]) {
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        applyQuickFilter();
      }
    });
  }

  const whereAutocomplete = attachAutocomplete(qfWhere, () => [
    ...quickFilterColumns(),
    ...WHERE_KEYWORDS.map((k) => ({ label: k, detail: "keyword" })),
  ]);
  const orderAutocomplete = attachAutocomplete(qfOrder, () => [
    ...quickFilterColumns(),
    ...ORDER_KEYWORDS.map((k) => ({ label: k, detail: "keyword" })),
  ]);

  // Columns/primary-keys are fetched on demand (there's no bulk "describe
  // everything" API) and cached by "schema.table" so re-suggesting or
  // re-editing the same table doesn't refetch. Keyed separately per
  // connection via onEnter's reset.
  let columnsByTable: Record<string, string[]> = {};
  let primaryKeysByTable: Record<string, string[]> = {};
  const pendingDescribes = new Map<string, Promise<void>>();

  function resolveTableKey(ref: string): string | null {
    if (ref.includes(".")) return ref;
    // Bare table name: match it against whatever schema(s) we've already
    // loaded tables for, preferring the currently-expanded ones.
    for (const schema of store.state.expandedSchemas) {
      const tables = store.state.tablesBySchema[schema];
      if (tables?.some((t) => t.Name === ref)) return `${schema}.${ref}`;
    }
    for (const [schema, tables] of Object.entries(store.state.tablesBySchema)) {
      if (tables.some((t) => t.Name === ref)) return `${schema}.${ref}`;
    }
    return null;
  }

  function detectEditableTable(sql: string): { schema: string; table: string } | null {
    if (/\bjoin\b/i.test(sql)) return null;
    const m = SIMPLE_SELECT_RE.exec(sql);
    if (!m) return null;
    const key = resolveTableKey(m[2] ? `${m[1]}.${m[2]}` : m[1]);
    if (!key) return null;
    const [schema, table] = key.split(".");
    return { schema, table };
  }

  // Shared by the editor's suggestions (which scan the SQL text for table
  // references), the quick filter (which already knows its table), and
  // cell editing (which additionally needs the primary key to target an
  // UPDATE at one row) — all three just want "make sure this table's
  // metadata is cached," and the first two want every open suggestion
  // popup to pick the result up once it lands.
  function loadColumns(schema: string, table: string): Promise<void> {
    const key = `${schema}.${table}`;
    if (columnsByTable[key]) return Promise.resolve();
    const pending = pendingDescribes.get(key);
    if (pending) return pending;
    const promise = api
      .describeTable(schema, table)
      .then((desc) => {
        columnsByTable[key] = (desc.Columns ?? []).map((c) => c.Name);
        primaryKeysByTable[key] = desc.PrimaryKeys ?? [];
      })
      .catch(() => {
        // Table might not exist / describe might fail — just stop
        // retrying it, suggestions fall back to keywords/tables (and cell
        // editing stays disabled for it).
      })
      .finally(() => {
        pendingDescribes.delete(key);
        autocomplete.refresh();
        whereAutocomplete.refresh();
        orderAutocomplete.refresh();
      });
    pendingDescribes.set(key, promise);
    return promise;
  }

  function ensureColumnsLoaded(sql: string): void {
    for (const m of sql.matchAll(TABLE_CONTEXT_RE)) {
      const key = resolveTableKey(m[1]);
      if (!key) continue;
      const [schema, table] = key.split(".");
      loadColumns(schema, table);
    }
  }

  // Builds the ResultGrid EditContext for a plain single-table SELECT (see
  // detectEditableTable) — an UPDATE keyed on the row's primary key, run
  // through the same api.runQuery path as everything else in this view.
  function buildEditContext(schema: string, table: string): EditContext {
    const key = `${schema}.${table}`;
    return {
      async commit(row, columns, colIndex, newValue) {
        await loadColumns(schema, table);
        const pk = primaryKeysByTable[key] ?? [];
        if (pk.length === 0) {
          throw new Error(`${schema}.${table} has no primary key — can't target a single row to update`);
        }
        const dbType = store.state.status.type;
        const whereParts = pk.map((col) => {
          const idx = columns.indexOf(col);
          if (idx < 0) {
            throw new Error(`Can't edit — the result is missing primary key column "${col}" (select it explicitly, or use SELECT *)`);
          }
          const val = row[idx];
          const cmp = val === null || val === undefined ? "IS NULL" : `= ${sqlLiteral(String(val), dbType)}`;
          return `${quoteIdent(col, dbType)} ${cmp}`;
        });
        const setClause = `${quoteIdent(columns[colIndex], dbType)} = ${
          newValue === null ? "NULL" : sqlLiteral(newValue, dbType)
        }`;
        const sql = `UPDATE ${schema}.${table} SET ${setClause} WHERE ${whereParts.join(" AND ")}`;
        await api.runQuery(sql);
      },
    };
  }

  function buildSuggestions(sql: string): Suggestion[] {
    const keywordItems: Suggestion[] = SQL_KEYWORDS.map((k) => ({ label: k, detail: "keyword" }));
    const schemaItems: Suggestion[] = store.state.schemas.map((s) => ({ label: s, detail: "schema" }));
    const tableItems: Suggestion[] = [];
    for (const [schema, tables] of Object.entries(store.state.tablesBySchema)) {
      for (const t of tables) {
        tableItems.push({ label: t.Name, detail: schema });
        tableItems.push({ label: `${schema}.${t.Name}`, detail: t.Kind });
      }
    }
    const columnItems: Suggestion[] = [];
    for (const [table, columns] of Object.entries(columnsByTable)) {
      for (const c of columns) columnItems.push({ label: c, detail: table });
    }
    ensureColumnsLoaded(sql);
    return [...columnItems, ...tableItems, ...schemaItems, ...keywordItems];
  }

  const autocomplete = attachAutocomplete(editor, ({ fullText }) => buildSuggestions(fullText));

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
    columnsByTable = {};
    primaryKeysByTable = {};
    hideQuickFilter();
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
      const target = detectEditableTable(sql);
      if (target) loadColumns(target.schema, target.table);
      grid.setResult(res, target ? buildEditContext(target.schema, target.table) : null);
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
      showQuickFilter(schema, table);
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
      hideQuickFilter();
    }
  });

  const onEnter = () => {
    store.set((s) => {
      s.schemas = [];
      s.tablesBySchema = {};
      s.expandedSchemas = new Set();
      s.sqlDatabases = [];
    });
    columnsByTable = {};
    primaryKeysByTable = {};
    hideQuickFilter();
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
