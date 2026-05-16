import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import yaml from "js-yaml";
import { api, Diagnostic, FieldDef } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { useUnsavedGuard } from "../hooks/useUnsavedGuard";
import { ConfirmModal } from "../components/ConfirmModal";

// Editable task detail. Loads the task's source YAML, exposes the common
// fields as inputs (label, description, version, timeout, retry, logic,
// provider, inherits) plus an editable inputs/outputs table. "Show JSON"
// reveals the merged result for developers. Save writes through /api/files
// so the full validator runs.

interface EditableTask {
  name: string;
  description: string;
  label: string;
  version: string;
  timeout: number | "";
  retry: number | "";
  logic: string;
  provider: string;
  inherits: string;
  inputs: FieldDef[];
  outputs: FieldDef[];
  _raw: Record<string, unknown>;
}

export default function TaskDetail() {
  const { name } = useParams();
  const detail = useAsync(() => api.task(name!), [name]);
  const usage = useAsync(() => api.taskUsage(name!), [name]);
  const allTasks = useAsync(() => api.tasks(), []);

  const [filePath, setFilePath] = useState<string>("");
  const [edited, setEdited] = useState<EditableTask | null>(null);
  const [original, setOriginal] = useState<string>(""); // serialized json of edited for dirty check
  const [showJSON, setShowJSON] = useState(false);
  const [busy, setBusy] = useState(false);
  const [diags, setDiags] = useState<Diagnostic[]>([]);
  const [flash, setFlash] = useState<string | null>(null);
  const [resetConfirm, setResetConfirm] = useState(false);

  useEffect(() => {
    if (!detail.data) return;
    const t = detail.data.task;
    const ed: EditableTask = {
      name: t.name,
      description: t.description ?? "",
      label: t.label ?? "",
      version: t.version ?? "",
      timeout: typeof t.timeout === "number" ? t.timeout : "",
      retry: typeof t.retry === "number" ? t.retry : "",
      logic: t.logic ?? "",
      provider: t.provider ?? "",
      inherits: t.inherits ?? "",
      inputs: (t.inputs ?? []).map((f) => ({ ...f })),
      outputs: (t.outputs ?? []).map((f) => ({ ...f })),
      _raw: t as unknown as Record<string, unknown>,
    };
    setEdited(ed);
    setOriginal(JSON.stringify(ed));
    (async () => {
      try {
        const loc = await api.taskLocation(t.name);
        setFilePath(loc.path);
      } catch (e) {
        // non-fatal: user just can't save
        setDiags([{ Severity: "error", File: "", Message: `cannot locate task file: ${(e as Error).message}` }]);
      }
    })();
  }, [detail.data]);

  const dirty = edited != null && JSON.stringify(edited) !== original;
  const blocker = useUnsavedGuard(dirty);

  const referencingTasks = useMemo(() => {
    return (allTasks.data ?? []).filter((t) => t.inherits === name);
  }, [allTasks.data, name]);

  function patch<K extends keyof EditableTask>(key: K, value: EditableTask[K]) {
    if (!edited) return;
    setEdited({ ...edited, [key]: value });
    setFlash(null);
  }

  async function save() {
    if (!edited || !filePath) return;
    setBusy(true);
    setDiags([]);
    setFlash(null);
    try {
      const raw = await api.file(filePath);
      const doc = (yaml.load(raw) as { tasks?: Record<string, unknown>[] }) ?? { tasks: [] };
      const tasks = doc.tasks ?? [];
      const idx = tasks.findIndex((t) => (t as { name?: string }).name === edited.name);
      if (idx < 0) throw new Error("task vanished while editing");

      // Merge changes back so we don't drop unknown fields (fallback, metadata, etc.).
      const merged = { ...(tasks[idx] as Record<string, unknown>) };
      writeStringOrDelete(merged, "description", edited.description);
      writeStringOrDelete(merged, "label", edited.label);
      writeStringOrDelete(merged, "version", edited.version);
      writeStringOrDelete(merged, "logic", edited.logic);
      writeStringOrDelete(merged, "provider", edited.provider);
      writeStringOrDelete(merged, "inherits", edited.inherits);
      writeNumberOrDelete(merged, "timeout", edited.timeout);
      writeNumberOrDelete(merged, "retry", edited.retry);
      // For inputs/outputs we always rewrite (empty arrays become explicit).
      merged.inputs = edited.inputs.map(stripEmptyFields);
      merged.outputs = edited.outputs.map(stripEmptyFields);

      tasks[idx] = merged;
      doc.tasks = tasks;
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [filePath]: text });
      if (status !== 200 || !data.ok) {
        setDiags(data.diagnostics ?? [{ Severity: "error", File: filePath, Message: "save failed" }]);
        return;
      }
      setOriginal(JSON.stringify(edited));
      setFlash("Saved.");
    } catch (e) {
      setDiags([{ Severity: "error", File: "", Message: (e as Error).message }]);
    } finally {
      setBusy(false);
    }
  }

  function resetEdits() {
    if (!detail.data) return;
    const t = detail.data.task;
    const ed: EditableTask = {
      name: t.name,
      description: t.description ?? "",
      label: t.label ?? "",
      version: t.version ?? "",
      timeout: typeof t.timeout === "number" ? t.timeout : "",
      retry: typeof t.retry === "number" ? t.retry : "",
      logic: t.logic ?? "",
      provider: t.provider ?? "",
      inherits: t.inherits ?? "",
      inputs: (t.inputs ?? []).map((f) => ({ ...f })),
      outputs: (t.outputs ?? []).map((f) => ({ ...f })),
      _raw: t as unknown as Record<string, unknown>,
    };
    setEdited(ed);
    setOriginal(JSON.stringify(ed));
  }

  if (detail.loading) return <div className="loading">Loading…</div>;
  if (detail.error) return <div className="empty">Error: {detail.error.message}</div>;
  if (!detail.data || !edited) return null;

  return (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16, flexWrap: "wrap" }}>
        <h1 style={{ margin: 0 }}>Task: {edited.name}</h1>
        {dirty && <span className="badge warn">unsaved</span>}
        {detail.data.logic_implemented ? (
          <span className="badge ok">logic implemented</span>
        ) : edited.logic ? (
          <span className="badge error">logic missing</span>
        ) : null}
        {edited.inherits && <Link to={`/tasks/${edited.inherits}`} className="badge">inherits {edited.inherits}</Link>}
        {edited.version && <span className="badge">v{edited.version}</span>}

        <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
          <button className={`btn ${showJSON ? "btn-primary" : ""}`} onClick={() => setShowJSON((v) => !v)}>
            {showJSON ? "Hide JSON" : "Show JSON"}
          </button>
          <button className="btn" disabled={!dirty || busy} onClick={() => setResetConfirm(true)}>Reset</button>
          <button className="btn btn-primary" disabled={!dirty || busy || !filePath} onClick={save}>
            {busy ? "Saving…" : "Save"}
          </button>
        </span>
      </div>

      {flash && <div className="diag" style={{ borderLeftColor: "var(--accent)" }}>{flash}</div>}
      {diags.length > 0 && (
        <div style={{ marginBottom: 12 }}>
          {diags.map((d, i) => (
            <div key={i} className={`diag ${d.Severity}`}>
              <span className="badge" style={{ marginLeft: 0, marginRight: 8 }}>{d.Severity}</span>
              {d.Message}
            </div>
          ))}
        </div>
      )}

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16, marginBottom: 16 }}>
        <DetailCard title="Properties">
          <FieldRow label="label">
            <input className="input" value={edited.label} onChange={(e) => patch("label", e.target.value)} placeholder="(optional flow-local name)" />
          </FieldRow>
          <FieldRow label="description">
            <input className="input" value={edited.description} onChange={(e) => patch("description", e.target.value)} />
          </FieldRow>
          <FieldRow label="version">
            <input className="input" value={edited.version} onChange={(e) => patch("version", e.target.value)} placeholder="e.g. 1.0.0" />
          </FieldRow>
          <FieldRow label="logic">
            <input className="input" value={edited.logic} onChange={(e) => patch("logic", e.target.value)} placeholder="handler name" />
          </FieldRow>
          <FieldRow label="provider">
            <input className="input" value={edited.provider} onChange={(e) => patch("provider", e.target.value)} placeholder="(optional)" />
          </FieldRow>
          <FieldRow label="timeout (ms)">
            <input
              className="input" type="number" min={0}
              value={edited.timeout === "" ? "" : edited.timeout}
              onChange={(e) => patch("timeout", e.target.value === "" ? "" : Number(e.target.value))}
            />
          </FieldRow>
          <FieldRow label="retry">
            <input
              className="input" type="number" min={0}
              value={edited.retry === "" ? "" : edited.retry}
              onChange={(e) => patch("retry", e.target.value === "" ? "" : Number(e.target.value))}
            />
          </FieldRow>
          <FieldRow label="inherits">
            <input className="input" value={edited.inherits} onChange={(e) => patch("inherits", e.target.value)} placeholder="parent task (optional)" />
          </FieldRow>
        </DetailCard>

        <DetailCard title="Used in">
          <UsageList flows={usage.data ?? []} loading={usage.loading} />
          {referencingTasks.length > 0 && (
            <div style={{ marginTop: 12 }}>
              <div className="meta" style={{ fontSize: 11, marginBottom: 4 }}>inherited by</div>
              {referencingTasks.map((rt) => (
                <Link key={rt.name} to={`/tasks/${rt.name}`} className="badge" style={{ marginRight: 4 }}>
                  {rt.name}
                </Link>
              ))}
            </div>
          )}
          {filePath && (
            <div className="meta" style={{ marginTop: 12, fontSize: 11 }}>
              source: <code>{filePath}</code>
            </div>
          )}
        </DetailCard>
      </div>

      <FieldsEditor
        title="Inputs"
        side="in"
        fields={edited.inputs}
        onChange={(next) => patch("inputs", next)}
      />
      <FieldsEditor
        title="Outputs"
        side="out"
        fields={edited.outputs}
        onChange={(next) => patch("outputs", next)}
      />

      {showJSON && (
        <DetailCard title="Raw JSON" style={{ marginBottom: 16 }}>
          <pre className="json" style={{ margin: 0 }}>{JSON.stringify(detail.data.task, null, 2)}</pre>
        </DetailCard>
      )}

      {blocker.state === "blocked" && (
        <ConfirmModal
          title="Unsaved changes"
          message="You have unsaved changes to this task. Leaving will discard them."
          confirmLabel="Discard and leave"
          cancelLabel="Stay"
          danger
          onConfirm={() => blocker.proceed?.()}
          onCancel={() => blocker.reset?.()}
        />
      )}

      {resetConfirm && (
        <ConfirmModal
          title="Reset edits?"
          message="Revert all unsaved changes to the last loaded state."
          confirmLabel="Reset"
          cancelLabel="Keep editing"
          danger
          onConfirm={() => { resetEdits(); setResetConfirm(false); }}
          onCancel={() => setResetConfirm(false)}
        />
      )}
    </>
  );
}

