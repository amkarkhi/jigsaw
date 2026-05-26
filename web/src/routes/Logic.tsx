import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, JSONSchema, LogicHandler } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { SchemaPanel, hasFields, expandTopLevelFields } from "../components/SchemaPanel";

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
    if (h.skippable_inputs && h.skippable_inputs.some((f) => f.toLowerCase().includes(needle))) return true;
    if (needle === "skippable" && (h.skippable_inputs?.length ?? 0) > 0) return true;
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
          {h.skippable_inputs && h.skippable_inputs.length > 0 && (
            <span
              title='Inputs marked `jig:"skippable"` — flows may omit them via bind.skip'
              style={{
                border: "1px dashed var(--border)",
                borderRadius: 3,
                padding: "0 5px",
                fontSize: 10,
              }}
            >
              {h.skippable_inputs.length} skippable
            </span>
          )}
        </div>
      )}
    </div>
  );
}

function HandlerDetail({ h, onClose }: { h: LogicHandler; onClose: () => void }) {
  const navigate = useNavigate();
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
        <button
          className="btn"
          onClick={() => navigate(`/playground?logic=${encodeURIComponent(h.name)}`)}
          title="Open this logic in the playground"
        >
          Test in playground
        </button>
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
        <SchemaPanel
          title="Inputs"
          schema={h.input_schema}
          emptyText="No inputs declared"
          tone="in"
          skippable={h.skippable_inputs ?? []}
        />
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
