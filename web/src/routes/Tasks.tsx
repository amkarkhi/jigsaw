import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useAsync } from "../hooks/useAsync";

// Filter and sort the task list. Beyond a free-text search, you can narrow
// by provider, by logic handler, and toggle "show only unimplemented" to
// see the gaps you need to fill before a flow goes live.

type SortKey = "name" | "logic" | "provider" | "io";
type SortDir = "asc" | "desc";

export default function Tasks() {
  const { data, error, loading } = useAsync(() => api.tasks(), []);
  const [q, setQ] = useState("");
  const [providerFilter, setProviderFilter] = useState<string>("");
  const [logicFilter, setLogicFilter] = useState<string>("");
  const [onlyMissing, setOnlyMissing] = useState(false);
  const [sortKey, setSortKey] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");

  const providers = useMemo(() => {
    if (!data) return [];
    return Array.from(new Set(data.map((t) => t.provider).filter(Boolean))).sort();
  }, [data]);
  const logics = useMemo(() => {
    if (!data) return [];
    return Array.from(new Set(data.map((t) => t.logic).filter(Boolean))).sort();
  }, [data]);

  const filtered = useMemo(() => {
    if (!data) return [];
    const needle = q.trim().toLowerCase();
    const rows = data.filter((t) => {
      if (onlyMissing && t.logic_implemented) return false;
      if (providerFilter && t.provider !== providerFilter) return false;
      if (logicFilter && t.logic !== logicFilter) return false;
      if (!needle) return true;
      return (
        t.name.toLowerCase().includes(needle) ||
        (t.description ?? "").toLowerCase().includes(needle) ||
        (t.logic ?? "").toLowerCase().includes(needle) ||
        (t.provider ?? "").toLowerCase().includes(needle)
      );
    });
    rows.sort((a, b) => {
      const mul = sortDir === "asc" ? 1 : -1;
      switch (sortKey) {
        case "name":     return a.name.localeCompare(b.name) * mul;
        case "logic":    return (a.logic ?? "").localeCompare(b.logic ?? "") * mul;
        case "provider": return (a.provider ?? "").localeCompare(b.provider ?? "") * mul;
        case "io":       return ((a.inputs + a.outputs) - (b.inputs + b.outputs)) * mul;
      }
    });
    return rows;
  }, [data, q, providerFilter, logicFilter, onlyMissing, sortKey, sortDir]);

  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!data || data.length === 0) return (
    <>
      <h1>Tasks</h1>
      <div className="empty">No tasks configured.</div>
    </>
  );

  const hasFilter = q || providerFilter || logicFilter || onlyMissing;

  return (
    <>
      <h1>Tasks</h1>

      <div className="filter-bar" style={{ marginBottom: 12 }}>
        <input
          className="input"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={`Search ${data.length} task${data.length === 1 ? "" : "s"}…`}
          style={{ flex: 1, minWidth: 240, padding: "8px 12px", fontSize: 13 }}
        />
        <select className="input" value={providerFilter} onChange={(e) => setProviderFilter(e.target.value)}>
          <option value="">all providers</option>
          {providers.map((p) => <option key={p} value={p}>{p}</option>)}
        </select>
        <select className="input" value={logicFilter} onChange={(e) => setLogicFilter(e.target.value)}>
          <option value="">all logic</option>
          {logics.map((l) => <option key={l} value={l}>{l}</option>)}
        </select>
        <label className="meta" style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", userSelect: "none" }}>
          <input type="checkbox" checked={onlyMissing} onChange={(e) => setOnlyMissing(e.target.checked)} />
          only unimplemented
        </label>
        {hasFilter && (
          <>
            <span className="meta">{filtered.length} / {data.length}</span>
            <button className="btn" onClick={() => { setQ(""); setProviderFilter(""); setLogicFilter(""); setOnlyMissing(false); }}>Clear</button>
          </>
        )}
      </div>

      <div className="meta" style={{ marginBottom: 8, fontSize: 11 }}>
        Sort by:{" "}
        <SortButton current={sortKey} dir={sortDir} k="name" onSet={(k, d) => { setSortKey(k); setSortDir(d); }} label="name" />
        <SortButton current={sortKey} dir={sortDir} k="logic" onSet={(k, d) => { setSortKey(k); setSortDir(d); }} label="logic" />
        <SortButton current={sortKey} dir={sortDir} k="provider" onSet={(k, d) => { setSortKey(k); setSortDir(d); }} label="provider" />
        <SortButton current={sortKey} dir={sortDir} k="io" onSet={(k, d) => { setSortKey(k); setSortDir(d); }} label="in/out" />
      </div>

      {filtered.length === 0 && <div className="empty">No tasks match.</div>}
      {filtered.map((task) => (
        <Link key={task.name} to={`/tasks/${task.name}`} className="row">
          <div>
            <div className="name">
              {task.name}
              {!task.logic_implemented && task.logic && (
                <span className="badge error">unimplemented</span>
              )}
              {task.inherits && (
                <span className="badge">inherits {task.inherits}</span>
              )}
            </div>
            {task.description && <div className="meta">{task.description}</div>}
          </div>
          <div className="meta">
            {task.logic && <span>logic: {task.logic}</span>}
            {task.provider && <span> · provider: {task.provider}</span>}
            <span> · {task.inputs}→{task.outputs}</span>
          </div>
        </Link>
      ))}
    </>
  );
}

function SortButton({
  current,
  dir,
  k,
  onSet,
  label,
}: {
  current: SortKey;
  dir: SortDir;
  k: SortKey;
  onSet: (k: SortKey, d: SortDir) => void;
  label: string;
}) {
  const active = current === k;
  return (
    <button
      onClick={() => onSet(k, active ? (dir === "asc" ? "desc" : "asc") : "asc")}
      style={{
        background: active ? "var(--panel-2)" : "transparent",
        color: active ? "var(--accent)" : "var(--text-dim)",
        border: 0,
        padding: "2px 8px",
        marginRight: 4,
        borderRadius: 4,
        cursor: "pointer",
        fontSize: 11,
        fontFamily: "inherit",
      }}
    >
      {label}{active ? (dir === "asc" ? " ↑" : " ↓") : ""}
    </button>
  );
}
