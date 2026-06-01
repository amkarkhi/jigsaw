import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import yaml from "js-yaml";
import { api, EndpointSummary, FlowSummary } from "../api/client";
import { useAsync } from "../hooks/useAsync";
import { useConfirmDialog } from "../components/useDialog";

export default function Endpoints() {
  const [reloadTick, setReloadTick] = useState(0);
  const { data, error, loading } = useAsync(() => api.endpoints(), [reloadTick]);
  const [newOpen, setNewOpen] = useState(false);
  const [existingNames, setExistingNames] = useState<string[]>([]);
  useEffect(() => {
    if (data) setExistingNames(data.map((d) => d.name));
  }, [data]);
  const reload = () => setReloadTick((t) => t + 1);

  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;

  const header = (
    <h1 style={{ display: "flex", alignItems: "center", gap: 12 }}>
      Endpoints
      <button className="btn btn-primary" onClick={() => setNewOpen(true)} style={{ marginLeft: "auto" }}>
        + New endpoint
      </button>
    </h1>
  );

  if (!data || data.length === 0) {
    return (
      <>
        {header}
        <div className="empty">No endpoints configured.</div>
        {newOpen && (
          <NewEndpointModal
            existing={existingNames}
            onCreated={() => { setNewOpen(false); reload(); }}
            onClose={() => setNewOpen(false)}
          />
        )}
      </>
    );
  }

  return (
    <>
      {header}
      {data.map((ep) => (
        <EndpointCard key={ep.name} ep={ep} onChanged={reload} />
      ))}
      {newOpen && (
        <NewEndpointModal
          existing={existingNames}
          onCreated={() => { setNewOpen(false); reload(); }}
          onClose={() => setNewOpen(false)}
        />
      )}
    </>
  );
}

