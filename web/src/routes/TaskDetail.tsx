import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import yaml from "js-yaml";
import { api, Diagnostic, LogicHandler } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { useUnsavedGuard } from "../hooks/useUnsavedGuard";
import { ConfirmModal } from "../components/ConfirmModal";
import { SchemaPanel } from "../components/SchemaPanel";

// Editable task detail. Loads the task's source YAML and exposes editable
// task-level fields (description, version, timeout, retry, logic, provider,
// inherits, wrapper). I/O shape is no longer a task property — it lives on
// the linked logic handler — so this page renders the logic's input/output
// schema read-only (and links to the logic registry for details).
// "Show JSON" reveals the merged task for developers. Save writes through
// /api/files so the full validator runs.

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
  wrapper: string;
  wrapperParams: string;
  _raw: Record<string, unknown>;
}

export default function TaskDetail() {
  const { name } = useParams();
  const navigate = useNavigate();
  const detail = useAsync(() => api.task(name!), [name]);
  const usage = useAsync(() => api.taskUsage(name!), [name]);
  const allTasks = useAsync(() => api.tasks(), []);
  const logicRegistry = useAsync(() => api.logic(), []);

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
      wrapper: t.wrapper?.task ?? "",
      wrapperParams: t.wrapper?.params ? JSON.stringify(t.wrapper.params, null, 2) : "",
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
      // Handle wrapper
      if (edited.wrapper) {
        let wrapperParams: Record<string, unknown> | undefined;
        if (edited.wrapperParams.trim()) {
          try {
            wrapperParams = JSON.parse(edited.wrapperParams);
          } catch {
            // invalid JSON, skip params
          }
        }
        merged.wrapper = { task: edited.wrapper, ...(wrapperParams ? { params: wrapperParams } : {}) };
      } else {
        delete merged.wrapper;
      }
      // I/O shape lives on the logic handler (struct tags / jsonschema), not
      // on the task. Strip any stale inputs/outputs left over from older
      // saves so the YAML stays clean.
      delete merged.inputs;
      delete merged.outputs;

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
      wrapper: t.wrapper?.task ?? "",
      wrapperParams: t.wrapper?.params ? JSON.stringify(t.wrapper.params, null, 2) : "",
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
          <button
            className="btn"
            onClick={() => navigate(`/playground?task=${encodeURIComponent(edited.name)}`)}
            title="Open this task in the playground"
          >
            Test in playground
          </button>
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

      {edited.label && (
        <div className="diag warning" style={{ marginBottom: 12 }}>
          This task has a top-level <code>label</code> set in YAML. Labels are
          now a per-placement concept on <code>TaskRef</code> — set them in
          the flow graph editor's inspector instead. The existing value is
          preserved for back-compat but new flows should label per-placement.
        </div>
      )}

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16, marginBottom: 16 }}>
        <DetailCard title="Properties">
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
          <FieldRow label="wrapper">
            <input className="input" value={edited.wrapper} onChange={(e) => patch("wrapper", e.target.value)} placeholder="wrapper task (optional)" />
          </FieldRow>
          {edited.wrapper && (
            <FieldRow label="wrapper params">
              <textarea 
                className="input" 
                value={edited.wrapperParams} 
                onChange={(e) => patch("wrapperParams", e.target.value)} 
                placeholder='{"keys": ["query"], "ttl": "120s"}'
                rows={3}
                style={{ fontFamily: "monospace", fontSize: 12 }}
              />
            </FieldRow>
          )}
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

      <LogicSchemaSection
        logicName={edited.logic}
        handlers={logicRegistry.data?.handlers ?? []}
        loading={logicRegistry.loading}
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

// LogicSchemaSection renders the linked logic handler's input/output schema
// read-only. Tasks no longer own their I/O shape — it lives on the logic
// (struct tags + jsonschema manifest) and the logic registry page is the
// authoritative editor. We show it here for convenience: developers
// reading a task usually want to see what its inputs/outputs look like.
function LogicSchemaSection({
  logicName,
  handlers,
  loading,
}: {
  logicName: string;
  handlers: LogicHandler[];
  loading: boolean;
}) {
  const handler = useMemo(
    () => handlers.find((h) => h.name === logicName) ?? null,
    [handlers, logicName],
  );

  if (!logicName) {
    return (
      <DetailCard title="Inputs / outputs" style={{ marginBottom: 16 }}>
        <div className="meta">
          This task has no <code>logic</code> set, so it has no I/O contract.
          Set a logic above to bind one.
        </div>
      </DetailCard>
    );
  }

  if (loading) {
    return (
      <DetailCard title="Inputs / outputs" style={{ marginBottom: 16 }}>
        <div className="meta">loading logic schema…</div>
      </DetailCard>
    );
  }

  if (!handler) {
    return (
      <DetailCard title="Inputs / outputs" style={{ marginBottom: 16 }}>
        <div className="meta">
          No registered logic handler named <code>{logicName}</code>. The
          schema can't be shown until the logic is registered (the host
          binary builds the manifest at startup).
        </div>
      </DetailCard>
    );
  }

  return (
    <DetailCard title="Inputs / outputs" style={{ marginBottom: 16 }}>
      <div className="meta" style={{ marginBottom: 10 }}>
        Read-only. I/O shape lives on the logic handler{" "}
        <Link to={`/logic?name=${encodeURIComponent(handler.name)}`}>
          <code>{handler.name}</code>
        </Link>
        , not the task. Edit the Go logic to change it.
      </div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))",
          gap: 12,
        }}
      >
        <SchemaPanel
          title="Inputs"
          schema={handler.input_schema}
          emptyText="No inputs declared"
          tone="in"
          skippable={handler.skippable_inputs ?? []}
        />
        <SchemaPanel
          title="Outputs"
          schema={handler.output_schema}
          emptyText="No outputs declared"
          tone="out"
        />
      </div>
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