// --- subcomponents --------------------------------------------------------

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "110px 1fr", gap: 8, marginBottom: 6, alignItems: "center" }}>
      <span className="meta">{label}</span>
      {children}
    </div>
  );
}

function DetailCard({
  title,
  children,
  style,
}: {
  title: string;
  children: React.ReactNode;
  style?: React.CSSProperties;
}) {
  return (
    <div
      style={{
        background: "var(--panel)",
        border: "1px solid var(--border)",
        borderRadius: 6,
        padding: 16,
        ...style,
      }}
    >
      <h3 style={{ margin: "0 0 12px 0", fontSize: 12, fontWeight: 500, color: "var(--text-dim)", textTransform: "uppercase", letterSpacing: 0.5 }}>
        {title}
      </h3>
      {children}
    </div>
  );
}

function UsageList({ flows, loading }: { flows: string[]; loading: boolean }) {
  if (loading) return <div className="meta">scanning…</div>;
  if (flows.length === 0) return <div className="meta">not referenced by any flow</div>;
  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
      {flows.map((name) => (
        <Link key={name} to={`/flows/${name}`} className="badge">{name}</Link>
      ))}
    </div>
  );
}

function FieldsEditor({
  title,
  side,
  fields,
  onChange,
}: {
  title: string;
  side: "in" | "out";
  fields: FieldDef[];
  onChange: (next: FieldDef[]) => void;
}) {
  function update(i: number, patch: Partial<FieldDef>) {
    onChange(fields.map((f, idx) => (idx === i ? { ...f, ...patch } : f)));
  }
  function add() {
    onChange([...fields, { name: "", type: "string" }]);
  }
  function remove(i: number) {
    onChange(fields.filter((_, idx) => idx !== i));
  }
  return (
    <DetailCard title={`${title} (${fields.length})`} style={{ marginBottom: 16 }}>
      {fields.length === 0 && <div className="meta" style={{ marginBottom: 8 }}>none</div>}
      <div style={{ display: "grid", gridTemplateColumns: "minmax(140px, 1.5fr) 110px 90px minmax(140px, 1.5fr) 40px", gap: 6, alignItems: "center", marginBottom: 6, fontSize: 11 }}>
        <div className="meta">name</div>
        <div className="meta">type</div>
        <div className="meta">required</div>
        <div className="meta">{side === "in" ? "from" : "default"}</div>
        <div></div>
      </div>
      {fields.map((f, i) => (
        <div key={i} style={{ display: "grid", gridTemplateColumns: "minmax(140px, 1.5fr) 110px 90px minmax(140px, 1.5fr) 40px", gap: 6, marginBottom: 6, alignItems: "center" }}>
          <input className="input" value={f.name} onChange={(e) => update(i, { name: e.target.value })} />
          <select className="input" value={f.type} onChange={(e) => update(i, { type: e.target.value })}>
            <option value="string">string</option>
            <option value="int">int</option>
            <option value="bool">bool</option>
            <option value="object">object</option>
            <option value="array">array</option>
            <option value="any">any</option>
          </select>
          <label className="meta" style={{ display: "flex", alignItems: "center", gap: 4 }}>
            <input type="checkbox" checked={!!f.required} onChange={(e) => update(i, { required: e.target.checked })} />
            <span>req</span>
          </label>
          {side === "in" ? (
            <input
              className="input"
              value={(f.from ?? "") + (f.field ? "." + f.field : "")}
              onChange={(e) => {
                const v = e.target.value;
                const dot = v.indexOf(".");
                if (dot < 0) update(i, { from: v, field: undefined });
                else update(i, { from: v.slice(0, dot), field: v.slice(dot + 1) });
              }}
              placeholder="label[.field]"
            />
          ) : (
            <input
              className="input"
              value={typeof f.default === "string" ? f.default : f.default == null ? "" : JSON.stringify(f.default)}
              onChange={(e) => update(i, { default: e.target.value === "" ? undefined : e.target.value })}
              placeholder="(optional default)"
            />
          )}
          <button className="btn" onClick={() => remove(i)} title="Remove" style={{ color: "var(--error)", padding: "4px 8px" }}>×</button>
        </div>
      ))}
      <button className="btn" onClick={add} style={{ marginTop: 6 }}>+ Add field</button>
    </DetailCard>
  );
}

// --- helpers --------------------------------------------------------------

function writeStringOrDelete(target: Record<string, unknown>, key: string, value: string) {
  if (value === "") delete target[key];
  else target[key] = value;
}
function writeNumberOrDelete(target: Record<string, unknown>, key: string, value: number | "") {
  if (value === "" || value === 0) delete target[key];
  else target[key] = value;
}
function stripEmptyFields(f: FieldDef): FieldDef {
  const out: FieldDef = { name: f.name, type: f.type };
  if (f.required) out.required = true;
  if (f.default !== undefined && f.default !== "") out.default = f.default;
  if (f.from) out.from = f.from;
  if (f.field) out.field = f.field;
  return out;
}
