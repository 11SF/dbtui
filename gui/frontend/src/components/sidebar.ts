// Left rail: saved connections. Click/Enter connects; hover reveals
// edit/delete; "+" opens the new-connection form.
import { store, showToast } from "../state";
import { api, errorMessage, type Connection } from "../api";
import { statusColorVar } from "../format";
import { confirmDialog } from "./overlays";

export function mountSidebar(root: HTMLElement): void {
  const el = document.createElement("div");
  el.className = "sidebar";
  root.appendChild(el);

  const render = () => {
    const { connections, status } = store.state;
    el.innerHTML = `
      <div class="sidebar__header">
        <span class="sidebar__title">Connections</span>
        <button type="button" class="btn btn--icon" data-act="new" title="New connection (n)">+</button>
      </div>
      <div class="sidebar__list" role="listbox">
        ${
          connections.length === 0
            ? `<p class="sidebar__empty">No connections yet. Press + to add one.</p>`
            : connections
                .map((c) => rowHtml(c, status.name === c.Name ? status.status : "disconnected"))
                .join("")
        }
      </div>
    `;
  };

  el.addEventListener("click", async (e) => {
    const target = e.target as HTMLElement;
    const row = target.closest<HTMLElement>("[data-name]");
    const act = target.dataset.act;

    if (act === "new") {
      store.set((s) => (s.connectionFormOpen = { mode: "new" }));
      return;
    }
    if (!row) return;
    const name = row.dataset.name!;

    if (act === "edit") {
      const conn = store.state.connections.find((c) => c.Name === name);
      if (conn) store.set((s) => (s.connectionFormOpen = { mode: "edit", original: conn }));
      return;
    }
    if (act === "delete") {
      const ok = await confirmDialog(`Delete connection "${name}"? This also removes its saved password.`, "Delete");
      if (!ok) return;
      try {
        await api.deleteConnection(name);
        const conns = await api.listConnections();
        store.set((s) => (s.connections = conns));
      } catch (err) {
        showToast(errorMessage(err));
      }
      return;
    }
    // Row body clicked (or Enter): connect.
    connectTo(name);
  });

  el.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    const row = (e.target as HTMLElement).closest<HTMLElement>("[data-name]");
    if (row) connectTo(row.dataset.name!);
  });

  store.subscribe(render);
  render();
}

export async function connectTo(name: string): Promise<void> {
  // The backend supports one active connection at a time and doesn't
  // disconnect the previous one on a fresh Connect() call (same "single
  // active connection" contract the original TUI had) — a mouse-driven
  // sidebar makes it much easier to click a second connection by accident
  // than the old keyboard-only TUI ever did, so guard it here rather than
  // silently orphaning the first connection's client/tunnel.
  const current = store.state.status;
  if (current.status !== "disconnected" && current.name !== name) {
    try {
      await api.disconnect();
    } catch (err) {
      showToast(errorMessage(err));
      return;
    }
  }
  try {
    await api.connect(name);
  } catch (err) {
    showToast(errorMessage(err));
  }
}

function rowHtml(c: Connection, status: string): string {
  return `
    <div class="sidebar__row" data-name="${attr(c.Name)}" tabindex="0" role="option">
      <span class="sidebar__dot" style="background:${statusColorVar(status)}"></span>
      <span class="sidebar__name">${attr(c.Name)}</span>
      <span class="sidebar__type">${attr(c.Type)}</span>
      <span class="sidebar__row-actions">
        <button type="button" class="btn btn--icon" data-act="edit" title="Edit">✎</button>
        <button type="button" class="btn btn--icon" data-act="delete" title="Delete">✕</button>
      </span>
    </div>`;
}

function attr(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}
