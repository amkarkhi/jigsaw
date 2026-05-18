import { useMemo, useState } from "react";
import { api, JSONSchema, LogicHandler } from "../api/client";
import { useAsync } from "../hooks/useAsync";

export default function Logic() {
  const { data, error, loading } = useAsync(() => api.logic(), []);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string | null>(null);

  const handlers = data?.handlers ?? [];
  const filtered = useMemo(() => filterHandlers(handlers, query), [handlers, query]);

  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!data) return null;

  if (!data.manifest_loaded) {
    return (
      <>
        <h1>Logic registry</h1>
        <div className="empty">
          No symbols manifest found. Run <code>jigsaw dump-symbols</code> from
          a consumer binary, or call <code>symbols.BuildFromEngine</code> +{" "}
          <code>symbols.Write</code> from your own <code>main()</code>.
        </div>
      </>
    );
  }

  if (handlers.length === 0) {
    return (
      <>
        <h1>Logic registry</h1>
        <div className="empty">Manifest is loaded but declares no handlers.</div>
      </>
    );
  }

  const detail = selected ? handlers.find((h) => h.name === selected) : null;

  return (
    <>
      <div style={{ display: "flex", alignItems: "baseline", gap: 12, marginBottom: 16, flexWrap: "wrap" }}>
        <h1 style={{ margin: 0 }}>Logic registry</h1>
        <span className="meta">
          {filtered.length} of {handlers.length} handler{handlers.length === 1 ? "" : "s"}
        </span>
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 16 }}>
        <input
          className="input"
          placeholder="Search handlers by name, description, field, or type…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ flex: 1, maxWidth: 520 }}
          autoFocus
        />
        {query && (
          <button className="btn" onClick={() => setQuery("")} title="Clear search">
            Clear
          </button>
        )}
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: detail ? "minmax(260px, 360px) 1fr" : "1fr",
          gap: 16,
          alignItems: "start",
        }}
      >
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          {filtered.length === 0 ? (
            <div className="empty">No handlers match "{query}"</div>
          ) : (
            filtered.map((h) => (
              <HandlerListItem
                key={h.name}
                h={h}
                selected={selected === h.name}
                onClick={() => setSelected(selected === h.name ? null : h.name)}
                compact={!!detail}
              />
            ))
          )}
        </div>

        {detail && <HandlerDetail h={detail} onClose={() => setSelected(null)} />}
      </div>
    </>
  );
}

function filterHandlers(handlers: LogicHandler[], q: string): LogicHandler[] {
  const needle = q.trim().toLowerCase();
  if (!needle) return handlers;
  return handlers.filter((h) => {
    if (h.name.toLowerCase().includes(needle)) return true;
    if (h.description && h.description.toLowerCase().includes(needle)) return true;
    if (h.used_by && h.used_by.some((u) => u.toLowerCase().includes(needle))) return true;
    if (schemaMatches(h.input_schema, needle)) return true;
    if (schemaMatches(h.output_schema, needle)) return true;
    if (schemaMatches(h.params_schema, needle)) return true;
    return false;
  });
}

function schemaMatches(schema: JSONSchema | null | undefined, needle: string): boolean {
  const fields = expandTopLevelFields(schema);
  return fields.some(
    (f) =>
      f.name.toLowerCase().includes(needle) ||
      f.type.toLowerCase().includes(needle) ||
      (f.description ?? "").toLowerCase().includes(needle),
  );
}

function HandlerListItem({
  h,
  selected,
  onClick,
  compact,
}: {
  h: LogicHandler;
  selected: boolean;
  onClick: () => void;
  compact: boolean;
}) {
  const inputs = expandTopLevelFields(h.input_schema);
  const outputs = expandTopLevelFields(h.output_schema);
  return (
    <div
      className="row"
      onClick={onClick}
      style={{
        cursor: "pointer",
        flexDirection: "column",
        alignItems: "stretch",
        gap: 6,
        padding: "10px 12px",
        border: selected ? "1px solid var(--accent, #4a9eff)" : undefined,
        background: selected ? "var(--panel-2)" : undefined,
      }}
    >
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 8 }}>
        <div className="name">{h.name}</div>
        {h.version && <span className="badge">v{h.version}</span>}
      </div>
      {h.description && (
        <div
          className="meta"
          style={{
            fontStyle: "italic",
            overflow: "hidden",
            textOverflow: "ellipsis",
            display: "-webkit-box",
            WebkitLineClamp: 2,
            WebkitBoxOrient: "vertical",
          }}
        >
          {h.description}
        </div>
      )}
      {!compact && (
        <div className="meta" style={{ display: "flex", gap: 12, flexWrap: "wrap" }}>
          <span>
            <span style={{ color: "var(--text)" }}>{inputs.length}</span> input
            {inputs.length === 1 ? "" : "s"}
          </span>
          <span>
            <span style={{ color: "var(--text)" }}>{outputs.length}</span> output
            {outputs.length === 1 ? "" : "s"}
          </span>
          {h.used_by && h.used_by.length > 0 && (
            <span>
              used by <span style={{ color: "var(--text)" }}>{h.used_by.length}</span>
            </span>
          )}
        </div>
      )}
    </div>
  );
}

