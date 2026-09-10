// Cmd/Ctrl+K palette — the GUI's descendant of the old TUI's ":" command
// line. Lists connect-to-X actions (one per saved connection), view
// switches, and connect/disconnect, filtered by substring match.
import { store, showToast } from "../state";
import { api, errorMessage } from "../api";
import { connectTo } from "./sidebar";
import type { ViewName } from "../state";

interface Action {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
}

function buildActions(): Action[] {
  const actions: Action[] = [];
  const { connections, status } = store.state;

  for (const c of connections) {
    actions.push({
      id: `connect:${c.Name}`,
      label: `Connect: ${c.Name}`,
      hint: c.Type,
      run: () => connectTo(c.Name),
    });
  }
  if (status.status !== "disconnected") {
    actions.push({
      id: "disconnect",
      label: `Disconnect: ${status.name}`,
      run: () => {
        api.disconnect().catch((err) => showToast(errorMessage(err)));
      },
    });
  }
  const views: [ViewName, string][] = [
    ["query", "Go to Query"],
    ["history", "Go to History"],
    ["logs", "Go to Logs"],
  ];
  for (const [v, label] of views) {
    actions.push({ id: `view:${v}`, label, run: () => store.set((s) => (s.activeView = v)) });
  }
  actions.push({
    id: "new-connection",
    label: "New connection",
    run: () => store.set((s) => (s.connectionFormOpen = { mode: "new" })),
  });
  return actions;
}

export function mountCommandPalette(root: HTMLElement): void {
  const overlay = document.createElement("div");
  overlay.className = "overlay overlay--palette";
  overlay.hidden = true;
  overlay.innerHTML = `
    <div class="palette" role="combobox" aria-expanded="true">
      <input class="palette__input" type="text" placeholder="Type a command or connection name…" />
      <div class="palette__list" role="listbox"></div>
    </div>`;
  root.appendChild(overlay);

  const input = overlay.querySelector<HTMLInputElement>(".palette__input")!;
  const list = overlay.querySelector<HTMLElement>(".palette__list")!;
  let filtered: Action[] = [];
  let cursor = 0;

  const renderList = () => {
    const q = input.value.trim().toLowerCase();
    const all = buildActions();
    filtered = q ? all.filter((a) => a.label.toLowerCase().includes(q)) : all;
    cursor = Math.min(cursor, Math.max(filtered.length - 1, 0));
    list.innerHTML = filtered
      .map(
        (a, i) =>
          `<div class="palette__item ${i === cursor ? "is-active" : ""}" data-index="${i}" role="option">
             <span>${escapeHtml(a.label)}</span>
             ${a.hint ? `<span class="palette__hint">${escapeHtml(a.hint)}</span>` : ""}
           </div>`
      )
      .join("") || `<div class="palette__empty">No matches</div>`;
  };

  const open = () => {
    overlay.hidden = false;
    input.value = "";
    cursor = 0;
    renderList();
    input.focus();
  };
  const close = () => {
    overlay.hidden = true;
    store.set((s) => (s.paletteOpen = false));
  };
  const run = (a: Action | undefined) => {
    if (!a) return;
    close();
    a.run();
  };

  overlay.addEventListener("click", (e) => {
    if (e.target === overlay) close();
    const item = (e.target as HTMLElement).closest<HTMLElement>(".palette__item");
    if (item) run(filtered[Number(item.dataset.index)]);
  });
  input.addEventListener("input", renderList);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") return close();
    if (e.key === "ArrowDown") {
      e.preventDefault();
      cursor = Math.min(cursor + 1, filtered.length - 1);
      renderList();
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      cursor = Math.max(cursor - 1, 0);
      renderList();
    }
    if (e.key === "Enter") {
      e.preventDefault();
      run(filtered[cursor]);
    }
  });

  store.subscribe(() => {
    if (store.state.paletteOpen && overlay.hidden) open();
    if (!store.state.paletteOpen && !overlay.hidden) overlay.hidden = true;
  });
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
