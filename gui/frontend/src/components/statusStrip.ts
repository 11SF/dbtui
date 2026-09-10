// The app's top strip — carried forward from the original TUI's status
// bar, deliberately not a logo+nav header. Shows the active connection's
// live StatusPayload and doubles as the disconnect control.
import { store } from "../state";
import { api, errorMessage } from "../api";
import { showToast } from "../state";
import { statusColorVar, statusLabel } from "../format";

export function mountStatusStrip(root: HTMLElement): void {
  const el = document.createElement("div");
  el.className = "status-strip";
  root.appendChild(el);

  const render = () => {
    const s = store.state.status;
    const connected = s.status !== "disconnected";
    el.innerHTML = `
      <span class="status-strip__dot" style="background:${statusColorVar(s.status)}"></span>
      ${
        connected
          ? `<span class="status-strip__name">${escapeAttr(s.name)}</span>
             <span class="status-strip__type">${escapeAttr(s.type)}</span>
             <span class="status-strip__label" style="color:${statusColorVar(s.status)}">${statusLabel(s.status)}</span>
             ${s.tunnelLocalPort ? `<span class="status-strip__tunnel">tunnel · localhost:${s.tunnelLocalPort}</span>` : ""}
             ${s.error ? `<span class="status-strip__error">${escapeAttr(s.error)}</span>` : ""}`
          : `<span class="status-strip__label">no connection</span>`
      }
      <span class="status-strip__spacer"></span>
      ${connected ? `<button type="button" class="btn btn--ghost" data-act="disconnect">Disconnect</button>` : ""}
      <button type="button" class="btn btn--ghost" data-act="palette">⌘K</button>
    `;
  };

  el.addEventListener("click", async (e) => {
    const act = (e.target as HTMLElement).dataset.act;
    if (act === "disconnect") {
      try {
        await api.disconnect();
      } catch (err) {
        showToast(errorMessage(err));
      }
    }
    if (act === "palette") {
      store.set((s) => (s.paletteOpen = true));
    }
  });

  store.subscribe(render);
  render();
}

function escapeAttr(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
