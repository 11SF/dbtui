// New/edit connection modal. Field visibility depends on Type, exactly
// like the old TUI's visibleFields table. Password is write-only: never
// pre-filled on edit (the backend never sends one back), left blank means
// "keep what's already stored" (SaveConnection's own contract).
import { store, showToast } from "../state";
import { api, errorMessage, type Connection } from "../api";

type FieldsFor = "postgres" | "mysql" | "redis" | "mongodb";

const FIELD_SETS: Record<FieldsFor, string[]> = {
  postgres: ["Host", "Port", "User", "Password", "DBName"],
  mysql: ["Host", "Port", "User", "Password", "DBName"],
  redis: ["Host", "Port", "Password"],
  mongodb: ["Host", "Port", "User", "Password", "DBName", "AuthSource"],
};

const DEFAULT_PORT: Record<FieldsFor, number> = { postgres: 5432, mysql: 3306, redis: 6379, mongodb: 27017 };

export function visibleFields(t: string): string[] {
  return FIELD_SETS[t as FieldsFor] ?? FIELD_SETS.postgres;
}

export function mountConnectionForm(root: HTMLElement): void {
  const overlay = document.createElement("div");
  overlay.className = "overlay";
  overlay.hidden = true;
  root.appendChild(overlay);

  const render = () => {
    const open = store.state.connectionFormOpen;
    if (!open) {
      overlay.hidden = true;
      overlay.innerHTML = "";
      return;
    }
    overlay.hidden = false;
    const existing = open.mode === "edit" ? open.original : blankConnection();
    overlay.innerHTML = formHtml(existing, open.mode);
    wireForm(overlay, open.mode, existing);
  };

  overlay.addEventListener("click", (e) => {
    if (e.target === overlay) store.set((s) => (s.connectionFormOpen = null));
  });
  document.addEventListener("keydown", (e) => {
    if (!store.state.connectionFormOpen) return;
    if (e.key === "Escape") store.set((s) => (s.connectionFormOpen = null));
  });

  store.subscribe(render);
  render();
}

function blankConnection(): Connection {
  return {
    Name: "",
    Type: "postgres",
    Host: "",
    Port: 5432,
    User: "",
    DBName: "",
    AuthSource: "",
    Tunnel: undefined,
    Group: "",
    QueryTimeoutSec: 0,
  } as Connection;
}

function formHtml(c: Connection, mode: "new" | "edit"): string {
  const fields = visibleFields(c.Type);
  const hasTunnel = !!c.Tunnel;
  return `
    <form class="dialog dialog--form" id="conn-form">
      <h2 class="dialog__title">${mode === "new" ? "New connection" : "Edit connection"}</h2>
      <p class="form-error" hidden></p>

      <label class="field">
        <span>Name</span>
        <input name="Name" required value="${attr(c.Name)}" ${mode === "edit" ? "" : "autofocus"} />
      </label>

      <label class="field">
        <span>Type</span>
        <select name="Type">
          ${(["postgres", "mysql", "redis", "mongodb"] as const)
            .map((t) => `<option value="${t}" ${c.Type === t ? "selected" : ""}>${t}</option>`)
            .join("")}
        </select>
      </label>

      <div class="field-grid" data-fields>
        ${fields.includes("Host") ? textField("Host", c.Host, "text") : ""}
        ${fields.includes("Port") ? textField("Port", String(c.Port || ""), "number") : ""}
        ${fields.includes("User") ? textField("User", c.User, "text") : ""}
        ${fields.includes("Password") ? textField("Password", "", "password", mode === "edit" ? "leave blank to keep current" : "") : ""}
        ${fields.includes("DBName") ? textField("DBName", c.DBName, "text") : ""}
        ${fields.includes("AuthSource") ? textField("AuthSource", c.AuthSource, "text", "default: admin") : ""}
      </div>

      <label class="field">
        <span>Group</span>
        <input name="Group" value="${attr(c.Group)}" placeholder="optional, e.g. staging" />
      </label>

      <label class="field field--checkbox">
        <input type="checkbox" name="useTunnel" ${hasTunnel ? "checked" : ""} />
        <span>Connect through a kubectl tunnel</span>
      </label>

      <div class="field-grid" data-tunnel-fields ${hasTunnel ? "" : "hidden"}>
        ${textField("KubeContext", c.Tunnel?.KubeContext ?? "", "text")}
        ${textField("Namespace", c.Tunnel?.Namespace ?? "", "text")}
        <label class="field">
          <span>TargetType</span>
          <select name="TargetType">
            <option value="pod" ${c.Tunnel?.TargetType === "pod" ? "selected" : ""}>pod</option>
            <option value="svc" ${c.Tunnel?.TargetType === "svc" ? "selected" : ""}>svc</option>
          </select>
        </label>
        ${textField("TargetName", c.Tunnel?.TargetName ?? "", "text")}
        ${textField("RemotePort", String(c.Tunnel?.RemotePort ?? ""), "number")}
        ${textField("LocalPort", String(c.Tunnel?.LocalPort ?? ""), "number", "0 = auto-assign")}
      </div>

      <div class="dialog__actions">
        <button type="button" class="btn" data-act="cancel">Cancel</button>
        <button type="submit" class="btn btn--primary">Save</button>
      </div>
    </form>`;
}

