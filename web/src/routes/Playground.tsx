import { useEffect, useMemo, useState } from "react";
import yaml from "js-yaml";
import { api, FlowSummary, PlaygroundResult, TaskSummary, TaskTrace } from "../api/client";
import { DraftEntry, deleteDraft, listDrafts, saveDraft } from "../lib/drafts";
import { useConfirmDialog } from "../components/useDialog";

// Playground — runs a flow against user-supplied inputs in a sandbox and
// shows per-task data so you can trace data flow and pinpoint failures
// without touching real backends.
//
// Two modes:
//   • Saved flow — pick a flow from the on-disk config and run it
//   • Custom — assemble a one-off linear flow from the task palette,
//     test it, and optionally save it as a local template that's also
//     selectable from the FlowGraph editor's "Insert flow as template"
//     picker.
//
// Caveat (echoed in the UI): this is a dry-run. The dashboard binary
// doesn't carry your service's logic handlers, so tasks land in the
// engine's "echo inputs as outputs" stub. Right for shape checking;
// not for end-to-end behaviour testing.

type Mode = "saved" | "custom";

export default function Playground() {
  const [mode, setMode] = useState<Mode>("saved");
  const [flows, setFlows] = useState<FlowSummary[]>([]);
  const [palette, setPalette] = useState<TaskSummary[]>([]);
  const [enabled, setEnabled] = useState<boolean | null>(null);

  useEffect(() => {
    api.info()
      .then((i) => setEnabled(!!i.playground))
      .catch(() => setEnabled(false));
    api.flows().then(setFlows).catch(() => {});
    api.tasks().then(setPalette).catch(() => {});
  }, []);

  if (enabled === null) return <div className="loading">Loading…</div>;
  if (!enabled) {
    return (
      <>
        <h1>Playground</h1>
        <div className="diag warning" style={{ maxWidth: 760 }}>
          The playground is disabled on this server. Start the dashboard with
          <code> --playground</code> (or set <code>JIGSAW_PLAYGROUND=true</code>)
          to enable it.
        </div>
      </>
    );
  }

  return (
    <>
      <h1>Playground</h1>
      <div className="meta" style={{ marginBottom: 12, maxWidth: 760 }}>
        Run a flow against test inputs in a sandbox. Provider lookups are
        stubbed and any logic handler not registered in this process
        echoes inputs as outputs — so you can see the data shape flowing
        through every task without hitting real backends.
      </div>

      <div style={{ display: "flex", gap: 6, marginBottom: 16 }}>
        <button
          className={`btn ${mode === "saved" ? "btn-primary" : ""}`}
          onClick={() => setMode("saved")}
        >
          Saved flow
        </button>
        <button
          className={`btn ${mode === "custom" ? "btn-primary" : ""}`}
          onClick={() => setMode("custom")}
        >
          Custom flow
        </button>
      </div>

      {mode === "saved" && <SavedFlowRunner flows={flows} />}
      {mode === "custom" && <CustomFlowRunner palette={palette} />}
    </>
  );
}

// ---------------------------------------------------------------------------
// Saved flow runner — picks an on-disk flow by name.
// ---------------------------------------------------------------------------

function SavedFlowRunner({ flows }: { flows: FlowSummary[] }) {
  const [flowName, setFlowName] = useState("");
  useEffect(() => {
    if (!flowName && flows.length > 0) setFlowName(flows[0].name);
  }, [flows, flowName]);

  return (
    <RunPanel
      label="Run flow"
      canRun={!!flowName}
      run={(inputs, sub) => api.playgroundRun(flowName, inputs, undefined, sub)}
      left={
        <>
          <label className="meta" style={{ display: "block" }}>flow</label>
          <select className="input" value={flowName} onChange={(e) => setFlowName(e.target.value)}>
            {flows.length === 0 && <option value="">no flows</option>}
            {flows.map((f) => <option key={f.name} value={f.name}>{f.name}</option>)}
          </select>
        </>
      }
    />
  );
}

