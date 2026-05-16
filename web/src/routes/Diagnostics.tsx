import { api } from "../api/client";
import { useAsync } from "../hooks/useAsync";

export default function Diagnostics() {
  const { data, error, loading } = useAsync(() => api.diagnostics(), []);
  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!data || data.length === 0) return (
    <>
      <h1>Diagnostics</h1>
      <div className="empty">No issues. ✓</div>
    </>
  );
  return (
    <>
      <h1>Diagnostics</h1>
      {data.map((d, i) => (
        <div key={i} className={`diag ${d.Severity}`}>
          <span className="badge" style={{ marginLeft: 0, marginRight: 8 }}>
            {d.Severity}
          </span>
          {d.Message}
        </div>
      ))}
    </>
  );
}
