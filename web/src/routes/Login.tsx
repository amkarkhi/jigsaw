import { useState } from "react";
import { useLocation } from "react-router-dom";
import { api } from "../api/client";

// Centered login card. Shown when /api/me reports not-authenticated. On
// success, the server sets an HTTP-only session cookie; we just navigate
// back to wherever the user was trying to go.

export default function Login() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const location = useLocation();

  // The auth gate stashes the original target in `state.from`; default to /.
  const from = (location.state as { from?: string } | null)?.from ?? "/";

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { status, data } = await api.login(username, password);
      if (status === 200 && data.ok) {
        // Hard reload so the AuthGate re-fetches /api/me with the new cookie.
        window.location.href = from;
      } else {
        setError("Invalid username or password.");
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div style={{
      minHeight: "100vh", display: "flex", alignItems: "center",
      justifyContent: "center", background: "var(--bg)",
    }}>
      <form
        onSubmit={submit}
        style={{
          background: "var(--panel)",
          border: "1px solid var(--border-strong)",
          borderRadius: 8,
          padding: 32,
          width: 360,
          boxShadow: "0 8px 32px #000c",
        }}
      >
        <div style={{ marginBottom: 24 }}>
          <div style={{ fontSize: 20, fontWeight: 600, color: "var(--accent)", letterSpacing: 1 }}>
            jigsaw
          </div>
          <div className="meta" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5 }}>
            config manager
          </div>
        </div>

        <div style={{ marginBottom: 12 }}>
          <label className="meta" style={{ display: "block", marginBottom: 4 }}>Username</label>
          <input
            autoFocus
            className="input"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            style={{ width: "100%", padding: "8px 12px" }}
            autoComplete="username"
          />
        </div>

        <div style={{ marginBottom: 20 }}>
          <label className="meta" style={{ display: "block", marginBottom: 4 }}>Password</label>
          <input
            type="password"
            className="input"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            style={{ width: "100%", padding: "8px 12px" }}
            autoComplete="current-password"
          />
        </div>

        {error && (
          <div className="diag error" style={{ marginBottom: 16 }}>{error}</div>
        )}

        <button
          type="submit"
          className="btn btn-primary"
          disabled={busy || !username || !password}
          style={{ width: "100%", padding: 10 }}
        >
          {busy ? "Signing in…" : "Sign in"}
        </button>

        <div className="meta" style={{ fontSize: 11, marginTop: 16, textAlign: "center" }}>
          Or send <code>Authorization: Bearer &lt;token&gt;</code> for API access.
        </div>
      </form>
    </div>
  );
}
