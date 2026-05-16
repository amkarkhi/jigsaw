import { NavLink, Outlet } from "react-router-dom";
import { useEffect, useState } from "react";
import { api, ServerInfo } from "./api/client";
import type { CurrentUser } from "./components/AuthGate";

// Trigger a tar-bundle download of the entire config tree. Useful in server
// mode (download → extract → commit → ship), and also handy as a backup
// snapshot in local mode.
async function downloadBundle() {
  const res = await fetch("/api/bundle", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ files: {} }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    const msg =
      (data && (data.diagnostics?.[0]?.Message || data.error)) ||
      `download failed (${res.status})`;
    alert(msg); // eslint-disable-line no-alert
    return;
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `jigsaw-config-${new Date().toISOString().slice(0, 10)}.tar.gz`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

// Layout chrome: sidebar nav + outlet. The route tree itself lives in main.tsx
// (data-router style) so useBlocker works for the unsaved-changes guard.
export default function App({ user }: { user?: CurrentUser }) {
  const [info, setInfo] = useState<ServerInfo | null>(null);

  useEffect(() => {
    api.info().then(setInfo).catch(() => setInfo(null));
  }, []);

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          jigsaw
          <small>config manager</small>
        </div>
        <nav className="nav">
          <NavLink to="/" end>Overview</NavLink>
          <NavLink to="/flows">Flows</NavLink>
          <NavLink to="/tasks">Tasks</NavLink>
          <NavLink to="/providers">Providers</NavLink>
          <NavLink to="/endpoints">Endpoints</NavLink>
          <NavLink to="/logic">Logic registry</NavLink>
          <NavLink to="/diagnostics">Diagnostics</NavLink>
          <NavLink to="/editor">Editor</NavLink>
        </nav>
        {info?.edit && (
          <div style={{ padding: "0 24px" }}>
            <button
              className="btn"
              style={{ width: "100%" }}
              onClick={downloadBundle}
              title="Download the full config tree as a tar.gz"
            >
              ⬇ Download bundle
            </button>
          </div>
        )}
        <div className="footer-info">
          {user && (
            <div style={{ marginBottom: 8, display: "flex", alignItems: "center", gap: 6 }}>
              <span className="mono" style={{ flex: 1 }}>{user.label}</span>
              <span className="badge" style={{ marginLeft: 0 }}>{user.role}</span>
              <button
                className="btn"
                style={{ padding: "2px 6px", fontSize: 10 }}
                onClick={async () => { await api.logout(); window.location.href = "/login"; }}
              >
                ⎋
              </button>
            </div>
          )}
          {info ? (
            <>
              <div style={{ marginBottom: 6 }}>
                <span className="mono" title={info.config_path}>
                  {info.service_name || prettyServiceFromPath(info.config_path)}
                </span>
              </div>
              <div className="meta" style={{ fontSize: 10, marginBottom: 8 }}>
                {info.mode} · edit {info.edit ? "on" : "off"}
              </div>
            </>
          ) : (
            <span>connecting…</span>
          )}
          <div style={{ fontSize: 10, color: "var(--text-dim)", marginTop: 4 }}>
            powered by{" "}
            <a
              href="https://github.com/amkarkhi/jigsaw"
              target="_blank"
              rel="noreferrer"
              style={{ color: "var(--accent-dim)", textDecoration: "none" }}
            >
              @amkarkhi/jigsaw
            </a>
          </div>
        </div>
      </aside>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}

// Derive a short service-name fallback from a config path: the parent dir
// name (e.g. "/opt/search-flow/configs" → "search-flow"). If the path is
// already short, return it as-is.
function prettyServiceFromPath(path: string): string {
  const parts = path.split("/").filter(Boolean);
  if (parts.length === 0) return path;
  const last = parts[parts.length - 1];
  if (last === "configs" && parts.length >= 2) return parts[parts.length - 2];
  return last;
}
