import { api, errorMessage } from "../api";
import { showToast } from "../state";

const POLL_MS = 2000;

export function mountLogsView(root: HTMLElement): { el: HTMLElement; onEnter: () => void; onLeave: () => void } {
  const el = document.createElement("div");
  el.className = "logs-view";
  el.innerHTML = `<pre class="logs-view__body" data-body></pre>`;
  root.appendChild(el);

  const body = el.querySelector<HTMLElement>("[data-body]")!;
  let timer: ReturnType<typeof setInterval> | undefined;

  async function refresh(): Promise<void> {
    try {
      const lines = (await api.getLogs()) ?? [];
      const nearBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 40;
      body.textContent = lines.length ? lines.join("\n") : "(no log lines yet)";
      if (nearBottom) body.scrollTop = body.scrollHeight;
    } catch (err) {
      showToast(errorMessage(err));
    }
  }

  const onEnter = () => {
    refresh();
    timer = setInterval(refresh, POLL_MS);
  };
  const onLeave = () => {
    clearInterval(timer);
  };

  return { el, onEnter, onLeave };
}