// ---------------------------------------------------------------------------
// Custom flow runner — assemble a linear flow from the task palette, run,
// and optionally stash as a template usable from the FlowGraph editor.
//
// We keep the canvas-side concept of branches out of scope here: this is a
// straight-line list of tasks, which is what "I just want to test these
// tasks together" usually means. Parallel composition stays in the graph
// editor where it has proper UI.
// ---------------------------------------------------------------------------

interface CustomTask {
  taskName: string;
  label: string;
}

const TEMPLATE_SCOPE = "playground-template";
const TEMPLATE_BUCKET = "default";

function CustomFlowRunner({ palette }: { palette: TaskSummary[] }) {
  const [tasks, setTasks] = useState<CustomTask[]>([]);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [templates, setTemplates] = useState<DraftEntry[]>([]);
  const [saveOpen, setSaveOpen] = useState(false);
  const { confirm, ui: confirmUI } = useConfirmDialog();

  function refreshTemplates() {
    setTemplates(listDrafts(TEMPLATE_SCOPE, TEMPLATE_BUCKET));
  }
  useEffect(() => { refreshTemplates(); }, []);

  function addTask(name: string) {
    setTasks((cur) => [...cur, { taskName: name, label: "" }]);
  }
  function removeTask(i: number) {
    setTasks((cur) => cur.filter((_, idx) => idx !== i));
  }
  function move(i: number, delta: number) {
    setTasks((cur) => {
      const next = [...cur];
      const j = i + delta;
      if (j < 0 || j >= next.length) return cur;
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  }
  function setLabel(i: number, label: string) {
    setTasks((cur) => cur.map((t, idx) => idx === i ? { ...t, label } : t));
  }

  // Synthetic YAML the backend will accept.
  const flowYAML = useMemo(() => buildCustomFlowYAML(tasks), [tasks]);
  const ready = tasks.length > 0;

  function loadTemplate(entry: DraftEntry) {
    try {
      const parsed = yaml.load(entry.yaml) as { flows?: Array<{ tasks?: Array<{ name?: string; label?: string }> }> };
      const refs = parsed?.flows?.[0]?.tasks ?? [];
      const loaded: CustomTask[] = refs
        .filter((r) => typeof r.name === "string")
        .map((r) => ({ taskName: r.name!, label: r.label ?? "" }));
      setTasks(loaded);
    } catch {
      // shape mismatch — ignore; user will see the empty load
    }
  }

  async function deleteTemplate(entry: DraftEntry) {
    const ok = await confirm({
      title: "Delete template?",
      message: <>Delete the template <code>{entry.label}</code>?</>,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    deleteDraft(TEMPLATE_SCOPE, TEMPLATE_BUCKET, entry.id);
    refreshTemplates();
  }

  return (
    <>
      <RunPanel
        label="Run custom flow"
        canRun={ready}
        run={(inputs, sub) => api.playgroundRunYAML(flowYAML, inputs, undefined, sub)}
        left={
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
              <strong style={{ flex: 1 }}>Tasks</strong>
              <button className="btn" onClick={() => setPickerOpen(true)}>+ Task</button>
              <button
                className="btn"
                disabled={!ready}
                onClick={() => setSaveOpen(true)}
                title="Save this task list as a local template"
              >
                Save as template
              </button>
            </div>
            {tasks.length === 0 && <div className="empty" style={{ padding: 12 }}>No tasks yet. Add some.</div>}
            {tasks.map((t, i) => (
              <div key={i} style={{
                display: "flex", gap: 6, alignItems: "center",
                background: "var(--bg)", border: "1px solid var(--border)",
                borderRadius: 4, padding: 6,
              }}>
                <span style={{ width: 18, textAlign: "right", color: "var(--text-dim)", fontSize: 11 }}>{i + 1}.</span>
                <span style={{ fontFamily: "var(--mono)", flex: 1 }}>{t.taskName}</span>
                <input
                  className="input"
                  value={t.label}
                  onChange={(e) => setLabel(i, e.target.value)}
                  placeholder="label (optional)"
                  style={{ width: 140, padding: "2px 6px", fontSize: 11 }}
                />
                <button className="btn" onClick={() => move(i, -1)} disabled={i === 0} title="Move up" style={{ padding: "2px 6px" }}>↑</button>
                <button className="btn" onClick={() => move(i, 1)} disabled={i === tasks.length - 1} title="Move down" style={{ padding: "2px 6px" }}>↓</button>
                <button
                  className="btn"
                  onClick={() => removeTask(i)}
                  title="Remove"
                  style={{ padding: "2px 6px", color: "var(--error)", borderColor: "var(--error)" }}
                >
                  ×
                </button>
              </div>
            ))}

            {templates.length > 0 && (
              <>
                <strong style={{ marginTop: 8 }}>Templates</strong>
                <div className="meta">Local to this browser. Also selectable from the flow editor's "Insert flow as template" menu.</div>
                {templates.map((t) => (
                  <div key={t.id} style={{
                    display: "flex", gap: 6, alignItems: "center",
                    background: "var(--bg)", border: "1px solid var(--border)",
                    borderRadius: 4, padding: 6,
                  }}>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t.label}</div>
                      <div className="meta" style={{ fontSize: 10 }}>{new Date(t.savedAt).toLocaleString()}</div>
                    </div>
                    <button className="btn" onClick={() => loadTemplate(t)}>Load</button>
                    <button
                      className="btn"
                      onClick={() => deleteTemplate(t)}
                      style={{ color: "var(--error)", borderColor: "var(--error)" }}
                    >
                      Delete
                    </button>
                  </div>
                ))}
              </>
            )}
          </div>
        }
      />

      {pickerOpen && (
        <TaskPickerModal
          palette={palette}
          onPick={(name) => { addTask(name); setPickerOpen(false); }}
          onClose={() => setPickerOpen(false)}
        />
      )}

      {saveOpen && (
        <SaveTemplateModal
          defaultLabel={`custom @ ${new Date().toLocaleString()}`}
          onSave={(label) => {
            saveDraft(TEMPLATE_SCOPE, TEMPLATE_BUCKET, label, flowYAML);
            refreshTemplates();
            setSaveOpen(false);
          }}
          onClose={() => setSaveOpen(false)}
        />
      )}

      {confirmUI}
    </>
  );
}

function buildCustomFlowYAML(tasks: CustomTask[]): string {
  const doc = {
    flows: [
      {
        name: "playground_custom",
        description: "Ad-hoc flow assembled in the playground",
        tasks: tasks.map((t) => t.label
          ? { name: t.taskName, label: t.label }
          : { name: t.taskName }),
      },
    ],
  };
  return yaml.dump(doc, { lineWidth: 100, noRefs: true });
}

function TaskPickerModal({
  palette,
  onPick,
  onClose,
}: {
  palette: TaskSummary[];
  onPick: (name: string) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const filtered = palette
    .filter((t) => t.name.toLowerCase().includes(q.toLowerCase()))
    .slice(0, 50);
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: 120, zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 480, maxHeight: "60vh", overflow: "hidden",
          display: "flex", flexDirection: "column",
        }}
      >
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="search tasks…"
          style={{
            background: "transparent", border: 0,
            borderBottom: "1px solid var(--border)",
            padding: "12px 16px", color: "var(--text)", fontSize: 14, outline: "none",
          }}
        />
        <div style={{ overflow: "auto", flex: 1 }}>
          {filtered.length === 0 && <div className="empty" style={{ padding: 24 }}>no matches</div>}
          {filtered.map((t) => (
            <div
              key={t.name}
              onClick={() => onPick(t.name)}
              style={{ padding: "8px 16px", cursor: "pointer", borderBottom: "1px solid var(--border)" }}
              onMouseEnter={(e) => (e.currentTarget.style.background = "var(--panel-2)")}
              onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
            >
              <div style={{ fontWeight: 500 }}>{t.name}</div>
              {t.description && <div className="meta">{t.description}</div>}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function SaveTemplateModal({
  defaultLabel,
  onSave,
  onClose,
}: {
  defaultLabel: string;
  onSave: (label: string) => void;
  onClose: () => void;
}) {
  const [label, setLabel] = useState(defaultLabel);
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: 120, zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 460, overflow: "hidden",
          display: "flex", flexDirection: "column",
        }}
      >
        <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)" }}>
          <strong>Save as template</strong>
          <div className="meta">Stored locally. Available from the flow editor's "Insert flow as template" picker.</div>
        </div>
        <div style={{ padding: 16 }}>
          <label className="meta" style={{ display: "block", marginBottom: 6 }}>Label</label>
          <input
            autoFocus
            className="input"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") onSave(label); }}
            style={{ width: "100%" }}
          />
        </div>
        <div style={{ padding: "12px 16px", borderTop: "1px solid var(--border)", display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary" onClick={() => onSave(label)}>Save</button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// RunPanel — shared shell: inputs editor on the left, trace on the right.
// The caller plugs in its own selector (saved flow dropdown or custom
// builder) via the `left` slot and the `run` function that produces a
// PlaygroundResult.
// ---------------------------------------------------------------------------

function RunPanel({
  label,
  canRun,
  run,
  left,
}: {
  label: string;
  canRun: boolean;
  run: (inputs: Record<string, unknown>, sub: number) => Promise<{ status: number; data: PlaygroundResult }>;
  left: React.ReactNode;
}) {
  const [sub, setSub] = useState(0);
  const [inputsText, setInputsText] = useState("{\n  \n}\n");
  const [parseErr, setParseErr] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [reqErr, setReqErr] = useState<string | null>(null);
  const [result, setResult] = useState<PlaygroundResult | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  function parseInputs(): Record<string, unknown> | null {
    const trimmed = inputsText.trim();
    if (!trimmed) return {};
    try {
      const v = JSON.parse(trimmed);
      if (v && typeof v === "object" && !Array.isArray(v)) {
        setParseErr(null);
        return v as Record<string, unknown>;
      }
      setParseErr("inputs must be a JSON object");
      return null;
    } catch (e) {
      setParseErr((e as Error).message);
      return null;
    }
  }

  async function doRun() {
    const inputs = parseInputs();
    if (inputs === null) return;
    setRunning(true);
    setReqErr(null);
    setResult(null);
    setExpanded(new Set());
    try {
      const { status, data } = await run(inputs, sub);
      if (status !== 200) setReqErr(`server returned ${status}`);
      setResult(data);
    } catch (e) {
      setReqErr((e as Error).message);
    } finally {
      setRunning(false);
    }
  }

  function toggle(k: string) {
    setExpanded((cur) => {
      const next = new Set(cur);
      if (next.has(k)) next.delete(k); else next.add(k);
      return next;
    });
  }

  return (
    <div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1.4fr)", gap: 16 }}>
      <section className="row" style={{ flexDirection: "column", alignItems: "stretch", gap: 12 }}>
        <h2 style={{ margin: 0 }}>Inputs</h2>
        {left}

        <label className="meta" style={{ display: "block" }}>sub <span style={{ opacity: 0.6 }}>(endpoint variant)</span></label>
        <input
          className="input"
          type="number"
          min={0}
          value={sub}
          onChange={(e) => setSub(Number(e.target.value || 0))}
          style={{ width: 120 }}
        />

        <label className="meta" style={{ display: "block" }}>inputs <span style={{ opacity: 0.6 }}>(JSON object)</span></label>
        <textarea
          className="input"
          value={inputsText}
          onChange={(e) => setInputsText(e.target.value)}
          rows={10}
          style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
        />
        {parseErr && <div className="diag error">JSON: {parseErr}</div>}

        <div style={{ display: "flex", gap: 8 }}>
          <button className="btn btn-primary" onClick={doRun} disabled={!canRun || running}>
            {running ? "Running…" : label}
          </button>
          {result && (
            <span className="meta" style={{ alignSelf: "center" }}>
              {result.status} · {result.tasks.length} task{result.tasks.length === 1 ? "" : "s"}
            </span>
          )}
        </div>
        {reqErr && <div className="diag error">{reqErr}</div>}
      </section>

      <section className="row" style={{ flexDirection: "column", alignItems: "stretch", gap: 12, minWidth: 0 }}>
        <h2 style={{ margin: 0 }}>Trace</h2>
        {!result ? (
          <div className="empty">Run to see per-task data.</div>
        ) : (
          <>
            {result.error && <div className="diag error">{result.error}</div>}
            {result.tasks.length === 0 && <div className="empty">No tasks executed.</div>}
            {result.tasks.map((t, i) => (
              <TaskRow key={`${t.name}-${i}`} trace={t} open={expanded.has(`${t.name}-${i}`)} onToggle={() => toggle(`${t.name}-${i}`)} />
            ))}
            {result.result !== undefined && (
              <details style={{ background: "var(--bg)", border: "1px solid var(--border)", borderRadius: 4, padding: 8 }}>
                <summary style={{ cursor: "pointer", fontWeight: 500 }}>final result</summary>
                <pre style={{ marginTop: 8, fontSize: 11, whiteSpace: "pre-wrap" }}>
                  {jsonPretty(result.result)}
                </pre>
              </details>
            )}
          </>
        )}
      </section>
    </div>
  );
}

function TaskRow({ trace, open, onToggle }: { trace: TaskTrace; open: boolean; onToggle: () => void }) {
  const failed = trace.status === "failed" || !!trace.error;
  const color = failed ? "var(--error)" : trace.skipped ? "var(--text-dim)" : "var(--accent)";
  return (
    <div style={{
      border: `1px solid ${failed ? "var(--error)" : "var(--border)"}`,
      borderRadius: 4,
      background: "var(--bg)",
      overflow: "hidden",
    }}>
      <button
        onClick={onToggle}
        style={{
          width: "100%", textAlign: "left", padding: "8px 12px",
          display: "flex", alignItems: "center", gap: 10,
          background: "transparent", border: 0, color: "var(--text)",
          cursor: "pointer", fontFamily: "inherit",
        }}
      >
        <span style={{ color, fontSize: 14 }}>{open ? "▾" : "▸"}</span>
        <span style={{ fontFamily: "var(--mono)", fontWeight: 500 }}>{trace.name}</span>
        {trace.label && <span className="badge">@{trace.label}</span>}
        {trace.provider && <span className="badge" style={{ color: "#a07cf0" }}>{trace.provider}</span>}
        <span className="meta" style={{ marginLeft: "auto" }}>
          <span style={{ color }}>{trace.status}</span>
          {trace.duration_ms > 0 ? ` · ${trace.duration_ms}ms` : ""}
          {trace.fallback_used ? " · fallback" : ""}
          {trace.skipped ? " · skipped" : ""}
        </span>
      </button>
      {open && (
        <div style={{ padding: "8px 12px", borderTop: "1px solid var(--border)", display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
          <div>
            <div className="meta" style={{ marginBottom: 4 }}>inputs</div>
            <pre style={{
              margin: 0, fontSize: 11, fontFamily: "var(--mono)",
              whiteSpace: "pre-wrap", maxHeight: 300, overflow: "auto",
            }}>
              {jsonPretty(trace.inputs ?? {})}
            </pre>
          </div>
          <div>
            <div className="meta" style={{ marginBottom: 4 }}>outputs</div>
            <pre style={{
              margin: 0, fontSize: 11, fontFamily: "var(--mono)",
              whiteSpace: "pre-wrap", maxHeight: 300, overflow: "auto",
            }}>
              {jsonPretty(trace.outputs ?? {})}
            </pre>
          </div>
          {trace.error && (
            <div style={{ gridColumn: "1 / -1" }}>
              <div className="meta" style={{ marginBottom: 4, color: "var(--error)" }}>error</div>
              <pre style={{ margin: 0, fontSize: 11, color: "var(--error)" }}>{trace.error}</pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function jsonPretty(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}
