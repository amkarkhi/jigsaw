import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import yaml from "js-yaml";
import { api, FlowSummary } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { ConfirmModal } from "../components/ConfirmModal";

export default function Flows() {
  const { data, error, loading } = useAsync(() => api.flows(), []);
  const [refreshKey, setRefreshKey] = useState(0);
  const reloaded = useAsync(() => api.flows(), [refreshKey]);
  // Show the newer of the two so duplicate reflects immediately.
  const list = reloaded.data ?? data;
  const [q, setQ] = useState("");
  const [dupTarget, setDupTarget] = useState<FlowSummary | null>(null);
  const [dupName, setDupName] = useState("");
  const [dupError, setDupError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const filtered = useMemo(() => {
    if (!list) return [];
    const needle = q.trim().toLowerCase();
    if (!needle) return list;
    return list.filter(
      (f) =>
        f.name.toLowerCase().includes(needle) ||
        (f.description ?? "").toLowerCase().includes(needle),
    );
  }, [list, q]);

  if (loading && !list) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;

  async function doDuplicate() {
    if (!dupTarget) return;
    const newName = dupName.trim();
    if (!newName) {
      setDupError("Name cannot be empty.");
      return;
    }
    if (!/^[a-zA-Z0-9_-]+$/.test(newName)) {
      setDupError("Name must be letters, digits, underscore, or dash.");
      return;
    }
    if ((list ?? []).some((f) => f.name === newName)) {
      setDupError(`A flow named "${newName}" already exists.`);
      return;
    }
    setBusy(true);
    setDupError(null);
    try {
      const loc = await api.flowLocation(dupTarget.name);
      const raw = await api.file(loc.path);
      const doc = (yaml.load(raw) as { flows: Record<string, unknown>[] }) ?? { flows: [] };
      const src = doc.flows.find((f) => (f as { name?: string }).name === dupTarget.name);
      if (!src) throw new Error("source flow disappeared");
      // Deep clone via JSON, override the name.
      const copy = JSON.parse(JSON.stringify(src)) as Record<string, unknown>;
      copy.name = newName;
      doc.flows.push(copy);
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [loc.path]: text });
      if (status !== 200 || !data.ok) {
        const msgs = (data.diagnostics ?? []).map((d) => d.Message).join("; ");
        setDupError(msgs || "save failed");
        return;
      }
      setDupTarget(null);
      setDupName("");
      setRefreshKey((k) => k + 1);
    } catch (e) {
      setDupError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <h1>Flows</h1>
      <FilterBar
        value={q}
        onChange={setQ}
        placeholder={`Filter ${list?.length ?? 0} flow${(list?.length ?? 0) === 1 ? "" : "s"}…`}
        rightCount={filtered.length}
        total={list?.length ?? 0}
      />
      {filtered.length === 0 && (
        <div className="empty">{q ? `No matches for "${q}".` : "No flows configured."}</div>
      )}
      {filtered.map((flow) => (
        <div key={flow.name} className="row" style={{ alignItems: "center" }}>
          <Link to={`/flows/${flow.name}`} style={{ flex: 1, color: "inherit" }}>
            <div className="name">{flow.name}</div>
            {flow.description && <div className="meta">{flow.description}</div>}
          </Link>
          <div className="meta" style={{ marginRight: 8 }}>
            {flow.task_count} task{flow.task_count === 1 ? "" : "s"}
            {flow.inherits && <span className="badge">inherits {flow.inherits}</span>}
            {flow.version && <span className="badge">v{flow.version}</span>}
          </div>
          <button
            className="btn"
            onClick={(e) => {
              e.preventDefault();
              setDupName(`${flow.name}_copy`);
              setDupError(null);
              setDupTarget(flow);
            }}
            title="Duplicate this flow"
          >
            Duplicate
          </button>
        </div>
      ))}

      {dupTarget && (
        <ConfirmModal
          title={`Duplicate "${dupTarget.name}"`}
          message={
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <div>Choose a name for the new flow. It will be added to the same file.</div>
              <input
                autoFocus
                className="input"
                value={dupName}
                onChange={(e) => { setDupName(e.target.value); setDupError(null); }}
              />
              {dupError && <div className="diag error" style={{ margin: 0 }}>{dupError}</div>}
            </div>
          }
          confirmLabel={busy ? "Duplicating…" : "Duplicate"}
          cancelLabel="Cancel"
          onConfirm={busy ? () => {} : doDuplicate}
          onCancel={() => { setDupTarget(null); setDupError(null); }}
        />
      )}
    </>
  );
}

function FilterBar({
  value,
  onChange,
  placeholder,
  rightCount,
  total,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  rightCount: number;
  total: number;
}) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 12 }}>
      <input
        className="input"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        style={{ flex: 1, padding: "8px 12px", fontSize: 13 }}
      />
      {value && (
        <span className="meta">
          {rightCount} / {total}
        </span>
      )}
      {value && (
        <button className="btn" onClick={() => onChange("")}>Clear</button>
      )}
    </div>
  );
}
