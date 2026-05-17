import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import yaml from "js-yaml";
import { api, FlowSummary } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { ConfirmModal } from "../components/ConfirmModal";

// Flows index — search, duplicate, and create new. New/duplicate write
// through /api/files so the full validator runs before persisting.

export default function Flows() {
  const navigate = useNavigate();
  const { data, error, loading } = useAsync(() => api.flows(), []);
  const [refreshKey, setRefreshKey] = useState(0);
  const reloaded = useAsync(() => api.flows(), [refreshKey]);
  const list = reloaded.data ?? data;
  const [q, setQ] = useState("");

  // Modals for duplicate / create.
  const [dupTarget, setDupTarget] = useState<FlowSummary | null>(null);
  const [dupName, setDupName] = useState("");
  const [dupError, setDupError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createDesc, setCreateDesc] = useState("");
  const [createStarter, setCreateStarter] = useState(""); // first task name to seed the flow
  const [createError, setCreateError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Need task names for the "starter task" picker in the create modal.
  // The validator requires at least one task (or an inherits) in every flow.
  const tasksList = useAsync(() => api.tasks(), []);
  useEffect(() => {
    if (createOpen && !createStarter && tasksList.data && tasksList.data.length > 0) {
      setCreateStarter(tasksList.data[0].name);
    }
  }, [createOpen, createStarter, tasksList.data]);

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
    const err = validateNewName(newName, (list ?? []).map((f) => f.name));
    if (err) { setDupError(err); return; }
    setBusy(true);
    setDupError(null);
    try {
      const loc = await api.flowLocation(dupTarget.name);
      const raw = await api.file(loc.path);
      const doc = (yaml.load(raw) as { flows: Record<string, unknown>[] }) ?? { flows: [] };
      const src = doc.flows.find((f) => (f as { name?: string }).name === dupTarget.name);
      if (!src) throw new Error("source flow disappeared");
      const copy = JSON.parse(JSON.stringify(src)) as Record<string, unknown>;
      copy.name = newName;
      doc.flows.push(copy);
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [loc.path]: text });
      if (status !== 200 || !data.ok) {
        setDupError((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
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

  async function doCreate() {
    const newName = createName.trim();
    const err = validateNewName(newName, (list ?? []).map((f) => f.name));
    if (err) { setCreateError(err); return; }
    if (!createStarter) {
      setCreateError("Pick a starter task. Flows must contain at least one task.");
      return;
    }
    setBusy(true);
    setCreateError(null);
    try {
      const path = `flows/${newName}.yml`;
      const newFlow: Record<string, unknown> = {
        name: newName,
        description: createDesc.trim() || "",
        tasks: [{ name: createStarter }],
      };
      const doc = { flows: [newFlow] };
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [path]: text });
      if (status !== 200 || !data.ok) {
        setCreateError((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
        return;
      }
      setCreateOpen(false);
      setCreateName("");
      setCreateDesc("");
      setCreateStarter("");
      navigate(`/flows/${newName}`);
    } catch (e) {
      setCreateError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16 }}>
        <h1 style={{ margin: 0 }}>Flows</h1>
        <button
          className="btn btn-primary"
          style={{ marginLeft: "auto" }}
          onClick={() => { setCreateName(""); setCreateDesc(""); setCreateError(null); setCreateOpen(true); }}
        >
          + New flow
        </button>
      </div>
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

      {createOpen && (
        <ConfirmModal
          title="New flow"
          message={
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <div>Creates a flow at <code>flows/&lt;name&gt;.yml</code> with one starter task. You can rewire it in the graph editor after.</div>
              <input
                autoFocus
                className="input"
                placeholder="name (letters, digits, _ or -)"
                value={createName}
                onChange={(e) => { setCreateName(e.target.value); setCreateError(null); }}
              />
              <input
                className="input"
                placeholder="description (optional)"
                value={createDesc}
                onChange={(e) => setCreateDesc(e.target.value)}
              />
              <select
                className="input"
                value={createStarter}
                onChange={(e) => { setCreateStarter(e.target.value); setCreateError(null); }}
              >
                <option value="">— pick a starter task —</option>
                {(tasksList.data ?? []).map((t) => (
                  <option key={t.name} value={t.name}>{t.name}</option>
                ))}
              </select>
              {createError && <div className="diag error" style={{ margin: 0 }}>{createError}</div>}
            </div>
          }
          confirmLabel={busy ? "Creating…" : "Create"}
          cancelLabel="Cancel"
          onConfirm={busy ? () => {} : doCreate}
          onCancel={() => { setCreateOpen(false); setCreateError(null); }}
        />
      )}
    </>
  );
}

function validateNewName(name: string, existing: string[]): string | null {
  if (!name) return "Name cannot be empty.";
  if (!/^[a-zA-Z0-9_-]+$/.test(name)) return "Name must be letters, digits, underscore, or dash.";
  if (existing.includes(name)) return `A flow named "${name}" already exists.`;
  return null;
}

function FilterBar({
  value, onChange, placeholder, rightCount, total,
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
      {value && <span className="meta">{rightCount} / {total}</span>}
      {value && <button className="btn" onClick={() => onChange("")}>Clear</button>}
    </div>
  );
}
