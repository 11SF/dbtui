// KV mode (Redis): DB index switcher + key list (type/ttl) + a
// redis-cli-styled command input, gated by the destructive-command
// confirm dialog when RunKVCommand reports needsConfirm.
import { store, showToast } from "../state";
import { api, errorMessage } from "../api";
import { formatTTL, renderKVValue } from "../format";
import { ResultGrid } from "../components/resultGrid";
import { infoDialog, confirmDialog } from "../components/overlays";

export function mountKvView(root: HTMLElement): { el: HTMLElement; onEnter: () => void } {
  const el = document.createElement("div");
  el.className = "kv-view";
  el.innerHTML = `
    <div class="kv-view__toolbar">
      <label class="kv-view__db">
        DB
        <select data-db-select></select>
      </label>
      <input type="text" class="kv-view__pattern" placeholder="key pattern (default *)" />
      <button type="button" class="btn" data-act="refresh">Refresh</button>
    </div>
    <div class="kv-view__body">
      <div class="kv-keys" data-keys></div>
      <div class="query-pane">
        <div class="redis-input">
          <span class="redis-input__prompt" data-prompt></span>
          <input type="text" class="redis-input__field" spellcheck="false" placeholder="GET mykey" />
        </div>
        <div class="result-slot"></div>
      </div>
    </div>`;
  root.appendChild(el);

  const dbSelect = el.querySelector<HTMLSelectElement>("[data-db-select]")!;
  const patternInput = el.querySelector<HTMLInputElement>(".kv-view__pattern")!;
  const keysBox = el.querySelector<HTMLElement>("[data-keys]")!;
  const promptEl = el.querySelector<HTMLElement>("[data-prompt]")!;
  const cmdInput = el.querySelector<HTMLInputElement>(".redis-input__field")!;
  const grid = new ResultGrid();
  el.querySelector(".result-slot")!.appendChild(grid.el);

  const renderPrompt = () => {
    const s = store.state.status;
    promptEl.textContent = `${s.name}[db${store.state.redisDbIndex}]>`;
  };

  const renderDbSelect = () => {
    dbSelect.innerHTML = Array.from({ length: 16 }, (_, i) => i)
      .map((i) => `<option value="${i}" ${i === store.state.redisDbIndex ? "selected" : ""}>${i}</option>`)
      .join("");
  };

  const renderKeys = () => {
    const { redisKeys, redisKeyMeta } = store.state;
    keysBox.innerHTML = redisKeys.length
      ? redisKeys
          .map((k) => {
            const meta = redisKeyMeta[k];
            return `
              <div class="kv-keys__row" data-key="${attr(k)}" tabindex="0">
                <span class="kv-keys__type">${meta ? escapeHtml(meta.type) : "…"}</span>
                <span class="kv-keys__name">${escapeHtml(k)}</span>
                <span class="kv-keys__ttl">${meta ? formatTTL(meta.ttl) : ""}</span>
              </div>`;
          })
          .join("")
      : `<p class="result__empty">No keys${patternInput.value ? " matching that pattern" : ""}.</p>`;
  };

  async function refreshKeys(): Promise<void> {
    const dbIndex = store.state.redisDbIndex;
    try {
      const keys = (await api.scanKeys(dbIndex, patternInput.value.trim() || "*")) ?? [];
      store.set((s) => (s.redisKeys = keys));
      keys.forEach(async (k) => {
        try {
          const [type, ttl] = await Promise.all([api.keyType(dbIndex, k), api.ttlSeconds(dbIndex, k)]);
          store.set((s) => (s.redisKeyMeta[k] = { type, ttl }));
        } catch {
          /* per-key metadata is best-effort; the row just shows "…" */
        }
      });
    } catch (err) {
      showToast(errorMessage(err));
    }
  }

  dbSelect.addEventListener("change", () => {
    store.set((s) => {
      s.redisDbIndex = Number(dbSelect.value);
      s.redisKeys = [];
      s.redisKeyMeta = {};
    });
    refreshKeys();
  });

  el.querySelector('[data-act="refresh"]')!.addEventListener("click", refreshKeys);
  patternInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") refreshKeys();
  });

  keysBox.addEventListener("click", async (e) => {
    const row = (e.target as HTMLElement).closest<HTMLElement>("[data-key]");
    if (!row) return;
    const key = row.dataset.key!;
    try {
      const v = await api.getValue(store.state.redisDbIndex, key);
      infoDialog(`${key} (${v.Type})`, renderKVValue(v));
    } catch (err) {
      showToast(errorMessage(err));
    }
  });

  async function runCommand(confirmed: boolean): Promise<void> {
    const line = cmdInput.value.trim();
    if (!line) return;
    try {
      const res = await api.runKVCommand(store.state.redisDbIndex, line, confirmed);
      if (res.needsConfirm) {
        const ok = await confirmDialog(`Run destructive command "${line}"?`, "Run");
        if (ok) await runCommand(true);
        return;
      }
      grid.setRaw(JSON.stringify(res.result, null, 2) ?? "(nil)");
      cmdInput.value = "";
      refreshKeys();
    } catch (err) {
      grid.showError(errorMessage(err));
    }
  }

  cmdInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") runCommand(false);
  });

  store.subscribe(() => {
    renderPrompt();
    renderKeys();
    if (store.state.pendingLoadQuery !== null && store.state.status.category === "kv") {
      cmdInput.value = store.state.pendingLoadQuery;
      store.state.pendingLoadQuery = null;
    }
  });

  const onEnter = () => {
    store.set((s) => {
      s.redisDbIndex = 0;
      s.redisKeys = [];
      s.redisKeyMeta = {};
    });
    renderDbSelect();
    renderPrompt();
    grid.clear();
    refreshKeys();
  };

  return { el, onEnter };
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function attr(s: string): string {
  return escapeHtml(s).replace(/"/g, "&quot;");
}
