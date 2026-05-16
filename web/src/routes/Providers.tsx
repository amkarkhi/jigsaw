import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useAsync } from "../hooks/useAsync";

export default function Providers() {
  const { data, error, loading } = useAsync(() => api.providers(), []);
  const [q, setQ] = useState("");
  const [typeFilter, setTypeFilter] = useState("");

  const types = useMemo(() => {
    if (!data) return [];
    return Array.from(new Set(data.map((p) => p.type))).sort();
  }, [data]);

  const filtered = useMemo(() => {
    if (!data) return [];
    const needle = q.trim().toLowerCase();
    return data.filter((p) => {
      if (typeFilter && p.type !== typeFilter) return false;
      if (!needle) return true;
      return p.name.toLowerCase().includes(needle) || p.type.toLowerCase().includes(needle);
    });
  }, [data, q, typeFilter]);

  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!data || data.length === 0) return (
    <>
      <h1>Providers</h1>
      <div className="empty">No providers configured.</div>
    </>
  );

  return (
    <>
      <h1>Providers</h1>
      <div className="filter-bar" style={{ marginBottom: 12 }}>
        <input
          className="input"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={`Search ${data.length} provider${data.length === 1 ? "" : "s"}…`}
          style={{ flex: 1, minWidth: 240 }}
        />
        <select className="input" value={typeFilter} onChange={(e) => setTypeFilter(e.target.value)}>
          <option value="">all types</option>
          {types.map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        {(q || typeFilter) && (
          <>
            <span className="meta">{filtered.length} / {data.length}</span>
            <button className="btn" onClick={() => { setQ(""); setTypeFilter(""); }}>Clear</button>
          </>
        )}
      </div>

      <div className="card-grid">
        {filtered.map((p) => (
          <Link key={p.name} to={`/providers/${p.name}`} className="card" style={{ textDecoration: "none", color: "inherit", display: "block" }}>
            <h3>{p.name}</h3>
            <div className="sub" style={{ marginBottom: 6 }}>
              <span style={{ color: typeColor(p.type) }}>{p.type}</span>
              {p.version && <span> · {p.version}</span>}
            </div>
            <div className="sub">init: {p.init_mode || "lazy"}</div>
            {p.pool_size > 0 && <div className="sub">pool: {p.pool_size}</div>}
            <div className="sub" style={{ marginTop: 8 }}>
              {p.user_count} task{p.user_count === 1 ? "" : "s"} use this
            </div>
          </Link>
        ))}
      </div>
    </>
  );
}

function typeColor(t: string): string {
  switch (t) {
    case "cache":         return "#7cf0c7";
    case "database":      return "#f0c977";
    case "search_engine": return "#a07cf0";
    case "vector_db":     return "#f08383";
    case "http":          return "#7c9cf0";
    default:              return "var(--text-dim)";
  }
}