function HandlerDetail({ h, onClose }: { h: LogicHandler; onClose: () => void }) {
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 6,
        background: "var(--panel)",
        padding: 16,
        display: "flex",
        flexDirection: "column",
        gap: 16,
        position: "sticky",
        top: 16,
      }}
    >
      <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
        <div className="name" style={{ fontSize: 16 }}>{h.name}</div>
        {h.version && <span className="badge">v{h.version}</span>}
        <span style={{ flex: 1 }} />
        <button className="btn" onClick={onClose} title="Close detail">×</button>
      </div>
      {h.description && (
        <div className="meta" style={{ fontStyle: "italic" }}>{h.description}</div>
      )}

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))",
          gap: 12,
        }}
      >
        <SchemaPanel title="Inputs" schema={h.input_schema} emptyText="No inputs declared" tone="in" />
        <SchemaPanel title="Outputs" schema={h.output_schema} emptyText="No outputs declared" tone="out" />
      </div>

      {hasFields(h.params_schema) && (
        <SchemaPanel title="Params" schema={h.params_schema} tone="param" />
      )}

      {h.used_by && h.used_by.length > 0 && (
        <div>
          <div className="meta" style={{ fontWeight: 600, color: "var(--text)", marginBottom: 4 }}>
            Used by ({h.used_by.length})
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
            {h.used_by.map((t) => (
              <span key={t} className="badge">{t}</span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function SchemaPanel({
  title,
  schema,
  emptyText,
  tone,
}: {
  title: string;
  schema: JSONSchema | null | undefined;
  emptyText?: string;
  tone: "in" | "out" | "param";
}) {
  const fields = expandTopLevelFields(schema);
  const accent =
    tone === "in" ? "#4a9eff" : tone === "out" ? "#7ab87a" : "var(--text-dim)";
  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 4,
        padding: "10px 12px",
        background: "var(--panel-2)",
      }}
    >
      <div
        style={{
          fontWeight: 600,
          color: "var(--text)",
          marginBottom: 8,
          fontSize: 12,
          textTransform: "uppercase",
          letterSpacing: 0.5,
          borderLeft: `3px solid ${accent}`,
          paddingLeft: 6,
        }}
      >
        {title}
        <span style={{ marginLeft: 6, color: "var(--text-dim)", fontWeight: 400, textTransform: "none", letterSpacing: 0 }}>
          ({fields.length})
        </span>
      </div>
      {fields.length === 0 ? (
        <div className="meta">{emptyText ?? "—"}</div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {fields.map((f) => (
            <FieldRowView key={f.name} field={f} />
          ))}
        </div>
      )}
    </div>
  );
}

function FieldRowView({ field }: { field: FieldRow }) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "auto 1fr",
        gap: "2px 10px",
        fontSize: 12,
      }}
    >
      <div style={{ fontFamily: "var(--font-mono)", color: "var(--text)" }}>
        {field.name}
        {field.required && <span style={{ color: "var(--danger, #c84)" }}> *</span>}
      </div>
      <div style={{ color: "var(--text-dim)", fontFamily: "var(--font-mono)" }}>
        <code>{field.type}</code>
      </div>
      {field.description && (
        <div style={{ gridColumn: "1 / -1", color: "var(--text-dim)", fontSize: 11 }}>
          {field.description}
        </div>
      )}
    </div>
  );
}

interface FieldRow {
  name: string;
  type: string;
  required: boolean;
  description?: string;
}

function expandTopLevelFields(schema: JSONSchema | null | undefined): FieldRow[] {
  if (!schema) return [];
  const root = resolveRef(schema, schema);
  const props = root.properties || {};
  const required = new Set(root.required || []);
  return Object.entries(props).map(([name, sub]) => {
    const resolved = resolveRef(sub, schema);
    return {
      name,
      type: jsonSchemaTypeLabel(resolved),
      required: required.has(name),
      description: resolved.description,
    };
  });
}

function hasFields(schema: JSONSchema | null | undefined): boolean {
  if (!schema) return false;
  const root = resolveRef(schema, schema);
  return !!root.properties && Object.keys(root.properties).length > 0;
}

function resolveRef(node: JSONSchema, root: JSONSchema): JSONSchema {
  if (!node.$ref) return node;
  const path = node.$ref.replace(/^#\//, "").split("/");
  let cur: any = root;
  for (const p of path) {
    if (cur == null) return node;
    cur = cur[p];
  }
  return cur || node;
}

function jsonSchemaTypeLabel(s: JSONSchema): string {
  if (!s) return "any";
  if (Array.isArray(s.type)) return s.type.join(" | ");
  if (s.type === "array") {
    const item = s.items ? jsonSchemaTypeLabel(s.items) : "any";
    return `${item}[]`;
  }
  if (s.type === "object" && s.properties) {
    const names = Object.keys(s.properties);
    return names.length > 0 ? `{ ${names.join(", ")} }` : "object";
  }
  return (s.type as string) || (s.enum ? "enum" : "any");
}
