import React, { Fragment, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import yaml from "js-yaml";
import {
  api,
  FlowSummary,
  LogicHandler,
  PlaygroundResult,
  TaskSummary,
  TaskTrace,
} from "../api/client";
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

type Mode = "saved" | "custom" | "task" | "logic";

// SessionStorage key prefix for the "Test in playground" handoff used by
// the FlowGraph editor: it stashes the current (possibly unsaved) YAML
// under a random key and navigates with ?customKey=<key>. Survives a tab
// reload but not a browser restart.
const CUSTOM_HANDOFF_PREFIX = "playground:custom:";

export default function Playground() {
  const [searchParams, setSearchParams] = useSearchParams();
  const initialFlow = searchParams.get("flow") ?? "";
  const initialTask = searchParams.get("task") ?? "";
  const initialLogic = searchParams.get("logic") ?? "";
  const initialCustomKey = searchParams.get("customKey") ?? "";
  const initialCustomYAML = useMemo(() => {
    if (!initialCustomKey) return "";
    try {
      return (
        sessionStorage.getItem(CUSTOM_HANDOFF_PREFIX + initialCustomKey) ?? ""
      );
    } catch {
      return "";
    }
  }, [initialCustomKey]);

  const [mode, setMode] = useState<Mode>(() => {
    if (initialCustomKey) return "custom";
    if (initialTask) return "task";
    if (initialLogic) return "logic";
    return "saved";
  });
  const [flows, setFlows] = useState<FlowSummary[]>([]);
  const [palette, setPalette] = useState<TaskSummary[]>([]);
  const [logics, setLogics] = useState<LogicHandler[]>([]);
  const [enabled, setEnabled] = useState<boolean | null>(null);

  useEffect(() => {
    api
      .info()
      .then((i) => setEnabled(!!i.playground))
      .catch(() => setEnabled(false));
    api
      .flows()
      .then(setFlows)
      .catch(() => {});
    api
      .tasks()
      .then(setPalette)
      .catch(() => {});
    api
      .logic()
      .then((r) => setLogics(r.handlers ?? []))
      .catch(() => {});
  }, []);

  // Strip handoff keys from the URL once consumed so a page reload doesn't
  // try to re-read sessionStorage (which we also clear below).
  useEffect(() => {
    if (initialCustomKey && initialCustomYAML) {
      try {
        sessionStorage.removeItem(CUSTOM_HANDOFF_PREFIX + initialCustomKey);
      } catch {
        /* ignore */
      }
      const next = new URLSearchParams(searchParams);
      next.delete("customKey");
      setSearchParams(next, { replace: true });
    }
    // run-once on mount: dependencies intentionally omitted
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (enabled === null) return <div className="loading">Loading…</div>;
  if (!enabled) {
    return (
      <>
        <h1>Playground</h1>
        <div className="diag warning" style={{ maxWidth: 760 }}>
          The playground is disabled on this server. Start the dashboard with
          <code> --playground</code> (or set <code>JIGSAW_PLAYGROUND=true</code>
          ) to enable it.
        </div>
      </>
    );
  }

  return (
    <>
      <h1>Playground</h1>
      <div className="meta" style={{ marginBottom: 12, maxWidth: 760 }}>
        Run a flow against test inputs in a sandbox. Provider lookups are
        stubbed and any logic handler not registered in this process echoes
        inputs as outputs — so you can see the data shape flowing through every
        task without hitting real backends.
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
        <button
          className={`btn ${mode === "task" ? "btn-primary" : ""}`}
          onClick={() => setMode("task")}
        >
          Single task
        </button>
        <button
          className={`btn ${mode === "logic" ? "btn-primary" : ""}`}
          onClick={() => setMode("logic")}
        >
          Single logic
        </button>
      </div>

      {mode === "saved" && (
        <SavedFlowRunner flows={flows} initialFlow={initialFlow} />
      )}
      {mode === "custom" && (
        <CustomFlowRunner palette={palette} initialYAML={initialCustomYAML} />
      )}
      {mode === "task" && (
        <SingleTaskRunner palette={palette} initialTask={initialTask} />
      )}
      {mode === "logic" && (
        <SingleLogicRunner logics={logics} initialLogic={initialLogic} />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Saved flow runner — picks an on-disk flow by name.
// ---------------------------------------------------------------------------

function SavedFlowRunner({
  flows,
  initialFlow,
}: {
  flows: FlowSummary[];
  initialFlow: string;
}) {
  const [flowName, setFlowName] = useState(initialFlow);
  useEffect(() => {
    if (!flowName && flows.length > 0) setFlowName(flows[0].name);
  }, [flows, flowName]);

  return (
    <RunPanel
      label="Run flow"
      canRun={!!flowName}
      run={(inputs, headers, sub) =>
        api.playgroundRun(flowName, inputs, headers, sub)
      }
      left={
        <>
          <label className="meta" style={{ display: "block" }}>
            flow
          </label>
          <select
            className="input"
            value={flowName}
            onChange={(e) => setFlowName(e.target.value)}
          >
            {flows.length === 0 && <option value="">no flows</option>}
            {flows.map((f) => (
              <option key={f.name} value={f.name}>
                {f.name}
              </option>
            ))}
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

function CustomFlowRunner({
  palette,
  initialYAML,
}: {
  palette: TaskSummary[];
  initialYAML: string;
}) {
  const [tasks, setTasks] = useState<CustomTask[]>(() =>
    parseHandoffYAML(initialYAML),
  );
  // initialYAML may carry parallel blocks the linear-list editor can't
  // represent. We keep the original so we send it to the backend
  // unchanged for the first run, and let the user fall back to editing
  // a linearised version (or open it in the flow editor) if they want.
  const [rawYAML, setRawYAML] = useState<string>(initialYAML);
  const [rawMode, setRawMode] = useState<boolean>(
    !!initialYAML && parseHandoffYAML(initialYAML).length === 0,
  );
  const [pickerOpen, setPickerOpen] = useState(false);
  const [templates, setTemplates] = useState<DraftEntry[]>([]);
  const [saveOpen, setSaveOpen] = useState(false);
  const { confirm, ui: confirmUI } = useConfirmDialog();

  function refreshTemplates() {
    setTemplates(listDrafts(TEMPLATE_SCOPE, TEMPLATE_BUCKET));
  }
  useEffect(() => {
    refreshTemplates();
  }, []);

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
    setTasks((cur) => cur.map((t, idx) => (idx === i ? { ...t, label } : t)));
  }

  // Synthetic YAML the backend will accept. In rawMode we ship the user's
  // own YAML (the "Test from flow editor" handoff sets this); otherwise we
  // build a linear YAML from the tasks list above.
  const flowYAML = useMemo(
    () => (rawMode ? rawYAML : buildCustomFlowYAML(tasks)),
    [rawMode, rawYAML, tasks],
  );
  const ready = rawMode ? rawYAML.trim().length > 0 : tasks.length > 0;

  function loadTemplate(entry: DraftEntry) {
    try {
      const parsed = yaml.load(entry.yaml) as {
        flows?: Array<{ tasks?: Array<{ name?: string; label?: string }> }>;
      };
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
      message: (
        <>
          Delete the template <code>{entry.label}</code>?
        </>
      ),
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
        run={(inputs, headers, sub) =>
          api.playgroundRunYAML(flowYAML, inputs, headers, sub)
        }
        left={
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
              <strong style={{ flex: 1 }}>Tasks</strong>
              <button
                className={`btn ${rawMode ? "" : "btn-primary"}`}
                onClick={() => setRawMode(false)}
                style={{ padding: "2px 8px", fontSize: 11 }}
                title="Build a linear flow from the task palette"
              >
                List
              </button>
              <button
                className={`btn ${rawMode ? "btn-primary" : ""}`}
                onClick={() => setRawMode(true)}
                style={{ padding: "2px 8px", fontSize: 11 }}
                title="Edit raw flow YAML (parallel blocks, bindings, params)"
              >
                YAML
              </button>
              {!rawMode && (
                <button className="btn" onClick={() => setPickerOpen(true)}>
                  + Task
                </button>
              )}
              <button
                className="btn"
                disabled={!ready}
                onClick={() => setSaveOpen(true)}
                title="Save this task list as a local template"
              >
                Save as template
              </button>
            </div>
            {rawMode ? (
              <>
                <div className="meta">
                  Paste a <code>{`{flows: [{tasks: [...]}]}`}</code> doc. Used
                  as-is — supports parallel blocks, bindings, params.
                </div>
                <textarea
                  className="input"
                  value={rawYAML}
                  onChange={(e) => setRawYAML(e.target.value)}
                  rows={14}
                  style={{
                    width: "100%",
                    fontFamily: "var(--mono)",
                    fontSize: 12,
                  }}
                  placeholder={
                    "flows:\n  - name: my-flow\n    tasks:\n      - name: my-task\n"
                  }
                />
              </>
            ) : (
              <>
                {tasks.length === 0 && (
                  <div className="empty" style={{ padding: 12 }}>
                    No tasks yet. Add some.
                  </div>
                )}
                {tasks.map((t, i) => (
                  <div
                    key={i}
                    style={{
                      display: "flex",
                      gap: 6,
                      alignItems: "center",
                      background: "var(--bg)",
                      border: "1px solid var(--border)",
                      borderRadius: 4,
                      padding: 6,
                    }}
                  >
                    <span
                      style={{
                        width: 18,
                        textAlign: "right",
                        color: "var(--text-dim)",
                        fontSize: 11,
                      }}
                    >
                      {i + 1}.
                    </span>
                    <span style={{ fontFamily: "var(--mono)", flex: 1 }}>
                      {t.taskName}
                    </span>
                    <input
                      className="input"
                      value={t.label}
                      onChange={(e) => setLabel(i, e.target.value)}
                      placeholder="label (optional)"
                      style={{ width: 140, padding: "2px 6px", fontSize: 11 }}
                    />
                    <button
                      className="btn"
                      onClick={() => move(i, -1)}
                      disabled={i === 0}
                      title="Move up"
                      style={{ padding: "2px 6px" }}
                    >
                      ↑
                    </button>
                    <button
                      className="btn"
                      onClick={() => move(i, 1)}
                      disabled={i === tasks.length - 1}
                      title="Move down"
                      style={{ padding: "2px 6px" }}
                    >
                      ↓
                    </button>
                    <button
                      className="btn"
                      onClick={() => removeTask(i)}
                      title="Remove"
                      style={{
                        padding: "2px 6px",
                        color: "var(--error)",
                        borderColor: "var(--error)",
                      }}
                    >
                      ×
                    </button>
                  </div>
                ))}

                {templates.length > 0 && (
                  <>
                    <strong style={{ marginTop: 8 }}>Templates</strong>
                    <div className="meta">
                      Local to this browser. Also selectable from the flow
                      editor's "Insert flow as template" menu.
                    </div>
                    {templates.map((t) => (
                      <div
                        key={t.id}
                        style={{
                          display: "flex",
                          gap: 6,
                          alignItems: "center",
                          background: "var(--bg)",
                          border: "1px solid var(--border)",
                          borderRadius: 4,
                          padding: 6,
                        }}
                      >
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div
                            style={{
                              overflow: "hidden",
                              textOverflow: "ellipsis",
                              whiteSpace: "nowrap",
                            }}
                          >
                            {t.label}
                          </div>
                          <div className="meta" style={{ fontSize: 10 }}>
                            {new Date(t.savedAt).toLocaleString()}
                          </div>
                        </div>
                        <button className="btn" onClick={() => loadTemplate(t)}>
                          Load
                        </button>
                        <button
                          className="btn"
                          onClick={() => deleteTemplate(t)}
                          style={{
                            color: "var(--error)",
                            borderColor: "var(--error)",
                          }}
                        >
                          Delete
                        </button>
                      </div>
                    ))}
                  </>
                )}
              </>
            )}
          </div>
        }
      />

      {pickerOpen && (
        <TaskPickerModal
          palette={palette}
          onPick={(name) => {
            addTask(name);
            setPickerOpen(false);
          }}
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

// parseHandoffYAML attempts to read a `{flows: [{tasks: [...]}]}` doc and
// return a linear CustomTask[] when every entry is a simple task ref. If
// the flow uses parallel blocks (or anything else this editor can't model)
// it returns an empty array — the caller falls back to raw-YAML mode and
// edits the doc directly.
function parseHandoffYAML(raw: string): CustomTask[] {
  if (!raw) return [];
  try {
    const parsed = yaml.load(raw) as {
      flows?: Array<{
        tasks?: Array<{ name?: string; label?: string; parallel?: unknown }>;
      }>;
    };
    const refs = parsed?.flows?.[0]?.tasks ?? [];
    const linear: CustomTask[] = [];
    for (const r of refs) {
      if (typeof r.name !== "string" || r.parallel) return [];
      linear.push({ taskName: r.name, label: r.label ?? "" });
    }
    return linear;
  } catch {
    return [];
  }
}

function buildCustomFlowYAML(tasks: CustomTask[]): string {
  const doc = {
    flows: [
      {
        name: "playground_custom",
        description: "Ad-hoc flow assembled in the playground",
        tasks: tasks.map((t) =>
          t.label ? { name: t.taskName, label: t.label } : { name: t.taskName },
        ),
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
        position: "fixed",
        inset: 0,
        background: "#000a",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "center",
        paddingTop: 120,
        zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)",
          border: "1px solid var(--border-strong)",
          borderRadius: 6,
          width: 480,
          maxHeight: "60vh",
          overflow: "hidden",
          display: "flex",
          flexDirection: "column",
        }}
      >
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="search tasks…"
          style={{
            background: "transparent",
            border: 0,
            borderBottom: "1px solid var(--border)",
            padding: "12px 16px",
            color: "var(--text)",
            fontSize: 14,
            outline: "none",
          }}
        />
        <div style={{ overflow: "auto", flex: 1 }}>
          {filtered.length === 0 && (
            <div className="empty" style={{ padding: 24 }}>
              no matches
            </div>
          )}
          {filtered.map((t) => (
            <div
              key={t.name}
              onClick={() => onPick(t.name)}
              style={{
                padding: "8px 16px",
                cursor: "pointer",
                borderBottom: "1px solid var(--border)",
              }}
              onMouseEnter={(e) =>
                (e.currentTarget.style.background = "var(--panel-2)")
              }
              onMouseLeave={(e) =>
                (e.currentTarget.style.background = "transparent")
              }
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
        position: "fixed",
        inset: 0,
        background: "#000a",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "center",
        paddingTop: 120,
        zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)",
          border: "1px solid var(--border-strong)",
          borderRadius: 6,
          width: 460,
          overflow: "hidden",
          display: "flex",
          flexDirection: "column",
        }}
      >
        <div
          style={{
            padding: "12px 16px",
            borderBottom: "1px solid var(--border)",
          }}
        >
          <strong>Save as template</strong>
          <div className="meta">
            Stored locally. Available from the flow editor's "Insert flow as
            template" picker.
          </div>
        </div>
        <div style={{ padding: 16 }}>
          <label className="meta" style={{ display: "block", marginBottom: 6 }}>
            Label
          </label>
          <input
            autoFocus
            className="input"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") onSave(label);
            }}
            style={{ width: "100%" }}
          />
        </div>
        <div
          style={{
            padding: "12px 16px",
            borderTop: "1px solid var(--border)",
            display: "flex",
            justifyContent: "flex-end",
            gap: 8,
          }}
        >
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" onClick={() => onSave(label)}>
            Save
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// SingleTaskRunner — run one task in isolation. The backend wraps it in a
// synthetic one-task flow so the existing dry-run executor + stub providers
// path is reused. Useful for verifying input/output shape without typing
// out a flow YAML.
// ---------------------------------------------------------------------------

function SingleTaskRunner({
  palette,
  initialTask,
}: {
  palette: TaskSummary[];
  initialTask: string;
}) {
  const [taskName, setTaskName] = useState(initialTask);
  const [paramsText, setParamsText] = useState("{\n  \n}\n");
  const [paramsErr, setParamsErr] = useState<string | null>(null);
  useEffect(() => {
    if (!taskName && palette.length > 0) setTaskName(palette[0].name);
  }, [palette, taskName]);

  function parseParams(): Record<string, unknown> | null {
    const trimmed = paramsText.trim();
    if (!trimmed) return {};
    try {
      const v = JSON.parse(trimmed);
      if (v && typeof v === "object" && !Array.isArray(v)) {
        setParamsErr(null);
        return v as Record<string, unknown>;
      }
      setParamsErr("params must be a JSON object");
      return null;
    } catch (e) {
      setParamsErr((e as Error).message);
      return null;
    }
  }

  return (
    <RunPanel
      label="Run task"
      canRun={!!taskName}
      run={(inputs, headers, sub) => {
        const params = parseParams();
        if (params === null)
          return Promise.reject(
            new Error("params: " + (paramsErr ?? "invalid JSON")),
          );
        return api.playgroundTask(taskName, inputs, headers, params, sub);
      }}
      left={
        <>
          <label className="meta" style={{ display: "block" }}>
            task
          </label>
          <select
            className="input"
            value={taskName}
            onChange={(e) => setTaskName(e.target.value)}
          >
            {palette.length === 0 && <option value="">no tasks</option>}
            {palette.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
          <label className="meta" style={{ display: "block" }}>
            params{" "}
            <span style={{ opacity: 0.6 }}>(JSON, overrides task params)</span>
          </label>
          <textarea
            className="input"
            value={paramsText}
            onChange={(e) => setParamsText(e.target.value)}
            rows={4}
            style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
          />
          {paramsErr && <div className="diag error">params: {paramsErr}</div>}
        </>
      }
    />
  );
}

// ---------------------------------------------------------------------------
// SingleLogicRunner — run one logic handler in isolation. Mirror of
// SingleTaskRunner but the backend synthesizes the task wrapping the logic.
// If the dashboard binary doesn't carry the real handler (the common case)
// the engine's echo-inputs fallback fires — still useful for shape checks.
// ---------------------------------------------------------------------------

function SingleLogicRunner({
  logics,
  initialLogic,
}: {
  logics: LogicHandler[];
  initialLogic: string;
}) {
  const [logicName, setLogicName] = useState(initialLogic);
  const [paramsText, setParamsText] = useState("{\n  \n}\n");
  const [paramsErr, setParamsErr] = useState<string | null>(null);
  useEffect(() => {
    if (!logicName && logics.length > 0) setLogicName(logics[0].name);
  }, [logics, logicName]);

  function parseParams(): Record<string, unknown> | null {
    const trimmed = paramsText.trim();
    if (!trimmed) return {};
    try {
      const v = JSON.parse(trimmed);
      if (v && typeof v === "object" && !Array.isArray(v)) {
        setParamsErr(null);
        return v as Record<string, unknown>;
      }
      setParamsErr("params must be a JSON object");
      return null;
    } catch (e) {
      setParamsErr((e as Error).message);
      return null;
    }
  }

  return (
    <RunPanel
      label="Run logic"
      canRun={!!logicName}
      run={(inputs, headers, sub) => {
        const params = parseParams();
        if (params === null)
          return Promise.reject(
            new Error("params: " + (paramsErr ?? "invalid JSON")),
          );
        return api.playgroundLogic(logicName, inputs, headers, params, sub);
      }}
      left={
        <>
          <label className="meta" style={{ display: "block" }}>
            logic
          </label>
          <select
            className="input"
            value={logicName}
            onChange={(e) => setLogicName(e.target.value)}
          >
            {logics.length === 0 && (
              <option value="">no logic handlers (manifest not loaded)</option>
            )}
            {logics.map((l) => (
              <option key={l.name} value={l.name}>
                {l.name}
              </option>
            ))}
          </select>
          <label className="meta" style={{ display: "block" }}>
            params <span style={{ opacity: 0.6 }}>(JSON)</span>
          </label>
          <textarea
            className="input"
            value={paramsText}
            onChange={(e) => setParamsText(e.target.value)}
            rows={4}
            style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
          />
          {paramsErr && <div className="diag error">params: {paramsErr}</div>}
        </>
      }
    />
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
  run: (
    inputs: Record<string, unknown>,
    headers: Record<string, string>,
    sub: number,
  ) => Promise<{ status: number; data: PlaygroundResult }>;
  left: React.ReactNode;
}) {
  const [sub, setSub] = useState(0);
  const [inputsText, setInputsText] = useState("{\n  \n}\n");
  const [parseErr, setParseErr] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [reqErr, setReqErr] = useState<string | null>(null);
  const [result, setResult] = useState<PlaygroundResult | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  // Headers: two-mode editor. "rows" lets the user type key/value pairs
  // (the common case for HTTP headers), "json" lets them paste a saved
  // headers blob. Both reduce to a flat string→string map at run time.
  const [hdrMode, setHdrMode] = useState<"rows" | "json">("rows");
  const [hdrRows, setHdrRows] = useState<{ key: string; value: string }[]>([]);
  const [hdrText, setHdrText] = useState("{\n  \n}\n");
  const [hdrErr, setHdrErr] = useState<string | null>(null);

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

  function parseHeaders(): Record<string, string> | null {
    if (hdrMode === "rows") {
      const out: Record<string, string> = {};
      for (const r of hdrRows) {
        const k = r.key.trim();
        if (!k) continue;
        out[k] = r.value;
      }
      setHdrErr(null);
      return out;
    }
    const trimmed = hdrText.trim();
    if (!trimmed) {
      setHdrErr(null);
      return {};
    }
    try {
      const v = JSON.parse(trimmed);
      if (!v || typeof v !== "object" || Array.isArray(v)) {
        setHdrErr("headers must be a JSON object of string→string");
        return null;
      }
      const out: Record<string, string> = {};
      for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
        if (typeof val !== "string") {
          setHdrErr(`header ${JSON.stringify(k)} must be a string`);
          return null;
        }
        out[k] = val;
      }
      setHdrErr(null);
      return out;
    } catch (e) {
      setHdrErr((e as Error).message);
      return null;
    }
  }

  async function doRun() {
    const inputs = parseInputs();
    if (inputs === null) return;
    const headers = parseHeaders();
    if (headers === null) return;
    setRunning(true);
    setReqErr(null);
    setResult(null);
    setExpanded(new Set());
    try {
      const { status, data } = await run(inputs, headers, sub);
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
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  }

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1.4fr)",
        gap: 16,
      }}
    >
      <section
        className="row"
        style={{
          flexDirection: "column",
          alignItems: "stretch",
          gap: 12,
          justifyContent: "flex-start",
        }}
      >
        <h2 style={{ margin: 0 }}>Inputs</h2>
        {left}

        <label className="meta" style={{ display: "block" }}>
          sub <span style={{ opacity: 0.6 }}>(endpoint variant)</span>
        </label>
        <input
          className="input"
          type="number"
          min={0}
          value={sub}
          onChange={(e) => setSub(Number(e.target.value || 0))}
          style={{ width: 120 }}
        />

        <label className="meta" style={{ display: "block" }}>
          inputs <span style={{ opacity: 0.6 }}>(JSON object)</span>
        </label>
        <textarea
          className="input"
          value={inputsText}
          onChange={(e) => setInputsText(e.target.value)}
          rows={10}
          style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
        />
        {parseErr && <div className="diag error">JSON: {parseErr}</div>}

        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            marginTop: 4,
          }}
        >
          <label className="meta" style={{ flex: 1 }}>
            headers{" "}
            <span style={{ opacity: 0.6 }}>(forwarded as request headers)</span>
          </label>
          <div style={{ display: "flex", gap: 4 }}>
            <button
              className={`btn ${hdrMode === "rows" ? "btn-primary" : ""}`}
              onClick={() => setHdrMode("rows")}
              style={{ padding: "2px 8px", fontSize: 11 }}
              type="button"
            >
              Form
            </button>
            <button
              className={`btn ${hdrMode === "json" ? "btn-primary" : ""}`}
              onClick={() => setHdrMode("json")}
              style={{ padding: "2px 8px", fontSize: 11 }}
              type="button"
            >
              JSON
            </button>
          </div>
        </div>
        {hdrMode === "rows" ? (
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {hdrRows.length === 0 && (
              <div className="empty" style={{ padding: 8, fontSize: 11 }}>
                No headers. Add one below.
              </div>
            )}
            {hdrRows.map((row, i) => (
              <div key={i} style={{ display: "flex", gap: 4 }}>
                <input
                  className="input"
                  placeholder="key"
                  value={row.key}
                  onChange={(e) =>
                    setHdrRows((cur) =>
                      cur.map((r, idx) =>
                        idx === i ? { ...r, key: e.target.value } : r,
                      ),
                    )
                  }
                  style={{ flex: 1, fontSize: 12, fontFamily: "var(--mono)" }}
                />
                <input
                  className="input"
                  placeholder="value"
                  value={row.value}
                  onChange={(e) =>
                    setHdrRows((cur) =>
                      cur.map((r, idx) =>
                        idx === i ? { ...r, value: e.target.value } : r,
                      ),
                    )
                  }
                  style={{ flex: 2, fontSize: 12, fontFamily: "var(--mono)" }}
                />
                <button
                  className="btn"
                  type="button"
                  onClick={() =>
                    setHdrRows((cur) => cur.filter((_, idx) => idx !== i))
                  }
                  style={{
                    padding: "2px 8px",
                    color: "var(--error)",
                    borderColor: "var(--error)",
                  }}
                  title="Remove"
                >
                  ×
                </button>
              </div>
            ))}
            <button
              className="btn"
              type="button"
              onClick={() =>
                setHdrRows((cur) => [...cur, { key: "", value: "" }])
              }
              style={{
                alignSelf: "flex-start",
                padding: "2px 8px",
                fontSize: 11,
              }}
            >
              + Header
            </button>
          </div>
        ) : (
          <textarea
            className="input"
            value={hdrText}
            onChange={(e) => setHdrText(e.target.value)}
            rows={5}
            style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
          />
        )}
        {hdrErr && <div className="diag error">headers: {hdrErr}</div>}

        <div style={{ display: "flex", gap: 8 }}>
          <button
            className="btn btn-primary"
            onClick={doRun}
            disabled={!canRun || running}
          >
            {running ? "Running…" : label}
          </button>
          {result && (
            <span className="meta" style={{ alignSelf: "center" }}>
              {result.status} · {result.tasks.length} task
              {result.tasks.length === 1 ? "" : "s"}
            </span>
          )}
        </div>
        {reqErr && <div className="diag error">{reqErr}</div>}
      </section>

      <section
        className="row"
        style={{
          flexDirection: "column",
          alignItems: "stretch",
          gap: 12,
          minWidth: 0,
        }}
      >
        <h2 style={{ margin: 0 }}>Trace</h2>
        {!result ? (
          <div className="empty">Run to see per-task data.</div>
        ) : (
          <>
            {result.error && <div className="diag error">{result.error}</div>}
            {result.tasks.length === 0 && (
              <div className="empty">No tasks executed.</div>
            )}
            {result.tasks.map((t, i) => (
              <TaskRow
                key={`${t.name}-${i}`}
                trace={t}
                open={expanded.has(`${t.name}-${i}`)}
                onToggle={() => toggle(`${t.name}-${i}`)}
              />
            ))}
            {result.result !== undefined && (
              <details
                style={{
                  background: "var(--bg)",
                  border: "1px solid var(--border)",
                  borderRadius: 4,
                  padding: 8,
                }}
              >
                <summary style={{ cursor: "pointer", fontWeight: 500 }}>
                  final result
                </summary>
                <pre
                  style={{
                    marginTop: 8,
                    fontSize: 11,
                    whiteSpace: "pre-wrap",
                    overflow: "scroll",
                  }}
                >
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

function TaskRow({
  trace,
  open,
  onToggle,
}: {
  trace: TaskTrace;
  open: boolean;
  onToggle: () => void;
}) {
  const failed = trace.status === "failed" || !!trace.error;
  const color = failed
    ? "var(--error)"
    : trace.skipped
      ? "var(--text-dim)"
      : "var(--accent)";
  return (
    <div
      style={{
        border: `1px solid ${failed ? "var(--error)" : "var(--border)"}`,
        borderRadius: 4,
        background: "var(--bg)",
        overflow: "hidden",
      }}
    >
      <button
        onClick={onToggle}
        style={{
          width: "100%",
          textAlign: "left",
          padding: "8px 12px",
          display: "flex",
          alignItems: "center",
          gap: 10,
          background: "transparent",
          border: 0,
          color: "var(--text)",
          cursor: "pointer",
          fontFamily: "inherit",
        }}
      >
        <span style={{ color, fontSize: 14 }}>{open ? "▾" : "▸"}</span>
        <span style={{ fontFamily: "var(--mono)", fontWeight: 500 }}>
          {trace.name}
        </span>
        {trace.label && <span className="badge">@{trace.label}</span>}
        {trace.provider && (
          <span className="badge" style={{ color: "#a07cf0" }}>
            {trace.provider}
          </span>
        )}
        <span className="meta" style={{ marginLeft: "auto" }}>
          <span style={{ color }}>{trace.status}</span>
          {trace.duration_ms > 0 ? ` · ${trace.duration_ms}ms` : ""}
          {trace.fallback_used ? " · fallback" : ""}
          {trace.skipped ? " · skipped" : ""}
          {trace.retry_count ? ` · retries: ${trace.retry_count}` : ""}
        </span>
      </button>
      {open && (
        <div
          style={{
            padding: "8px 12px",
            borderTop: "1px solid var(--border)",
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: 12,
          }}
        >
          <div>
            <div className="meta" style={{ marginBottom: 4 }}>
              inputs
            </div>
            <pre
              style={{
                margin: 0,
                fontSize: 11,
                fontFamily: "var(--mono)",
                whiteSpace: "pre-wrap",
                maxHeight: 300,
                overflow: "auto",
              }}
            >
              {jsonPretty(trace.inputs ?? {})}
            </pre>
          </div>
          <div>
            <div className="meta" style={{ marginBottom: 4 }}>
              outputs
            </div>
            <pre
              style={{
                margin: 0,
                fontSize: 11,
                fontFamily: "var(--mono)",
                whiteSpace: "pre-wrap",
                maxHeight: 300,
                overflow: "auto",
              }}
            >
              {jsonPretty(trace.outputs ?? {})}
            </pre>
          </div>
          {trace.params && Object.keys(trace.params).length > 0 && (
            <div style={{ gridColumn: "1 / -1" }}>
              <div className="meta" style={{ marginBottom: 4 }}>
                params
              </div>
              <pre
                style={{
                  margin: 0,
                  fontSize: 11,
                  fontFamily: "var(--mono)",
                  whiteSpace: "pre-wrap",
                  maxHeight: 200,
                  overflow: "auto",
                }}
              >
                {jsonPretty(trace.params)}
              </pre>
            </div>
          )}
          {trace.annotations && Object.keys(trace.annotations).length > 0 && (
            <div style={{ gridColumn: "1 / -1" }}>
              <div className="meta" style={{ marginBottom: 4 }}>
                annotations
              </div>
              <AnnotationsTable data={trace.annotations} />
            </div>
          )}
          {(trace.task_version ||
            trace.provider_version ||
            trace.logic_version) && (
            <div className="meta" style={{ gridColumn: "1 / -1", fontSize: 11 }}>
              {trace.task_version && <>task: {trace.task_version} </>}
              {trace.provider_version && <> · provider: {trace.provider_version} </>}
              {trace.logic_version && <> · logic: {trace.logic_version}</>}
            </div>
          )}
          {trace.error && (
            <div style={{ gridColumn: "1 / -1" }}>
              <div
                className="meta"
                style={{ marginBottom: 4, color: "var(--error)" }}
              >
                error
              </div>
              <pre style={{ margin: 0, fontSize: 11, color: "var(--error)" }}>
                {trace.error}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function AnnotationsTable({ data }: { data: Record<string, unknown> }) {
  const entries = Object.entries(data);
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "minmax(120px, max-content) 1fr",
        gap: "4px 12px",
        fontSize: 11,
        fontFamily: "var(--mono)",
      }}
    >
      {entries.map(([k, v]) => (
        <Fragment key={k}>
          <div style={{ color: "var(--text-dim)" }}>{k}</div>
          <div style={{ wordBreak: "break-word" }}>{renderAnnotationValue(v)}</div>
        </Fragment>
      ))}
    </div>
  );
}

function renderAnnotationValue(v: unknown): React.ReactNode {
  if (v && typeof v === "object" && (v as { __link?: boolean }).__link) {
    const link = v as { label?: string; url?: string };
    if (link.url) {
      return (
        <a
          href={link.url}
          target="_blank"
          rel="noreferrer"
          style={{ color: "var(--accent)" }}
        >
          {link.label || link.url}
        </a>
      );
    }
  }
  if (v === null || typeof v === "string" || typeof v === "number" || typeof v === "boolean") {
    return String(v);
  }
  return <span style={{ whiteSpace: "pre-wrap" }}>{jsonPretty(v)}</span>;
}

function jsonPretty(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}
