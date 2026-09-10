// Small formatting/escaping helpers shared across views.

export function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/** Truncates to at most n characters, appending an ellipsis if cut. */
export function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, n) + "…";
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

export function formatTTL(seconds: number): string {
  if (seconds < 0) return "no expiry";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h`;
}

export function formatTimestamp(t: unknown): string {
  const d = new Date(t as string);
  if (isNaN(d.getTime())) return String(t);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/** Renders any Go-side cell value (already JSON-safe) as display text. */
export function formatCell(v: unknown): string {
  if (v === null || v === undefined) return "NULL";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

/** postgres/mysql -> sql, redis -> kv, mongodb -> document. Mirrors
 * config.DBType.Category() on the Go side. */
export function categoryForType(t: string): "sql" | "kv" | "document" | "" {
  switch (t) {
    case "postgres":
    case "mysql":
      return "sql";
    case "redis":
      return "kv";
    case "mongodb":
      return "document";
    default:
      return "";
  }
}

export function statusLabel(status: string): string {
  switch (status) {
    case "connected":
      return "connected";
    case "tunnel_connecting":
      return "opening tunnel…";
    case "tunnel_up":
      return "tunnel up…";
    case "db_connecting":
      return "connecting…";
    case "error":
      return "error";
    default:
      return "disconnected";
  }
}

/** Renders a TableDescription as plain text for the describe dialog —
 * mirrors the old TUI's FormatTableDescription. */
export function formatTableDescription(desc: import("./api").TableDescription): string {
  // A Go nil slice (a table with zero foreign keys, say) marshals to JSON
  // `null`, not `[]` — every array field coming from the Go bridge needs
  // this guard, not just the "obviously optional" ones.
  const columns = desc.Columns ?? [];
  const primaryKeys = desc.PrimaryKeys ?? [];
  const foreignKeys = desc.ForeignKeys ?? [];
  const indexes = desc.Indexes ?? [];

  const lines: string[] = ["Columns:"];
  for (const c of columns) {
    const nullable = c.Nullable ? "NULL" : "NOT NULL";
    let line = `  ${c.Name} ${c.DataType} ${nullable}`;
    if (c.Default) line += ` DEFAULT ${c.Default}`;
    lines.push(line);
  }
  if (primaryKeys.length) {
    lines.push("", `Primary Key: ${primaryKeys.join(", ")}`);
  }
  if (foreignKeys.length) {
    lines.push("", "Foreign Keys:");
    for (const fk of foreignKeys) {
      lines.push(`  ${fk.Column} -> ${fk.RefSchema}.${fk.RefTable}.${fk.RefColumn}`);
    }
  }
  if (indexes.length) {
    lines.push("", "Indexes:");
    for (const idx of indexes) {
      lines.push(`  ${idx.Name}${idx.Unique ? " UNIQUE" : ""} (${(idx.Columns ?? []).join(", ")})`);
    }
  }
  return lines.join("\n");
}

/** Renders MongoDB IndexInfo[] as plain text — reuses the same dialog shell. */
export function formatIndexInfo(indexes: import("./api").IndexInfo[] | null | undefined): string {
  const lines: string[] = ["Indexes:"];
  for (const idx of indexes ?? []) {
    lines.push(`  ${idx.Name}${idx.Unique ? " UNIQUE" : ""} (${(idx.Columns ?? []).join(", ")})`);
  }
  return lines.join("\n");
}

/** Renders a KVValue by its Type, ignoring the other zero-valued fields —
 * mirrors the old TUI's renderKVValue dispatch. */
export function renderKVValue(v: import("./api").KVValue): string {
  switch (v.Type) {
    case "string":
      return v.String;
    case "hash":
      return Object.entries(v.Hash ?? {})
        .map(([k, val]) => `${k}: ${val}`)
        .join("\n");
    case "list":
      return (v.List ?? []).map((s, i) => `${i}) ${s}`).join("\n");
    case "set":
      return (v.Set ?? []).join("\n");
    case "zset":
      return (v.ZSet ?? []).map((m) => `${m.Score}\t${m.Member}`).join("\n");
    default:
      return `(unsupported type: ${v.Type})`;
  }
}

/** CSS custom-property name for a ConnStatus value's dot/text color —
 * kept in one place so the sidebar, status strip, and connect button never
 * drift out of sync on what each status means. */
export function statusColorVar(status: string): string {
  switch (status) {
    case "connected":
      return "var(--ok)";
    case "error":
      return "var(--danger)";
    case "tunnel_connecting":
    case "tunnel_up":
    case "db_connecting":
      return "var(--warn)";
    default:
      return "var(--muted)";
  }
}
