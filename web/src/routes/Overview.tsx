import { api } from "../api/client";
import { useAsync } from "../hooks/useAsync";

export default function Overview() {
  const { data, error, loading } = useAsync(() => api.overview(), []);

  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!data) return null;

  const stats: { label: string; value: number; warn?: boolean }[] = [
    { label: "Flows", value: data.flows },
    { label: "Tasks", value: data.tasks },
    { label: "Providers", value: data.providers },
    { label: "Endpoints", value: data.endpoints },
    { label: "Logic handlers", value: data.logic_handlers },
    {
      label: "Unimplemented",
      value: data.unimplemented_logic,
      warn: data.unimplemented_logic > 0,
    },
  ];

  return (
    <>
      <h1>Overview</h1>
      <div className="stat-grid">
        {stats.map((s) => (
          <div key={s.label} className={`stat ${s.warn ? "warn" : ""}`}>
            <div className="label">{s.label}</div>
            <div className="value">{s.value}</div>
          </div>
        ))}
      </div>
      {!data.manifest_loaded && (
        <div className="diag warning">
          No symbols manifest found. Logic handlers cannot be cross-checked
          until <code>./.jigsaw/symbols.json</code> exists.
        </div>
      )}
    </>
  );
}