function EndpointCard({ ep, onChanged }: { ep: EndpointSummary; onChanged: () => void }) {
  const [addOpen, setAddOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const { confirm, ui: confirmUI } = useConfirmDialog();

  async function removeMapping(sub: number) {
    const ok = await confirm({
      title: "Remove mapping?",
      message: `Remove mapping sub=${sub} from "${ep.name}"?`,
      confirmLabel: "Remove",
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    setErr(null);
    try {
      const { path } = await api.endpointLocation(ep.name);
      const raw = await api.file(path);
      const doc = (yaml.load(raw) as { endpoints?: Record<string, unknown>[] }) ?? { endpoints: [] };
      const eps = (doc.endpoints ?? []) as Record<string, unknown>[];
      const idx = eps.findIndex((e) => (e as { name?: string }).name === ep.name);
      if (idx < 0) throw new Error("endpoint vanished while editing");
      const flows = ((eps[idx] as { flows?: { sub: number; flow_name: string }[] }).flows ?? [])
        .filter((m) => m.sub !== sub);
      (eps[idx] as Record<string, unknown>).flows = flows;
      doc.endpoints = eps;
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [path]: text });
      if (status !== 200 || !data.ok) {
        throw new Error((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
      }
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="row" style={{ cursor: "default", flexDirection: "column", alignItems: "stretch", gap: 8 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div className="name">
          <span className="mono">{ep.method}</span> {ep.path}
        </div>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <div className="meta">{ep.name}</div>
          {ep.flows.length > 0 && (
            <Link
              className="btn"
              to={`/playground?endpoint=${encodeURIComponent(ep.name)}&sub=${ep.flows[0].sub}`}
              title="Test this endpoint in the playground"
            >
              Test
            </Link>
          )}
          <button className="btn" disabled={busy} onClick={() => setAddOpen(true)}>+ Flow</button>
        </div>
      </div>
      {ep.description && <div className="meta">{ep.description}</div>}
      {ep.request_params && ep.request_params.length > 0 && (
        <div className="meta" style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
          <span style={{ opacity: 0.7 }}>seeds:</span>
          {ep.request_params.map((p) => (
            <span
              key={p}
              className="badge mono"
              title="Scope key seeded from the request before the flow runs"
              style={{ background: "var(--badge-soft, rgba(120,120,120,0.15))" }}
            >
              {p}
            </span>
          ))}
        </div>
      )}
      <div className="meta" style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
        {ep.flows.length === 0 && <span style={{ opacity: 0.7 }}>no flow mappings</span>}
        {ep.flows.map((m) => (
          <span key={m.sub} className="badge" style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
            sub={m.sub} → {m.flow}
            <button
              onClick={() => removeMapping(m.sub)}
              disabled={busy}
              title="Remove mapping"
              style={{
                background: "transparent", border: 0, padding: 0,
                color: "var(--error)", cursor: "pointer", marginLeft: 4, fontSize: 12,
              }}
            >
              ×
            </button>
          </span>
        ))}
      </div>
      {err && <div className="diag error">{err}</div>}
      {addOpen && (
        <AddFlowMappingModal
          endpointName={ep.name}
          usedSubs={ep.flows.map((m) => m.sub)}
          onAdded={() => { setAddOpen(false); onChanged(); }}
          onClose={() => setAddOpen(false)}
        />
      )}
      {confirmUI}
    </div>
  );
}

function AddFlowMappingModal({
  endpointName,
  usedSubs,
  onAdded,
  onClose,
}: {
  endpointName: string;
  usedSubs: number[];
  onAdded: () => void;
  onClose: () => void;
}) {
  const [flows, setFlows] = useState<FlowSummary[]>([]);
  const [flowName, setFlowName] = useState("");
  const [sub, setSub] = useState<number | "">("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api.flows().then((fs) => {
      setFlows(fs);
      if (fs.length > 0) setFlowName(fs[0].name);
    }).catch((e) => setErr((e as Error).message));
    // Suggest the smallest unused sub.
    let next = 1;
    while (usedSubs.includes(next)) next += 1;
    setSub(next);
  }, [usedSubs]);

  const subTaken = sub !== "" && usedSubs.includes(Number(sub));
  const canSave = flowName && sub !== "" && Number(sub) > 0 && !subTaken && !busy;

  async function save() {
    setBusy(true);
    setErr(null);
    try {
      const { path } = await api.endpointLocation(endpointName);
      const raw = await api.file(path);
      const doc = (yaml.load(raw) as { endpoints?: Record<string, unknown>[] }) ?? { endpoints: [] };
      const eps = (doc.endpoints ?? []) as Record<string, unknown>[];
      const idx = eps.findIndex((e) => (e as { name?: string }).name === endpointName);
      if (idx < 0) throw new Error("endpoint vanished while editing");
      const flowsList = ((eps[idx] as { flows?: { sub: number; flow_name: string }[] }).flows ?? []).slice();
      flowsList.push({ sub: Number(sub), flow_name: flowName });
      flowsList.sort((a, b) => a.sub - b.sub);
      (eps[idx] as Record<string, unknown>).flows = flowsList;
      doc.endpoints = eps;
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const { status, data } = await api.saveFiles({ [path]: text });
      if (status !== 200 || !data.ok) {
        throw new Error((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
      }
      onAdded();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title={`Add flow mapping to "${endpointName}"`} onClose={onClose}>
      <div style={{ display: "grid", gridTemplateColumns: "max-content 1fr", gap: "10px 12px", padding: 16 }}>
        <label className="meta">sub *</label>
        <div>
          <input
            className="input"
            type="number"
            min={1}
            autoFocus
            value={sub === "" ? "" : sub}
            onChange={(e) => setSub(e.target.value === "" ? "" : Number(e.target.value))}
            style={{ width: 120 }}
          />
          {subTaken && <div className="meta" style={{ color: "var(--error)" }}>sub={String(sub)} is already mapped on this endpoint</div>}
        </div>

        <label className="meta">flow *</label>
        <select className="input" value={flowName} onChange={(e) => setFlowName(e.target.value)}>
          {flows.length === 0 && <option value="">no flows defined</option>}
          {flows.map((f) => <option key={f.name} value={f.name}>{f.name}</option>)}
        </select>
      </div>

      {err && <div className="diag error" style={{ margin: "0 16px 12px" }}>{err}</div>}

      <ModalFooter onCancel={onClose}>
        <button className="btn btn-primary" disabled={!canSave} onClick={save}>
          {busy ? "Adding…" : "Add mapping"}
        </button>
      </ModalFooter>
    </ModalShell>
  );
}

function NewEndpointModal({
  existing,
  onCreated,
  onClose,
}: {
  existing: string[];
  onCreated: () => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [path, setPath] = useState("/api/");
  const [method, setMethod] = useState("POST");
  const [description, setDescription] = useState("");
  const [flows, setFlows] = useState<FlowSummary[]>([]);
  const [initialFlow, setInitialFlow] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api.flows().then((fs) => {
      setFlows(fs);
      if (fs.length > 0) setInitialFlow(fs[0].name);
    }).catch(() => {});
  }, []);

  const nameTaken = existing.includes(name.trim());
  const nameValid = /^[a-zA-Z][a-zA-Z0-9_-]*$/.test(name.trim());
  const pathValid = path.trim().startsWith("/");
  const canSave = nameValid && !nameTaken && pathValid && method && !busy;

  async function save() {
    setBusy(true);
    setErr(null);
    try {
      const doc: Record<string, unknown> = {
        endpoints: [{
          name: name.trim(),
          path: path.trim(),
          method: method.toUpperCase(),
          ...(description.trim() ? { description: description.trim() } : {}),
          flows: initialFlow
            ? [{ sub: 1, flow_name: initialFlow }]
            : [],
        }],
      };
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const target = `endpoints/${name.trim()}.yml`;
      const { status, data } = await api.saveFiles({ [target]: text });
      if (status !== 200 || !data.ok) {
        throw new Error((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
      }
      onCreated();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell title="New endpoint" onClose={onClose}>
      <div style={{ padding: 16, display: "grid", gridTemplateColumns: "max-content 1fr", gap: "10px 12px" }}>
        <label className="meta">name *</label>
        <div>
          <input
            className="input"
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. catalog_search"
            style={{ width: "100%" }}
          />
          {name && !nameValid && <div className="meta" style={{ color: "var(--error)" }}>letters, digits, _ and -; must start with a letter</div>}
          {nameTaken && <div className="meta" style={{ color: "var(--error)" }}>an endpoint with this name already exists</div>}
        </div>

        <label className="meta">path *</label>
        <input
          className="input"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="/api/search"
        />

        <label className="meta">method *</label>
        <select className="input" value={method} onChange={(e) => setMethod(e.target.value)}>
          <option value="GET">GET</option>
          <option value="POST">POST</option>
          <option value="PUT">PUT</option>
          <option value="PATCH">PATCH</option>
          <option value="DELETE">DELETE</option>
        </select>

        <label className="meta">description</label>
        <input
          className="input"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="(optional)"
        />

        <label className="meta">first mapping</label>
        <div>
          <select className="input" value={initialFlow} onChange={(e) => setInitialFlow(e.target.value)} style={{ width: "100%" }}>
            <option value="">(none — add flow mappings later)</option>
            {flows.map((f) => <option key={f.name} value={f.name}>{f.name}</option>)}
          </select>
          <div className="meta">If chosen, becomes the endpoint's <code>sub=1</code> mapping.</div>
        </div>
      </div>

      {err && <div className="diag error" style={{ margin: "0 16px 12px" }}>{err}</div>}

      <ModalFooter onCancel={onClose}>
        <button className="btn btn-primary" disabled={!canSave} onClick={save}>
          {busy ? "Creating…" : "Create endpoint"}
        </button>
      </ModalFooter>
    </ModalShell>
  );
}

function ModalShell({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed", inset: 0, background: "#000a",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: 80, zIndex: 200,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)", border: "1px solid var(--border-strong)",
          borderRadius: 6, width: 560, maxHeight: "85vh", overflow: "auto",
          display: "flex", flexDirection: "column",
        }}
      >
        <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)", display: "flex", alignItems: "center" }}>
          <strong style={{ flex: 1 }}>{title}</strong>
          <button className="btn" onClick={onClose}>Cancel</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function ModalFooter({ onCancel, children }: { onCancel: () => void; children: React.ReactNode }) {
  return (
    <div style={{ padding: "12px 16px", borderTop: "1px solid var(--border)", display: "flex", justifyContent: "flex-end", gap: 8 }}>
      <button className="btn" onClick={onCancel}>Cancel</button>
      {children}
    </div>
  );
}