function textField(name: string, value: string, type: string, placeholder = ""): string {
  return `<label class="field">
    <span>${name}</span>
    <input name="${name}" type="${type}" value="${attr(value)}" placeholder="${attr(placeholder)}" />
  </label>`;
}

function wireForm(overlay: HTMLElement, mode: "new" | "edit", original: Connection): void {
  const form = overlay.querySelector<HTMLFormElement>("#conn-form")!;
  const typeSelect = form.elements.namedItem("Type") as HTMLSelectElement;
  const fieldsBox = form.querySelector<HTMLElement>("[data-fields]")!;
  const tunnelCheckbox = form.elements.namedItem("useTunnel") as HTMLInputElement;
  const tunnelFields = form.querySelector<HTMLElement>("[data-tunnel-fields]")!;
  const errorEl = form.querySelector<HTMLElement>(".form-error")!;

  typeSelect.addEventListener("change", () => {
    const fields = visibleFields(typeSelect.value);
    fieldsBox.querySelectorAll<HTMLElement>("label.field").forEach((label) => {
      const input = label.querySelector<HTMLInputElement>("input,select");
      if (!input) return;
      label.hidden = !fields.includes(input.name);
    });
    const portInput = form.elements.namedItem("Port") as HTMLInputElement | null;
    if (portInput && !portInput.value) {
      portInput.value = String(DEFAULT_PORT[typeSelect.value as FieldsFor] ?? "");
    }
  });

  tunnelCheckbox.addEventListener("change", () => {
    tunnelFields.hidden = !tunnelCheckbox.checked;
  });

  form.querySelector('[data-act="cancel"]')!.addEventListener("click", () => {
    store.set((s) => (s.connectionFormOpen = null));
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    errorEl.hidden = true;
    const data = new FormData(form);
    const str = (k: string) => String(data.get(k) ?? "").trim();
    const num = (k: string) => {
      const v = str(k);
      return v ? parseInt(v, 10) : 0;
    };

    const conn: Connection = {
      Name: str("Name"),
      Type: typeSelect.value,
      Host: str("Host"),
      Port: num("Port"),
      User: str("User"),
      DBName: str("DBName"),
      AuthSource: str("AuthSource"),
      Group: str("Group"),
      QueryTimeoutSec: 0,
    } as Connection;

    if (tunnelCheckbox.checked) {
      conn.Tunnel = {
        KubeContext: str("KubeContext"),
        Namespace: str("Namespace"),
        TargetType: str("TargetType") || "pod",
        TargetName: str("TargetName"),
        RemotePort: num("RemotePort"),
        LocalPort: num("LocalPort"),
      };
    }

    if (!conn.Name) {
      errorEl.hidden = false;
      errorEl.textContent = "Name is required.";
      return;
    }

    try {
      await api.saveConnection(mode === "edit" ? original.Name : "", conn, str("Password"));
      const conns = await api.listConnections();
      store.set((s) => {
        s.connections = conns;
        s.connectionFormOpen = null;
      });
    } catch (err) {
      errorEl.hidden = false;
      errorEl.textContent = errorMessage(err);
      showToast(errorMessage(err));
    }
  });
}

function attr(s: string | undefined): string {
  return (s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}
