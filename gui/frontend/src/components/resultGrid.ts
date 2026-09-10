// Shared result renderer — the GUI's counterpart to the old TUI's
// ResultView: one component that renders either a tabular QueryResult, a
// DocResult (divider-separated pretty JSON), or raw text (Redis command
// replies), with a shared footer and copy-to-clipboard interaction.
import type { QueryResult, DocResult } from "../api";
import { formatCell, formatDuration } from "../format";
import { showToast } from "../state";

const DOC_DIVIDER = "\n---\n";

export class ResultGrid {
  readonly el: HTMLElement;
  private body: HTMLElement;
  private footer: HTMLElement;

  constructor() {
    this.el = document.createElement("div");
    this.el.className = "result";
    this.el.innerHTML = `<div class="result__body"></div><div class="result__footer"></div>`;
    this.body = this.el.querySelector(".result__body")!;
    this.footer = this.el.querySelector(".result__footer")!;
    this.clear();

    this.body.addEventListener("click", (e) => {
      const cell = (e.target as HTMLElement).closest<HTMLElement>("[data-cell]");
      if (!cell) return;
      copy(cell.dataset.cell!, "Copied cell");
    });
    this.body.addEventListener("dblclick", (e) => {
      const row = (e.target as HTMLElement).closest<HTMLElement>("[data-row]");
      if (!row) return;
      copy(row.dataset.row!, "Copied row");
    });
  }

  clear(): void {
    this.body.innerHTML = `<p class="result__empty">Run a query to see results here.</p>`;
    this.footer.textContent = "";
  }

  showError(message: string): void {
    this.body.innerHTML = `<p class="result__error">${escapeHtml(message)}</p>`;
    this.footer.textContent = "";
  }

  setResult(res: QueryResult): void {
    // A Go nil slice (zero rows, or — less commonly — zero columns) marshals
    // to JSON `null`, not `[]`; guard every array field from the bridge.
    const columns = res.Columns ?? [];
    const rows = res.Rows ?? [];
    if (rows.length === 0) {
      this.body.innerHTML = `<p class="result__empty">Query succeeded — 0 rows.</p>`;
    } else {
      const head = columns.map((c) => `<th>${escapeHtml(c)}</th>`).join("");
      const rowsHtml = rows.map((row) => {
        const tsv = row.map(formatCell).join("\t");
        const cells = row.map((v) => `<td data-cell="${attr(formatCell(v))}">${escapeHtml(formatCell(v))}</td>`).join("");
        return `<tr data-row="${attr(tsv)}">${cells}</tr>`;
      }).join("");
      this.body.innerHTML = `<table class="result__table"><thead><tr>${head}</tr></thead><tbody>${rowsHtml}</tbody></table>`;
    }
    const capNote = res.Capped ? " (capped at 1000)" : "";
    this.footer.textContent = `${rows.length} rows · ${formatDuration(res.Duration / 1e6)}${capNote} · click a cell to copy, double-click a row`;
  }

  setDocResult(res: DocResult, limit = 100): void {
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
    this.body.innerHTML = `<pre class="result__docs" data-cell="${attr(text)}">${escapeHtml(text)}</pre>`;
    this.footer.textContent = "click to copy";
  }
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
