import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import yaml from "js-yaml";
import { api, TaskSummary } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { ConfirmModal } from "../components/ConfirmModal";

type SortKey = "name" | "logic" | "provider" | "io";
type SortDir = "asc" | "desc";

export default function Tasks() {
  const navigate = useNavigate();
  const { data, error, loading } = useAsync(() => api.tasks(), []);
  const [refreshKey, setRefreshKey] = useState(0);
  const reloaded = useAsync(() => api.tasks(), [refreshKey]);
  const list = reloaded.data ?? data;

  const [q, setQ] = useState("");
  const [providerFilter, setProviderFilter] = useState<string>("");
  const [logicFilter, setLogicFilter] = useState<string>("");
  const [onlyMissing, setOnlyMissing] = useState(false);
  const [sortKey, setSortKey] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");

  // Create / duplicate state.
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createDesc, setCreateDesc] = useState("");
  const [createLogic, setCreateLogic] = useState("");
  const [createProvider, setCreateProvider] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [dupTarget, setDupTarget] = useState<TaskSummary | null>(null);
  const [dupName, setDupName] = useState("");
  const [dupError, setDupError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const providers = useMemo(() => {
    if (!list) return [];
    return Array.from(new Set(list.map((t) => t.provider).filter(Boolean))).sort();
  }, [list]);
  const logics = useMemo(() => {
    if (!list) return [];
    return Array.from(new Set(list.map((t) => t.logic).filter(Boolean))).sort();
  }, [list]);

  // Authoritative lists for the create modal: registered logic handlers come
  // from the symbols manifest, providers come from the providers/ dir.
  const logicReg = useAsync(() => api.logic(), []);
  const providersReg = useAsync(() => api.providers(), []);
  const allLogics = useMemo(() => {
    const fromManifest = (logicReg.data?.handlers ?? []).map((h) => h.name);
    const fromUsage = logics;
    return Array.from(new Set([...fromManifest, ...fromUsage])).sort();
  }, [logicReg.data, logics]);
  const allProviders = useMemo(() => {
    const fromConfig = (providersReg.data ?? []).map((p) => p.name);
    return Array.from(new Set([...fromConfig, ...providers])).sort();
  }, [providersReg.data, providers]);

  const filtered = useMemo(() => {
    if (!list) return [];
    const needle = q.trim().toLowerCase();
    const rows = list.filter((t) => {
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
  }, [list, q, providerFilter, logicFilter, onlyMissing, sortKey, sortDir]);

  if (loading && !list) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!list || list.length === 0) return (
    <>
      <header style={{ display: "flex", alignItems: "center", marginBottom: 16 }}>
        <h1 style={{ margin: 0, flex: 1 }}>Tasks</h1>
        <button className="btn btn-primary" onClick={openCreate}>+ New task</button>
      </header>
      <div className="empty">No tasks configured.</div>
      {renderCreateModal()}
    </>
  );

  const hasFilter = q || providerFilter || logicFilter || onlyMissing;

  function openCreate() {
    setCreateName("");
    setCreateDesc("");
    setCreateLogic("");
    setCreateProvider("");
    setCreateError(null);
    setCreateOpen(true);
  }

  async function doCreate() {
    const newName = createName.trim();
    const err = validateNewName(newName, (list ?? []).map((t) => t.name));
    if (err) { setCreateError(err); return; }
    if (!createLogic.trim() && !createProvider.trim()) {
      setCreateError("Tasks must declare either a logic handler or a provider. Fill at least one.");
      return;
    }
    setBusy(true);
    setCreateError(null);
    try {
      const path = `tasks/${newName}.yml`;
      const task: Record<string, unknown> = { name: newName };
      if (createDesc.trim()) task.description = createDesc.trim();
      if (createLogic.trim()) task.logic = createLogic.trim();
      if (createProvider.trim()) task.provider = createProvider.trim();
      task.inputs = [];
      task.outputs = [];
      const doc = { tasks: [task] };
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [path]: text });
      if (status !== 200 || !data.ok) {
        setCreateError((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
        return;
      }
      setCreateOpen(false);
      navigate(`/tasks/${newName}`);
    } catch (e) {
      setCreateError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function doDuplicate() {
    if (!dupTarget) return;
    const newName = dupName.trim();
    const err = validateNewName(newName, (list ?? []).map((t) => t.name));
    if (err) { setDupError(err); return; }
    setBusy(true);
    setDupError(null);
    try {
      const loc = await api.taskLocation(dupTarget.name);
      const raw = await api.file(loc.path);
      const doc = (yaml.load(raw) as { tasks: Record<string, unknown>[] }) ?? { tasks: [] };
      const src = doc.tasks.find((t) => (t as { name?: string }).name === dupTarget.name);
      if (!src) throw new Error("source task disappeared");
      const copy = JSON.parse(JSON.stringify(src)) as Record<string, unknown>;
      copy.name = newName;
      doc.tasks.push(copy);
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

  function renderCreateModal() {
    if (!createOpen) return null;
    return (
      <ConfirmModal
        title="New task"
        message={
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <div>Creates a task at <code>tasks/&lt;name&gt;.yml</code>. At least one of <b>logic</b> or <b>provider</b> is required.</div>
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
            <input
              className="input"
              list="logic-options"
              placeholder="logic handler (type or pick)"
              value={createLogic}
              onChange={(e) => { setCreateLogic(e.target.value); setCreateError(null); }}
            />
            <datalist id="logic-options">
              {allLogics.map((l) => <option key={l} value={l} />)}
            </datalist>
            <input
              className="input"
              list="provider-options"
              placeholder="provider (type or pick)"
              value={createProvider}
              onChange={(e) => { setCreateProvider(e.target.value); setCreateError(null); }}
            />
            <datalist id="provider-options">
              {allProviders.map((p) => <option key={p} value={p} />)}
            </datalist>
            {createError && <div className="diag error" style={{ margin: 0 }}>{createError}</div>}
          </div>
        }
        confirmLabel={busy ? "Creating…" : "Create"}
        cancelLabel="Cancel"
        onConfirm={busy ? () => {} : doCreate}
        onCancel={() => { setCreateOpen(false); setCreateError(null); }}
      />
    );
  }

  return (
    <>
      <header style={{ display: "flex", alignItems: "center", marginBottom: 16 }}>
        <h1 style={{ margin: 0, flex: 1 }}>Tasks</h1>
        <button className="btn btn-primary" onClick={openCreate}>+ New task</button>
      </header>

      <div className="filter-bar" style={{ marginBottom: 12 }}>
        <input
          className="input"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={`Search ${list.length} task${list.length === 1 ? "" : "s"}…`}
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
            <span className="meta">{filtered.length} / {list.length}</span>
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
        <div key={task.name} className="row" style={{ alignItems: "center" }}>
          <Link to={`/tasks/${task.name}`} style={{ flex: 1, color: "inherit" }}>
            <div className="name">
              {task.name}
              {!task.logic_implemented && task.logic && (
                <span className="badge error">unimplemented</span>
              )}
              {task.inherits && <span className="badge">inherits {task.inherits}</span>}
            </div>
            {task.description && <div className="meta">{task.description}</div>}
          </Link>
          <div className="meta" style={{ marginRight: 8 }}>
            {task.logic && <span>logic: {task.logic}</span>}
            {task.provider && <span> · provider: {task.provider}</span>}
            <span> · {task.inputs}→{task.outputs}</span>
          </div>
          <button
            className="btn"
            onClick={(e) => {
              e.preventDefault();
              setDupName(`${task.name}_copy`);
              setDupError(null);
              setDupTarget(task);
            }}
            title="Duplicate this task"
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
              <div>Choose a name for the new task. It will be added to the same file.</div>
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

      {renderCreateModal()}
    </>
  );
}

function validateNewName(name: string, existing: string[]): string | null {
  if (!name) return "Name cannot be empty.";
  if (!/^[a-zA-Z0-9_-]+$/.test(name)) return "Name must be letters, digits, underscore, or dash.";
  if (existing.includes(name)) return `A task named "${name}" already exists.`;
  return null;
}

function SortButton({
  current, dir, k, onSet, label,
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
