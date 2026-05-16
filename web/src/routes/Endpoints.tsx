import { api } from "../api/client";
import { useAsync } from "../hooks/useAsync";

export default function Endpoints() {
  const { data, error, loading } = useAsync(() => api.endpoints(), []);
  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!data || data.length === 0) return (
    <>
      <h1>Endpoints</h1>
      <div className="empty">No endpoints configured.</div>
    </>
  );
  return (
    <>
      <h1>Endpoints</h1>
      {data.map((ep) => (
        <div key={ep.name} className="row" style={{ cursor: "default", flexDirection: "column", alignItems: "stretch", gap: 8 }}>
          <div style={{ display: "flex", justifyContent: "space-between" }}>
            <div className="name">
              <span className="mono">{ep.method}</span> {ep.path}
            </div>
            <div className="meta">{ep.name}</div>
          </div>
          {ep.description && <div className="meta">{ep.description}</div>}
          <div className="meta">
            {ep.flows.map((m) => (
              <span key={m.sub} className="badge">
                sub={m.sub} → {m.flow}
              </span>
            ))}
          </div>
        </div>
      ))}
    </>
  );
}
