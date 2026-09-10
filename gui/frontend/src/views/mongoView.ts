// Document mode (MongoDB): database/collection tree + JSON filter editor
// + doc result view. Selecting a collection fills the target field and
// runs an empty-filter Find, same as the old TUI's browser.
import { store, showToast } from "../state";
import { api, errorMessage } from "../api";
import { formatIndexInfo } from "../format";
import { ResultGrid } from "../components/resultGrid";
import { infoDialog } from "../components/overlays";

export function mountMongoView(root: HTMLElement): { el: HTMLElement; onEnter: () => void } {
  const el = document.createElement("div");
  el.className = "sql-view";
  el.innerHTML = `
    <div class="tree-pane">
      <div class="tree-pane__header">Databases</div>
      <div class="tree" data-tree></div>
    </div>
    <div class="query-pane">
      <input class="target-field" type="text" placeholder="database.collection" />
      <textarea class="editor" spellcheck="false" placeholder='{"age": {"$gt": 30}}  (Cmd/Ctrl + Enter to run)'>{}</textarea>
      <div class="query-pane__toolbar">
        <button type="button" class="btn btn--primary" data-act="run">Run ⌘⏎</button>
        <button type="button" class="btn" data-act="export">Export JSONL</button>
      </div>
      <div class="result-slot"></div>
    </div>`;
  root.appendChild(el);

  const tree = el.querySelector<HTMLElement>("[data-tree]")!;
  const target = el.querySelector<HTMLInputElement>(".target-field")!;
  const editor = el.querySelector<HTMLTextAreaElement>(".editor")!;
  const grid = new ResultGrid();
  el.querySelector(".result-slot")!.appendChild(grid.el);

  const renderTree = () => {
    const { mongoDatabases, collectionsByDb, expandedMongoDbs } = store.state;
    tree.innerHTML = mongoDatabases
      .map((database) => {
        const open = expandedMongoDbs.has(database);
        const colls = collectionsByDb[database];
        return `
          <div class="tree__schema">
            <div class="tree__row" data-db="${attr(database)}" tabindex="0">
              <span class="tree__caret">${open ? "▾" : "▸"}</span>
              <span>${escapeHtml(database)}</span>
            </div>
            ${
              open
                ? `<div class="tree__children">
                    ${
                      colls
                        ? colls.map((c) => collRowHtml(database, c)).join("")
                        : `<div class="tree__loading">loading…</div>`
                    }
                   </div>`
                : ""
            }
          </div>`;
      })
      .join("");
  };

  async function loadCollections(database: string): Promise<void> {
    try {
      const colls = (await api.listCollections(database)) ?? [];
      store.set((s) => (s.collectionsByDb[database] = colls));
    } catch (err) {
      showToast(errorMessage(err));
    }
  }

  async function runFind(): Promise<void> {
    const [database, collection] = target.value.split(".");
    if (!database || !collection) {
      grid.showError("Set database.collection first.");
      return;
    }
    try {
      const res = await api.find(database, collection, editor.value || "{}");
      grid.setDocResult(res);
    } catch (err) {
      grid.showError(errorMessage(err));
    }
  }

  tree.addEventListener("click", async (e) => {
    const t = e.target as HTMLElement;
    const infoBtn = t.closest<HTMLElement>("[data-describe]");
    if (infoBtn) {
      e.stopPropagation();
      const [database, collection] = infoBtn.dataset.describe!.split(" ");
      try {
        const indexes = await api.indexInfo(database, collection);
        infoDialog(`${database}.${collection}`, formatIndexInfo(indexes));
      } catch (err) {
        showToast(errorMessage(err));
      }
      return;
    }
    const collRow = t.closest<HTMLElement>("[data-coll]");
    if (collRow) {
      const [database, collection] = collRow.dataset.coll!.split(" ");
      target.value = `${database}.${collection}`;
      await runFind();
      return;
    }
    const dbRow = t.closest<HTMLElement>("[data-db]");
    if (dbRow) {
      const database = dbRow.dataset.db!;
      const alreadyOpen = store.state.expandedMongoDbs.has(database);
      store.set((s) => {
        if (alreadyOpen) s.expandedMongoDbs.delete(database);
        else s.expandedMongoDbs.add(database);
      });
      if (!alreadyOpen && !store.state.collectionsByDb[database]) await loadCollections(database);
    }
  });

  editor.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      runFind();
    }
  });

  el.addEventListener("click", async (e) => {
    const act = (e.target as HTMLElement).dataset.act;
    if (act === "run") runFind();
    if (act === "export") {
      try {
        const path = await api.exportDocResultJSONL();
        showToast(`Exported to ${path}`, "info");
      } catch (err) {
        showToast(errorMessage(err));
      }
    }
  });

  store.subscribe(renderTree);
  store.subscribe(() => {
    if (store.state.pendingLoadQuery !== null && store.state.status.category === "document") {
      editor.value = store.state.pendingLoadQuery;
      store.state.pendingLoadQuery = null;
    }
  });

  const onEnter = () => {
    store.set((s) => {
      s.mongoDatabases = [];
      s.collectionsByDb = {};
      s.expandedMongoDbs = new Set();
    });
    grid.clear();
    api
      .listMongoDatabases()
      .then((dbs) => store.set((s) => (s.mongoDatabases = dbs ?? [])))
      .catch((err) => showToast(errorMessage(err)));
  };

  return { el, onEnter };
}

function collRowHtml(database: string, name: string): string {
  const key = `${database} ${name}`;
  return `
    <div class="tree__row tree__row--leaf" data-coll="${attr(key)}" tabindex="0">
      <span>${escapeHtml(name)}</span>
      <button type="button" class="btn btn--icon" data-describe="${attr(key)}" title="Indexes">ⓘ</button>
    </div>`;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function attr(s: string): string {
  return escapeHtml(s).replace(/"/g, "&quot;");
}
