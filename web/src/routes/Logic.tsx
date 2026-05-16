import { api } from "../api/client";
import { useAsync } from "../hooks/useAsync";

export default function Logic() {
  const { data, error, loading } = useAsync(() => api.logic(), []);
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
  if (data.handlers.length === 0) {
    return (
      <>
        <h1>Logic registry</h1>
        <div className="empty">Manifest is loaded but declares no handlers.</div>
      </>
    );
  }
  return (
    <>
      <h1>Logic registry</h1>
      {data.handlers.map((h) => (
        <div key={h.name} className="row" style={{ cursor: "default", flexDirection: "column", alignItems: "stretch", gap: 8 }}>
          <div className="name">{h.name}</div>
          <div className="meta">
            {h.input_schema && h.input_schema.length > 0 && (
              <span>in: {h.input_schema.map((f) => `${f.name}:${f.type}`).join(", ")}</span>
            )}
            {h.input_schema && h.output_schema && " · "}
            {h.output_schema && h.output_schema.length > 0 && (
              <span>out: {h.output_schema.map((f) => `${f.name}:${f.type}`).join(", ")}</span>
            )}
          </div>
          {h.used_by && h.used_by.length > 0 && (
            <div className="meta">
              used by: {h.used_by.map((t) => (
                <span key={t} className="badge">{t}</span>
              ))}
            </div>
          )}
        </div>
      ))}
    </>
  );
}
