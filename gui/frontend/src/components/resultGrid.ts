// Shared result renderer — the GUI's counterpart to the old TUI's
// ResultView: one component that renders either a tabular QueryResult, a
// DocResult (divider-separated pretty JSON), or raw text (Redis command
// replies), with a shared footer and copy-to-clipboard interaction.
import type { QueryResult, DocResult } from "../api";
import { formatCell, formatDuration } from "../format";
import { showToast } from "../state";

const DOC_DIVIDER = "\n---\n";

type SortDir = "asc" | "desc";

/** Lets the caller (sqlView, which alone knows whether the current result
 * maps 1:1 onto a single real table) turn a cell edit into a persisted
 * UPDATE. `row` is the live row array — ResultGrid mutates row[colIndex]
 * in place once commit() resolves, so sort order and later edits stay
 * consistent with what was actually saved. Reject to leave the cell in
 * edit mode with the typed value intact, so the user can fix and retry. */
export interface EditContext {
  commit(row: any[], columns: string[], colIndex: number, newValue: string | null): Promise<void>;
}

export class ResultGrid {
  readonly el: HTMLElement;
  private body: HTMLElement;
  private footer: HTMLElement;
  private currentResult: QueryResult | null = null;
  private sortColumn: number | null = null;
  private sortDir: SortDir = "asc";
  private editContext: EditContext | null = null;
  private displayedRows: any[][] = [];
  private activeEdit: HTMLElement | null = null;

  constructor() {
    this.el = document.createElement("div");
    this.el.className = "result";
    this.el.innerHTML = `<div class="result__body"></div><div class="result__footer"></div>`;
    this.body = this.el.querySelector(".result__body")!;
    this.footer = this.el.querySelector(".result__footer")!;
    this.clear();

    this.body.addEventListener("click", (e) => {
      if ((e.target as HTMLElement).closest(".cell-edit")) return;
      const th = (e.target as HTMLElement).closest<HTMLElement>("[data-col]");
      if (th) {
        this.toggleSort(Number(th.dataset.col));
        return;
      }
      const cell = (e.target as HTMLElement).closest<HTMLElement>("[data-cell]");
      if (!cell) return;
      copy(cell.dataset.cell!, "Copied cell");
    });
    this.body.addEventListener("dblclick", (e) => {
      const cell = (e.target as HTMLElement).closest<HTMLElement>("[data-cell]");
      if (cell && this.editContext && !this.activeEdit) {
        this.beginEdit(cell);
        return;
      }
      const row = (e.target as HTMLElement).closest<HTMLElement>("[data-row]");
      if (!row) return;
      copy(row.dataset.row!, "Copied row");
    });
  }

  clear(): void {
    this.currentResult = null;
    this.sortColumn = null;
    this.editContext = null;
    this.activeEdit = null;
    this.body.innerHTML = `<p class="result__empty">Run a query to see results here.</p>`;
    this.footer.textContent = "";
  }

  showError(message: string): void {
    this.currentResult = null;
    this.sortColumn = null;
    this.editContext = null;
    this.activeEdit = null;
    this.body.innerHTML = `<p class="result__error">${escapeHtml(message)}</p>`;
    this.footer.textContent = "";
  }

  /** editable is null when the result can't be safely mapped back onto a
   * single table (a join, an aggregate, a plain history/typed query) — the
   * grid then behaves exactly as before (dblclick copies the row). */
  setResult(res: QueryResult, editable: EditContext | null = null): void {
    this.currentResult = res;
    this.sortColumn = null;
    this.sortDir = "asc";
    this.editContext = editable;
    this.renderResult(res);
  }

  private toggleSort(col: number): void {
    if (!this.currentResult) return;
    this.sortDir = this.sortColumn === col && this.sortDir === "asc" ? "desc" : "asc";
    this.sortColumn = col;
    this.renderResult(this.currentResult);
  }

  private renderResult(res: QueryResult): void {
    this.activeEdit = null;
    // A Go nil slice (zero rows, or — less commonly — zero columns) marshals
    // to JSON `null`, not `[]`; guard every array field from the bridge.
    const columns = res.Columns ?? [];
    const rows = res.Rows ?? [];
    if (rows.length === 0) {
      this.displayedRows = [];
      this.body.innerHTML = `<p class="result__empty">Query succeeded — 0 rows.</p>`;
    } else {
      this.displayedRows = this.sortColumn === null ? rows : sortRows(rows, this.sortColumn, this.sortDir);
      const head = columns.map((c, i) => {
        const arrow = this.sortColumn === i ? (this.sortDir === "asc" ? " ▲" : " ▼") : "";
        return `<th data-col="${i}">${escapeHtml(c)}${arrow}</th>`;
      }).join("");
      const editableCls = this.editContext ? " is-editable" : "";
      const rowsHtml = this.displayedRows.map((row, ri) => {
        const tsv = row.map(formatCell).join("\t");
        const cells = row
          .map((v, ci) => {
            const text = formatCell(v);
            return `<td data-cell="${attr(text)}" data-row-idx="${ri}" data-col-idx="${ci}" class="${editableCls}">${escapeHtml(text)}</td>`;
          })
          .join("");
        return `<tr data-row="${attr(tsv)}">${cells}</tr>`;
      }).join("");
      this.body.innerHTML = `<table class="result__table"><thead><tr>${head}</tr></thead><tbody>${rowsHtml}</tbody></table>`;
    }
    const capNote = res.Capped ? " (capped at 1000)" : "";
    const editHint = this.editContext ? ", double-click a cell to edit" : "";
    this.footer.textContent = `${rows.length} rows · ${formatDuration(res.Duration / 1e6)}${capNote} · click a cell to copy, double-click a row, click a header to sort${editHint}`;
  }

