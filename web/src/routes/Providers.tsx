import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import yaml from "js-yaml";
import { api } from "../api/client";
import { useAsync } from "../hooks/useAsync";

export default function Providers() {
  const { data, error, loading } = useAsync(() => api.providers(), []);
  const [q, setQ] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [newOpen, setNewOpen] = useState(false);
  const navigate = useNavigate();

  const types = useMemo(() => {
    if (!data) return [];
    return Array.from(new Set(data.map((p) => p.type))).sort();
  }, [data]);

  const filtered = useMemo(() => {
    if (!data) return [];
    const needle = q.trim().toLowerCase();
    return data.filter((p) => {
      if (typeFilter && p.type !== typeFilter) return false;
      if (!needle) return true;
      return p.name.toLowerCase().includes(needle) || p.type.toLowerCase().includes(needle);
    });
  }, [data, q, typeFilter]);

  if (loading) return <div className="loading">Loading…</div>;
  if (error) return <div className="empty">Error: {error.message}</div>;
  if (!data || data.length === 0) return (
    <>
      <h1 style={{ display: "flex", alignItems: "center", gap: 12 }}>
        Providers
        <button className="btn btn-primary" onClick={() => setNewOpen(true)} style={{ marginLeft: "auto" }}>
          + New provider
        </button>
      </h1>
      <div className="empty">No providers configured.</div>
      {newOpen && (
        <NewProviderModal
          existing={[]}
          onCreated={(name) => { setNewOpen(false); navigate(`/providers/${name}`); }}
          onClose={() => setNewOpen(false)}
        />
      )}
    </>
  );

  return (
    <>
      <h1 style={{ display: "flex", alignItems: "center", gap: 12 }}>
        Providers
        <button className="btn btn-primary" onClick={() => setNewOpen(true)} style={{ marginLeft: "auto" }}>
          + New provider
        </button>
      </h1>
      <div className="filter-bar" style={{ marginBottom: 12 }}>
        <input
          className="input"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={`Search ${data.length} provider${data.length === 1 ? "" : "s"}…`}
          style={{ flex: 1, minWidth: 240 }}
        />
        <select className="input" value={typeFilter} onChange={(e) => setTypeFilter(e.target.value)}>
          <option value="">all types</option>
          {types.map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        {(q || typeFilter) && (
          <>
            <span className="meta">{filtered.length} / {data.length}</span>
            <button className="btn" onClick={() => { setQ(""); setTypeFilter(""); }}>Clear</button>
          </>
        )}
      </div>

      {newOpen && (
        <NewProviderModal
          existing={data.map((p) => p.name)}
          onCreated={(name) => { setNewOpen(false); navigate(`/providers/${name}`); }}
          onClose={() => setNewOpen(false)}
        />
      )}

      <div className="card-grid">
        {filtered.map((p) => (
          <Link key={p.name} to={`/providers/${p.name}`} className="card" style={{ textDecoration: "none", color: "inherit", display: "block" }}>
            <h3>{p.name}</h3>
            <div className="sub" style={{ marginBottom: 6 }}>
              <span style={{ color: typeColor(p.type) }}>{p.type}</span>
              {p.version && <span> · {p.version}</span>}
            </div>
            <div className="sub">init: {p.init_mode || "lazy"}</div>
            {p.pool_size > 0 && <div className="sub">pool: {p.pool_size}</div>}
            <div className="sub" style={{ marginTop: 8 }}>
              {p.user_count} task{p.user_count === 1 ? "" : "s"} use this
            </div>
          </Link>
        ))}
      </div>
    </>
  );
}

function NewProviderModal({
  existing,
  onCreated,
  onClose,
}: {
  existing: string[];
  onCreated: (name: string) => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [registeredTypes, setRegisteredTypes] = useState<string[]>([]);
  const [type, setType] = useState("database");
  const [version, setVersion] = useState("");

  useEffect(() => {
    api.providerTypes()
      .then((ts) => {
        setRegisteredTypes(ts);
        if (ts.length > 0 && !ts.includes(type)) {
          setType(ts[0]);
        }
      })
      .catch(() => {
        // Endpoint missing or backend not running — fall back to a single
        // sensible default so the form still works.
        setRegisteredTypes(["database", "api_call"]);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const [initMode, setInitMode] = useState("lazy");
  const [poolSize, setPoolSize] = useState<number | "">("");
  const [configText, setConfigText] = useState("host: localhost\nport: 6379\n");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const nameTaken = existing.includes(name.trim());
  const nameValid = /^[a-zA-Z][a-zA-Z0-9_-]*$/.test(name.trim());
  const canSave = nameValid && !nameTaken && type.trim() && !busy;

  async function save() {
    setBusy(true);
    setErr(null);
    try {
      let cfg: Record<string, unknown> = {};
      const trimmed = configText.trim();
      if (trimmed) {
        const parsed = yaml.load(trimmed);
        if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
          cfg = parsed as Record<string, unknown>;
        } else {
          throw new Error("config must be a YAML mapping (key: value)");
        }
      }
      const doc: Record<string, unknown> = {
        providers: [{
          name: name.trim(),
          type: type.trim(),
          ...(version.trim() ? { version: version.trim() } : {}),
          ...(Object.keys(cfg).length > 0 ? { config: cfg } : {}),
          init_mode: initMode,
          ...(poolSize !== "" && Number(poolSize) > 0 ? { pool_size: Number(poolSize) } : {}),
        }],
      };
      const text = yaml.dump(doc, { lineWidth: 100, noRefs: true });
      const path = `providers/${name.trim()}.yml`;
      const { status, data } = await api.saveFiles({ [path]: text });
      if (status !== 200 || !data.ok) {
        throw new Error((data.diagnostics ?? []).map((d) => d.Message).join("; ") || "save failed");
      }
      onCreated(name.trim());
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

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
          <strong style={{ flex: 1 }}>New provider</strong>
          <button className="btn" onClick={onClose}>Cancel</button>
        </div>
        <div style={{ padding: 16, display: "grid", gridTemplateColumns: "max-content 1fr", gap: "10px 12px" }}>
          <label className="meta">name *</label>
          <div>
            <input
              className="input"
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. cache_eu"
              style={{ width: "100%" }}
            />
            {name && !nameValid && <div className="meta" style={{ color: "var(--error)" }}>letters, digits, _ and -; must start with a letter</div>}
            {nameTaken && <div className="meta" style={{ color: "var(--error)" }}>a provider with this name already exists</div>}
          </div>

          <label className="meta">type *</label>
          <select className="input" value={type} onChange={(e) => setType(e.target.value)}>
            {registeredTypes.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>

          <label className="meta">version</label>
          <input className="input" value={version} onChange={(e) => setVersion(e.target.value)} placeholder="(optional)" />

          <label className="meta">init_mode</label>
          <select className="input" value={initMode} onChange={(e) => setInitMode(e.target.value)}>
            <option value="lazy">lazy</option>
            <option value="eager">eager</option>
            <option value="pooled">pooled</option>
          </select>

          <label className="meta">pool_size</label>
          <input
            className="input"
            type="number"
            min={0}
            value={poolSize === "" ? "" : poolSize}
            onChange={(e) => setPoolSize(e.target.value === "" ? "" : Number(e.target.value))}
            placeholder="(only used when init_mode is pooled)"
          />

          <label className="meta" style={{ alignSelf: "start", marginTop: 4 }}>config</label>
          <textarea
            className="input"
            rows={8}
            value={configText}
            onChange={(e) => setConfigText(e.target.value)}
            style={{ fontFamily: "var(--mono)", fontSize: 12 }}
            placeholder={"host: localhost\nport: 6379"}
          />
        </div>

        {err && <div className="diag error" style={{ margin: "0 16px 12px" }}>{err}</div>}

        <div style={{ padding: "12px 16px", borderTop: "1px solid var(--border)", display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary" disabled={!canSave} onClick={save}>
            {busy ? "Creating…" : "Create provider"}
          </button>
        </div>
      </div>
    </div>
  );
}

// typeColor picks a stable color per type name so cards stay visually
// distinct as users register their own provider types. Hash → HSL gives
// well-separated hues without a hardcoded palette.
function typeColor(t: string): string {
  let h = 0;
  for (let i = 0; i < t.length; i++) {
    h = (h * 31 + t.charCodeAt(i)) >>> 0;
  }
  return `hsl(${h % 360}, 60%, 70%)`;
}
