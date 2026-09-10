// Shown in the main content area when nothing is connected.
import { store } from "../state";

export function mountEmptyState(root: HTMLElement): { el: HTMLElement; update: () => void } {
  const el = document.createElement("div");
  el.className = "empty-state";
  root.appendChild(el);

  const update = () => {
    const has = store.state.connections.length > 0;
    el.innerHTML = has
      ? `<p>Pick a connection on the left, or press <kbd>⌘K</kbd> to connect.</p>`
      : `<p>No connections yet. Press <kbd>+</kbd> in the sidebar to add one.</p>`;
  };
  store.subscribe(update);
  update();
  return { el, update };
}
