import { useEffect, useState } from "react";
import Editor from "@monaco-editor/react";
import { api, Diagnostic, SaveResult, ServerInfo } from "../api/client";

// Raw YAML editor with file picker, save (local) or bundle download (server).
// Phase 6 milestone: forms-based graph editor is the next add; this is the
// textual escape hatch that's always available.
export default function EditorPage() {
  const [tree, setTree] = useState<string[]>([]);
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [path, setPath] = useState<string | null>(null);
  const [original, setOriginal] = useState<string>("");
  const [draft, setDraft] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [diags, setDiags] = useState<Diagnostic[]>([]);
  const [flash, setFlash] = useState<string | null>(null);

  useEffect(() => {
    api.tree().then(setTree);
    api.info().then(setInfo).catch(() => {});
  }, []);

  useEffect(() => {
    if (!path) return;
    api.file(path).then((text) => {
      setOriginal(text);
      setDraft(text);
      setDiags([]);
      setFlash(null);
    });
  }, [path]);

  const dirty = path && draft !== original;

  async function save() {
    if (!path) return;
    setBusy(true);
    setDiags([]);
    setFlash(null);
    try {
      const { status, data } = await api.saveFiles({ [path]: draft });
      if (status === 200 && data.ok) {
        setOriginal(draft);
        setFlash(`Saved · ${(data.written ?? []).join(", ")}`);
        if (data.diagnostics && data.diagnostics.length > 0) {
          setDiags(data.diagnostics);
        }
      } else {
        setDiags(data.diagnostics ?? [{ Severity: "error", File: "", Message: "save failed" }]);
      }
    } catch (e) {
      setDiags([{ Severity: "error", File: "", Message: (e as Error).message }]);
    } finally {
      setBusy(false);
    }
  }

  async function downloadBundle() {
    if (!path) return;
    setBusy(true);
    setDiags([]);
    try {
      const res = await api.downloadBundle({ [path]: draft });
      if (!res.ok) {
        const data = (await res.json()) as SaveResult;
        setDiags(data.diagnostics ?? []);
        return;
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "jigsaw-config-bundle.tar.gz";
      a.click();
      URL.revokeObjectURL(url);
      setFlash("Bundle downloaded");
    } finally {
      setBusy(false);
    }
  }

  const mode = info?.mode ?? "local";
  const canEdit = info?.edit ?? false;

  return (
    <>
      <h1>Editor</h1>
      {!canEdit && (
        <div className="diag warning">
          Edit mode is off. Restart with <code>--edit</code> to enable saving.
        </div>
      )}
      <div style={{ display: "grid", gridTemplateColumns: "240px 1fr", gap: 16, height: "calc(100vh - 140px)" }}>
        <div style={{ overflow: "auto", border: "1px solid var(--border)", borderRadius: 6, padding: 8 }}>
          {tree.length === 0 && <div className="empty">no files</div>}
          {Object.entries(groupTree(tree)).map(([group, files]) => (
            <div key={group} style={{ marginBottom: 12 }}>
              <div style={{
                fontSize: 10,
                color: "var(--text-dim)",
                textTransform: "uppercase",
                letterSpacing: 0.5,
                padding: "4px 8px",
                fontWeight: 600,
              }}>
                {group} <span style={{ opacity: 0.6 }}>({files.length})</span>
              </div>
              {files.map((p) => (
                <div
                  key={p}
                  onClick={() => setPath(p)}
                  style={{
                    padding: "5px 8px 5px 16px",
                    cursor: "pointer",
                    borderRadius: 4,
                    fontFamily: "var(--mono)",
                    fontSize: 11,
                    color: p === path ? "var(--accent)" : "var(--text-dim)",
                    background: p === path ? "var(--panel-2)" : "transparent",
                  }}
                  title={p}
                >
                  {p.split("/").pop()}
                </div>
              ))}
            </div>
          ))}
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <strong>{path ?? "no file selected"}</strong>
            <span className="meta" style={{ marginLeft: "auto" }}>
              {dirty && <span className="badge warn">unsaved</span>}
            </span>
            <button
              disabled={!dirty || busy || !canEdit}
              onClick={mode === "server" ? downloadBundle : save}
              style={{
                background: dirty ? "var(--accent-dim)" : "var(--panel)",
                color: "var(--text)",
                border: "1px solid var(--border-strong)",
                borderRadius: 4,
                padding: "6px 12px",
                cursor: dirty && !busy ? "pointer" : "default",
              }}
            >
              {mode === "server" ? "Download bundle" : "Save"}
            </button>
          </div>
          {flash && <div className="diag" style={{ borderLeftColor: "var(--accent)" }}>{flash}</div>}
          {diags.length > 0 && (
            <div>
              {diags.map((d, i) => (
                <div key={i} className={`diag ${d.Severity}`}>
                  <span className="badge" style={{ marginLeft: 0, marginRight: 8 }}>
                    {d.Severity}
                  </span>
                  {d.Message}
                </div>
              ))}
            </div>
          )}
          <div style={{ flex: 1, border: "1px solid var(--border)", borderRadius: 6, overflow: "hidden" }}>
            <Editor
              height="100%"
              language="yaml"
              theme="vs-dark"
              value={draft}
              onChange={(v) => setDraft(v ?? "")}
              options={{
                minimap: { enabled: false },
                fontSize: 13,
                tabSize: 2,
                fontFamily: "ui-monospace, 'JetBrains Mono', 'SF Mono', Menlo, monospace",
                scrollBeyondLastLine: false,
              }}
            />
          </div>
        </div>
      </div>
    </>
  );
}

// Group file paths by top-level directory (flows, tasks, providers, endpoints).
// Anything else lands in "other". Order is meaningful — flows and tasks first,
// since those are what users touch most.
function groupTree(paths: string[]): Record<string, string[]> {
  const order = ["flows", "tasks", "providers", "endpoints"];
  const groups: Record<string, string[]> = {};
  for (const g of order) groups[g] = [];
  for (const p of paths) {
    const top = p.split("/")[0];
    if (groups[top]) {
      groups[top].push(p);
    } else {
      (groups.other ||= []).push(p);
    }
  }
  // Drop empty groups for cleaner UI.
  for (const g of Object.keys(groups)) {
    if (groups[g].length === 0) delete groups[g];
    else groups[g].sort();
  }
  return groups;
}
