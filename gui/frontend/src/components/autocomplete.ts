// Lightweight inline suggestion popup for a plain <textarea> or single-line
// <input> — no code-editor dependency. Filters a caller-supplied candidate
// list by the identifier currently under the caret and inserts the pick in
// place, Cmd/Ctrl+K palette-style (arrow keys + Enter/Tab to accept, Escape
// to dismiss).
export type TextInput = HTMLTextAreaElement | HTMLInputElement;

export interface Suggestion {
  label: string;
  detail?: string;
}

export interface SuggestContext {
  fullText: string;
  word: string;
}

export type SuggestFn = (ctx: SuggestContext) => Suggestion[];

const WORD_RE = /[\w.]/;

export interface AutocompleteHandle {
  /** Re-runs suggest() against the current word, but only while the popup
   * is already open — for callers whose candidate list loads async (e.g.
   * a lazily-fetched table's columns) and want the visible list to pick
   * it up without the user retyping. */
  refresh(): void;
}

export function attachAutocomplete(textarea: TextInput, suggest: SuggestFn): AutocompleteHandle {
  const box = document.createElement("div");
  box.className = "autocomplete";
  box.hidden = true;
  document.body.appendChild(box);

  let items: Suggestion[] = [];
  let cursor = 0;
  let wordStart = 0;

  function currentWord(): { start: number; word: string } {
    const pos = textarea.selectionStart ?? textarea.value.length;
    const text = textarea.value;
    let start = pos;
    while (start > 0 && WORD_RE.test(text[start - 1])) start--;
    return { start, word: text.slice(start, pos) };
  }

  function close(): void {
    if (box.hidden) return;
    box.hidden = true;
    items = [];
  }

  function position(): void {
    const coords = caretCoordinates(textarea, wordStart);
    const rect = textarea.getBoundingClientRect();
    box.style.left = `${rect.left + coords.left}px`;
    box.style.top = `${rect.top + coords.top + coords.height}px`;
  }

  function render(): void {
    if (items.length === 0) {
      close();
      return;
    }
    box.hidden = false;
    box.innerHTML = items
      .map(
        (it, i) =>
          `<div class="autocomplete__item ${i === cursor ? "is-active" : ""}" data-index="${i}">
             <span>${escapeHtml(it.label)}</span>
             ${it.detail ? `<span class="autocomplete__detail">${escapeHtml(it.detail)}</span>` : ""}
           </div>`
      )
      .join("");
    position();
  }

  function open(opts: { force?: boolean } = {}): void {
    const { start, word } = currentWord();
    if (!opts.force && word.length === 0) {
      close();
      return;
    }
    wordStart = start;
    const q = word.toLowerCase();
    items = suggest({ fullText: textarea.value, word })
      .filter((it) => it.label.toLowerCase() !== q && it.label.toLowerCase().startsWith(q))
      .slice(0, 20);
    cursor = 0;
    render();
  }

  function accept(item: Suggestion | undefined): void {
    if (!item) return;
    const text = textarea.value;
    const pos = textarea.selectionStart ?? text.length;
    const before = text.slice(0, wordStart);
    const after = text.slice(pos);
    textarea.value = before + item.label + after;
    const newPos = before.length + item.label.length;
    textarea.selectionStart = textarea.selectionEnd = newPos;
    close();
  }

  textarea.addEventListener("input", () => open());
  textarea.addEventListener("click", close);
  textarea.addEventListener("blur", () => setTimeout(close, 150));
  textarea.addEventListener("scroll", () => {
    if (!box.hidden) position();
  });

  textarea.addEventListener("keydown", (evt: Event) => {
    const e = evt as KeyboardEvent;
    if (e.key === " " && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      open({ force: true });
      return;
    }
    if (box.hidden) return;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        cursor = Math.min(cursor + 1, items.length - 1);
        render();
        break;
      case "ArrowUp":
        e.preventDefault();
        cursor = Math.max(cursor - 1, 0);
        render();
        break;
      case "Enter":
      case "Tab":
        e.preventDefault();
        accept(items[cursor]);
        break;
      case "Escape":
        e.preventDefault();
        close();
        break;
      case "ArrowLeft":
      case "ArrowRight":
      case "Home":
      case "End":
        close();
        break;
    }
  });

  box.addEventListener("mousedown", (e) => {
    e.preventDefault();
    const el = (e.target as HTMLElement).closest<HTMLElement>("[data-index]");
    if (el) accept(items[Number(el.dataset.index)]);
  });

  window.addEventListener("resize", () => {
    if (!box.hidden) position();
  });

  return {
    refresh(): void {
      if (!box.hidden) open();
    },
  };
}

/** Pixel offset of a text position inside a textarea/input, measured with a
 * hidden mirror element that copies its box model, font and wrapping — the
 * standard technique since neither element exposes a native caret-rect API. */
function caretCoordinates(el: TextInput, position: number): { left: number; top: number; height: number } {
  const div = document.createElement("div");
  const style = getComputedStyle(el);
  const isInput = el.tagName === "INPUT";
  const props = [
    "boxSizing",
    "width",
    "paddingTop",
    "paddingRight",
    "paddingBottom",
    "paddingLeft",
    "borderTopWidth",
    "borderRightWidth",
    "borderBottomWidth",
    "borderLeftWidth",
    "fontStyle",
    "fontVariant",
    "fontWeight",
    "fontSize",
    "lineHeight",
    "fontFamily",
    "textAlign",
    "textTransform",
    "textIndent",
    "letterSpacing",
    "wordSpacing",
    "tabSize",
  ] as const;

  div.style.position = "absolute";
  div.style.visibility = "hidden";
  // A single-line input never wraps (text just scrolls), so its mirror
  // must not wrap either — width-based wrapping is a textarea-only concern.
  div.style.whiteSpace = isInput ? "pre" : "pre-wrap";
  div.style.wordWrap = isInput ? "normal" : "break-word";
  div.style.top = "0";
  div.style.left = "-9999px";
  for (const prop of props) {
    div.style[prop] = style[prop];
  }
  if (!isInput) div.style.width = style.width;
  document.body.appendChild(div);

  div.textContent = el.value.substring(0, position);
  const span = document.createElement("span");
  span.textContent = el.value.substring(position) || ".";
  div.appendChild(span);

  const coords = {
    left: span.offsetLeft - el.scrollLeft,
    top: span.offsetTop - el.scrollTop,
    height: parseInt(style.lineHeight, 10) || span.offsetHeight,
  };
  document.body.removeChild(div);
  return coords;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
