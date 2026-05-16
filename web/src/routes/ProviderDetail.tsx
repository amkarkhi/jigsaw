import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import yaml from "js-yaml";
import { api, Diagnostic } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { useUnsavedGuard } from "../hooks/useUnsavedGuard";
import { ConfirmModal } from "../components/ConfirmModal";

// Editable provider detail. Edits write through /api/files to the provider's
// source file. Config / metadata are kept as JSON-edited text blobs so all
// shapes (URL strings, nested objects, lists) work without per-field UI.

interface EditableProvider {
  name: string;
  type: string;
  version: string;
  init_mode: string;
  pool_size: number | "";
  config_text: string;   // JSON text the user can edit freely
  metadata_text: string; // same for metadata
  _raw: Record<string, unknown>;
}

export default function ProviderDetail() {
  const { name } = useParams();
  const fetched = useAsync(() => api.provider(name!), [name]);
  const [filePath, setFilePath] = useState("");
  const [edited, setEdited] = useState<EditableProvider | null>(null);
  const [original, setOriginal] = useState("");
  const [showJSON, setShowJSON] = useState(false);
  const [busy, setBusy] = useState(false);
  const [diags, setDiags] = useState<Diagnostic[]>([]);
  const [flash, setFlash] = useState<string | null>(null);
  const [resetConfirm, setResetConfirm] = useState(false);
  const [configErr, setConfigErr] = useState<string | null>(null);
  const [metaErr, setMetaErr] = useState<string | null>(null);

  useEffect(() => {
    if (!fetched.data) return;
    const p = fetched.data.provider;
    const ed: EditableProvider = {
      name: p.name,
      type: p.type ?? "",
      version: p.version ?? "",
      init_mode: p.init_mode ?? "",
      pool_size: typeof p.pool_size === "number" ? p.pool_size : "",
      config_text: p.config ? JSON.stringify(p.config, null, 2) : "{}",
      metadata_text: p.metadata ? JSON.stringify(p.metadata, null, 2) : "",
      _raw: p as unknown as Record<string, unknown>,
    };
    setEdited(ed);
    setOriginal(JSON.stringify(ed));
    // locate the file (providers may also live in subdirs)
    (async () => {
      try {
        // No dedicated /api/provider-location yet — providers live in
        // providers/<name>.yml by repo convention. Try that first and
        // fall back to scanning all provider files via /api/tree.
        const tryPath = `providers/${p.name}.yml`;
        try {
          await api.file(tryPath);
          setFilePath(tryPath);
          return;
        } catch { /* fall through */ }
        const tree = await api.tree();
        for (const path of tree) {
          if (!path.startsWith("providers/")) continue;
          const raw = await api.file(path);
          const doc = yaml.load(raw) as { providers?: { name?: string }[] };
          if ((doc.providers ?? []).some((x) => x.name === p.name)) {
            setFilePath(path);
            return;
          }
        }
        setDiags([{ Severity: "error", File: "", Message: `cannot locate provider file for ${p.name}` }]);
      } catch (e) {
        setDiags([{ Severity: "error", File: "", Message: (e as Error).message }]);
      }
    })();
  }, [fetched.data]);

  const dirty = edited != null && JSON.stringify(edited) !== original;
  const blocker = useUnsavedGuard(dirty);

  function patch<K extends keyof EditableProvider>(k: K, v: EditableProvider[K]) {
    if (!edited) return;
    setEdited({ ...edited, [k]: v });
    setFlash(null);
  }

  async function save() {
    if (!edited || !filePath) return;
    // Validate JSON-ish fields client-side first.
    let cfg: unknown = {};
    let meta: unknown = undefined;
    try {
      cfg = edited.config_text.trim() ? JSON.parse(edited.config_text) : {};
      setConfigErr(null);
    } catch (e) {
      setConfigErr(`config is not valid JSON: ${(e as Error).message}`);
      return;
    }
    try {
      meta = edited.metadata_text.trim() ? JSON.parse(edited.metadata_text) : undefined;
      setMetaErr(null);
    } catch (e) {
      setMetaErr(`metadata is not valid JSON: ${(e as Error).message}`);
      return;
    }

    setBusy(true);
    setDiags([]);
    setFlash(null);
    try {
      const raw = await api.file(filePath);
      const doc = (yaml.load(raw) as { providers?: Record<string, unknown>[] }) ?? { providers: [] };
      const provs = doc.providers ?? [];
      const idx = provs.findIndex((p) => (p as { name?: string }).name === edited.name);
      if (idx < 0) throw new Error("provider vanished while editing");
      const merged = { ...(provs[idx] as Record<string, unknown>) };
      writeOrDelete(merged, "type", edited.type);
      writeOrDelete(merged, "version", edited.version);
      writeOrDelete(merged, "init_mode", edited.init_mode);
      if (edited.pool_size === "" || edited.pool_size === 0) delete merged.pool_size;
      else merged.pool_size = edited.pool_size;
      merged.config = cfg;
      if (meta === undefined) delete merged.metadata;
      else merged.metadata = meta;
      provs[idx] = merged;
      doc.providers = provs;

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
    if (!fetched.data) return;
    const p = fetched.data.provider;
    const ed: EditableProvider = {
      name: p.name,
      type: p.type ?? "",
      version: p.version ?? "",
      init_mode: p.init_mode ?? "",
      pool_size: typeof p.pool_size === "number" ? p.pool_size : "",
      config_text: p.config ? JSON.stringify(p.config, null, 2) : "{}",
      metadata_text: p.metadata ? JSON.stringify(p.metadata, null, 2) : "",
      _raw: p as unknown as Record<string, unknown>,
    };
    setEdited(ed);
    setOriginal(JSON.stringify(ed));
    setConfigErr(null);
    setMetaErr(null);
  }

  if (fetched.loading) return <div className="loading">Loading…</div>;
  if (fetched.error) return <div className="empty">Error: {fetched.error.message}</div>;
  if (!fetched.data || !edited) return null;

  return (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16, flexWrap: "wrap" }}>
        <h1 style={{ margin: 0 }}>Provider: {edited.name}</h1>
        {dirty && <span className="badge warn">unsaved</span>}
        <span className="badge">{edited.type}</span>
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
        <DetailCard title="Settings">
          <FieldRow label="type">
            <input className="input" value={edited.type} onChange={(e) => patch("type", e.target.value)} placeholder="cache, database, http…" />
          </FieldRow>
          <FieldRow label="version">
            <input className="input" value={edited.version} onChange={(e) => patch("version", e.target.value)} placeholder="(optional)" />
          </FieldRow>
          <FieldRow label="init_mode">
            <select className="input" value={edited.init_mode} onChange={(e) => patch("init_mode", e.target.value)}>
              <option value="">(default)</option>
              <option value="lazy">lazy</option>
              <option value="eager">eager</option>
              <option value="pooled">pooled</option>
            </select>
          </FieldRow>
          <FieldRow label="pool_size">
            <input
              className="input" type="number" min={0}
              value={edited.pool_size === "" ? "" : edited.pool_size}
              onChange={(e) => patch("pool_size", e.target.value === "" ? "" : Number(e.target.value))}
            />
          </FieldRow>
        </DetailCard>

        <DetailCard title="Used by">
          {fetched.data.used_by.length === 0 ? (
            <div className="meta">no tasks reference this provider</div>
          ) : (
            <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
              {fetched.data.used_by.map((t) => (
                <Link key={t} to={`/tasks/${t}`} className="badge">{t}</Link>
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

      <DetailCard title="Config (JSON)" style={{ marginBottom: 16 }}>
        <textarea
          className="input"
          rows={Math.min(20, Math.max(6, edited.config_text.split("\n").length + 1))}
          value={edited.config_text}
          onChange={(e) => { patch("config_text", e.target.value); setConfigErr(null); }}
          style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
          spellCheck={false}
        />
        {configErr && <div className="diag error" style={{ marginTop: 6 }}>{configErr}</div>}
      </DetailCard>

      <DetailCard title="Metadata (JSON, optional)" style={{ marginBottom: 16 }}>
        <textarea
          className="input"
          rows={Math.min(12, Math.max(3, edited.metadata_text.split("\n").length + 1))}
          value={edited.metadata_text}
          onChange={(e) => { patch("metadata_text", e.target.value); setMetaErr(null); }}
          style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
          spellCheck={false}
          placeholder="(empty = no metadata)"
        />
        {metaErr && <div className="diag error" style={{ marginTop: 6 }}>{metaErr}</div>}
      </DetailCard>

      {showJSON && (
        <DetailCard title="Raw JSON" style={{ marginBottom: 16 }}>
          <pre className="json" style={{ margin: 0 }}>{JSON.stringify(fetched.data.provider, null, 2)}</pre>
        </DetailCard>
      )}

      {blocker.state === "blocked" && (
        <ConfirmModal
          title="Unsaved changes"
          message="You have unsaved changes to this provider. Leaving will discard them."
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

// --- small subcomponents --------------------------------------------------

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

function writeOrDelete(target: Record<string, unknown>, key: string, value: string) {
  if (value === "") delete target[key];
  else target[key] = value;
}
