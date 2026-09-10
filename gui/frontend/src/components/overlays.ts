// Toast notifications + a generic yes/no confirm dialog. Both are
// singleton overlays mounted once by main.ts and driven by store state
// (toast) or direct imperative calls (confirm, which resolves a promise —
// no store round-trip needed for a one-shot yes/no).
import { store } from "../state";

export function mountToast(root: HTMLElement): void {
  const el = document.createElement("div");
  el.className = "toast";
  el.hidden = true;
  root.appendChild(el);

  store.subscribe(() => {
    const t = store.state.toast;
    if (!t) {
      el.hidden = true;
      return;
    }
    el.hidden = false;
    el.textContent = t.text;
    el.className = `toast toast--${t.kind}`;
  });
}

let confirmResolve: ((ok: boolean) => void) | null = null;
let confirmRoot: HTMLElement;

export function mountConfirmDialog(root: HTMLElement): void {
  confirmRoot = document.createElement("div");
  confirmRoot.className = "overlay";
  confirmRoot.hidden = true;
  confirmRoot.innerHTML = `
    <div class="dialog" role="alertdialog" aria-modal="true">
      <p class="dialog__text"></p>
      <div class="dialog__actions">
        <button type="button" class="btn" data-act="cancel">Cancel</button>
        <button type="button" class="btn btn--danger" data-act="confirm">Confirm</button>
      </div>
    </div>`;
  root.appendChild(confirmRoot);

  confirmRoot.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    if (target === confirmRoot) return settle(false);
    const act = target.dataset.act;
    if (act === "confirm") settle(true);
    if (act === "cancel") settle(false);
  });
  document.addEventListener("keydown", (e) => {
    if (confirmRoot.hidden) return;
    if (e.key === "Escape") settle(false);
    if (e.key === "Enter") settle(true);
  });
}

function settle(ok: boolean): void {
  confirmRoot.hidden = true;
  const resolve = confirmResolve;
  confirmResolve = null;
  resolve?.(ok);
}

/** Shows the confirm dialog with the given message; resolves true/false. */
export function confirmDialog(message: string, confirmLabel = "Confirm"): Promise<boolean> {
  const text = confirmRoot.querySelector(".dialog__text")!;
  const btn = confirmRoot.querySelector<HTMLButtonElement>('[data-act="confirm"]')!;
  text.textContent = message;
  btn.textContent = confirmLabel;
  confirmRoot.hidden = false;
  btn.focus();
  return new Promise((resolve) => {
    confirmResolve = resolve;
  });
}

let infoRoot: HTMLElement;

export function mountInfoDialog(root: HTMLElement): void {
  infoRoot = document.createElement("div");
  infoRoot.className = "overlay";
  infoRoot.hidden = true;
  infoRoot.innerHTML = `
    <div class="dialog dialog--info" role="dialog" aria-modal="true">
      <div class="dialog__header">
        <h2 class="dialog__title"></h2>
        <button type="button" class="btn btn--icon" data-act="close">✕</button>
      </div>
      <pre class="dialog__body"></pre>
    </div>`;
  root.appendChild(infoRoot);
  infoRoot.addEventListener("click", (e) => {
    const target = e.target as HTMLElement;
    if (target === infoRoot || target.dataset.act === "close") infoRoot.hidden = true;
  });
  document.addEventListener("keydown", (e) => {
    if (!infoRoot.hidden && e.key === "Escape") infoRoot.hidden = true;
  });
}

/** Shows a read-only titled text panel (used for table/index describe). */
export function infoDialog(title: string, bodyText: string): void {
  infoRoot.querySelector(".dialog__title")!.textContent = title;
  infoRoot.querySelector(".dialog__body")!.textContent = bodyText;
  infoRoot.hidden = false;
}
