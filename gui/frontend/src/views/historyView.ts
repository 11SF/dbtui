import { store, showToast } from "../state";
import { api, errorMessage, type HistoryEntry } from "../api";
import { truncate, formatTimestamp } from "../format";

export function mountHistoryView(root: HTMLElement): { el: HTMLElement; onEnter: () => void } {
  const el = document.createElement("div");
  el.className = "history-view";
  el.innerHTML = `
    <div class="history-view__toolbar">
      <input type="text" class="history-view__filter" placeholder="Filter by connection name…" />
    </div>
    <div class="history-view__list" data-list></div>`;
  root.appendChild(el);

  const filterInput = el.querySelector<HTMLInputElement>(".history-view__filter")!;
  const list = el.querySelector<HTMLElement>("[data-list]")!;
  let entries: HistoryEntry[] = [];

  const render = () => {
    if (entries.length === 0) {
      list.innerHTML = `<p class="result__empty">No history yet.</p>`;
      return;
    }
    list.innerHTML = `
      <table class="result__table history-table">
        <thead><tr><th>Time</th><th>Connection</th><th>Duration</th><th>Rows</th><th>Query</th></tr></thead>
        <tbody>
          ${entries
            .map(
              (e, i) => `
              <tr data-index="${i}" class="${e.Error ? "history-row--error" : ""}" tabindex="0">
                <td>${escapeHtml(formatTimestamp(e.Timestamp))}</td>
                <td>${escapeHtml(e.ConnName)}</td>
                <td>${e.DurationMs}ms</td>
                <td>${e.Error ? "—" : e.RowCount}</td>
                <td class="history-table__query">${escapeHtml(truncate(e.Query, 80))}${e.Error ? ` <span class="history-row__err">${escapeHtml(e.Error)}</span>` : ""}</td>
              </tr>`
            )
            .join("")}
        </tbody>
      </table>`;
  };

  async function load(): Promise<void> {
    try {
      entries = await api.getHistory(200, filterInput.value.trim());
      render();
    } catch (err) {
      showToast(errorMessage(err));
    }
  }

  filterInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") load();
  });
  filterInput.addEventListener("blur", load);

  list.addEventListener("click", (e) => {
    const row = (e.target as HTMLElement).closest<HTMLElement>("tr[data-index]");
    if (!row) return;
    const entry = entries[Number(row.dataset.index)];
    if (!entry) return;
    store.set((s) => {
      s.pendingLoadQuery = entry.Query;
      s.activeView = "query";
    });
  });
  list.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    const row = (e.target as HTMLElement).closest<HTMLElement>("tr[data-index]");
    row?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });

  return { el, onEnter: load };
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