  private beginEdit(td: HTMLElement): void {
    const editable = this.editContext;
    if (!editable || !this.currentResult) return;
    const ri = Number(td.dataset.rowIdx);
    const ci = Number(td.dataset.colIdx);
    const row = this.displayedRows[ri];
    if (!row) return;
    const columns = this.currentResult.Columns ?? [];

    this.activeEdit = td;
    td.classList.add("is-editing");
    const raw = row[ci];
    // originalDisplay is what the cell shows ("NULL" for a real null); the
    // input itself can't distinguish null from an actual empty string, so
    // it starts blank for both — that ambiguity only matters on submit
    // (blank commits as NULL either way), never on cancel/no-op, which
    // must restore the exact text that was there before.
    const originalDisplay = formatCell(raw);
    const originalInput = raw === null || raw === undefined ? "" : originalDisplay;
    td.innerHTML = `<input class="cell-edit" type="text" spellcheck="false" />`;
    const input = td.querySelector<HTMLInputElement>(".cell-edit")!;
    input.value = originalInput;
    input.focus();
    input.select();

    let settled = false;
    const exit = (displayText: string) => {
      td.classList.remove("is-editing");
      td.dataset.cell = displayText;
      td.textContent = displayText;
      this.activeEdit = null;
    };
    const cancel = () => {
      if (settled) return;
      settled = true;
      exit(originalDisplay);
    };
    const submit = async () => {
      if (settled) return;
      if (input.value === originalInput) {
        settled = true;
        exit(originalDisplay);
        return;
      }
      settled = true;
      const newValue: string | null = input.value === "" ? null : input.value;
      input.disabled = true;
      try {
        await editable.commit(row, columns, ci, newValue);
        row[ci] = newValue;
        exit(formatCell(newValue));
        showToast("Saved", "info");
      } catch (err) {
        settled = false;
        input.disabled = false;
        input.focus();
        showToast(err instanceof Error ? err.message : String(err));
      }
    };

    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        submit();
      } else if (e.key === "Escape") {
        e.preventDefault();
        cancel();
      }
    });
    input.addEventListener("blur", () => submit());
  }

  setDocResult(res: DocResult, limit = 100): void {
    this.editContext = null;
    this.activeEdit = null;
    const documents = res.Documents ?? [];
    if (documents.length === 0) {
      this.body.innerHTML = `<p class="result__empty">Query succeeded — 0 documents.</p>`;
    } else {
      this.body.innerHTML = `<pre class="result__docs">${escapeHtml(documents.join(DOC_DIVIDER))}</pre>`;
    }
    const capNote = documents.length >= limit ? ` (capped at ${limit})` : "";
    this.footer.textContent = `${documents.length} docs · ${formatDuration(res.Duration / 1e6)}${capNote}`;
  }

  setRaw(text: string): void {
    this.editContext = null;
    this.activeEdit = null;
    this.body.innerHTML = `<pre class="result__docs" data-cell="${attr(text)}">${escapeHtml(text)}</pre>`;
    this.footer.textContent = "click to copy";
  }
}

/** Nulls sort last regardless of direction; numeric values compare
 * numerically, everything else falls back to string comparison. */
function sortRows(rows: any[][], col: number, dir: SortDir): any[][] {
  const mult = dir === "asc" ? 1 : -1;
  return rows.slice().sort((a, b) => {
    const av = a[col];
    const bv = b[col];
    const aNull = av === null || av === undefined;
    const bNull = bv === null || bv === undefined;
    if (aNull && bNull) return 0;
    if (aNull) return 1;
    if (bNull) return -1;
    return mult * compareValues(av, bv);
  });
}

function compareValues(a: unknown, b: unknown): number {
  if (typeof a === "number" && typeof b === "number") return a - b;
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: "base" });
}

function copy(text: string, message: string): void {
  navigator.clipboard
    .writeText(text)
    .then(() => showToast(message, "info"))
    .catch(() => showToast("Copy failed — clipboard unavailable"));
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function attr(s: string): string {
  return escapeHtml(s).replace(/"/g, "&quot;");
}
