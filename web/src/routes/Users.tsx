import { useEffect, useState } from "react";
import { api, UserRow } from "../api/client";
import { useConfirmDialog } from "../components/useDialog";

// Admin-only page. Shows the auth.json roster and lets admins:
//   - create a new password user (with role + per-resource access list)
//   - edit an existing user's role / email / access (PATCH /api/users/{name})
//   - delete a user
// The server enforces the same rules (e.g. you can't demote or delete yourself).
// SSO-provisioned users show up here too — admin grants them access before
// they can write anything.

export default function Users() {
  const [users, setUsers] = useState<UserRow[]>([]);
  const [resources, setResources] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [flash, setFlash] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const { confirm, ui: confirmUI } = useConfirmDialog();

  function confirmDelete(username: string) {
    return confirm({
      title: `Delete ${username}?`,
      message: "If this user authenticates via SSO, they'll be re-provisioned on their next login with the default role.",
      confirmLabel: "Delete user",
      danger: true,
    });
  }

  async function load() {
    setLoading(true);
    setErr(null);
    try {
      const { users, resources } = await api.listUsers();
      setUsers(users);
      setResources(resources);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  if (loading) return <div className="loading">Loading…</div>;
  if (err) return <div className="diag error">Error: {err}</div>;

  return (
    <>
      <h1 style={{ display: "flex", alignItems: "center", gap: 12 }}>
        Users
        <button className="btn btn-primary" onClick={() => setCreateOpen(true)} style={{ marginLeft: "auto" }}>
          + New user
        </button>
      </h1>
      <div className="meta" style={{ marginBottom: 16 }}>
        Admins have access to everything regardless of the checkboxes below.
        Viewers can read every page but can't save changes.
      </div>

      {flash && <div className="diag" style={{ borderLeftColor: "var(--accent)", marginBottom: 12 }}>{flash}</div>}

      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr style={{ borderBottom: "1px solid var(--border-strong)", textAlign: "left" }}>
            <th style={{ padding: "8px 12px" }}>Username</th>
            <th style={{ padding: "8px 12px" }}>Email</th>
            <th style={{ padding: "8px 12px" }}>Role</th>
            <th style={{ padding: "8px 12px" }}>Access</th>
            <th style={{ padding: "8px 12px" }}>Created</th>
            <th style={{ padding: "8px 12px" }}></th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <UserRowEditor
              key={u.username}
              user={u}
              resources={resources}
              onChange={(msg) => { setFlash(msg); load(); }}
              onError={setErr}
              confirmDelete={confirmDelete}
            />
          ))}
          {users.length === 0 && (
            <tr><td colSpan={6} className="empty" style={{ padding: 24 }}>No users yet.</td></tr>
          )}
        </tbody>
      </table>

      {createOpen && (
        <CreateUserModal
          resources={resources}
          existing={users.map((u) => u.username)}
          onCreated={() => { setCreateOpen(false); setFlash("User created."); load(); }}
          onClose={() => setCreateOpen(false)}
        />
      )}
      {confirmUI}
    </>
  );
}

function UserRowEditor({
  user,
  resources,
  onChange,
  onError,
  confirmDelete,
}: {
  user: UserRow;
  resources: string[];
  onChange: (msg: string) => void;
  onError: (msg: string) => void;
  confirmDelete: (username: string) => Promise<boolean>;
}) {
  const [role, setRole] = useState(user.role);
  const [email, setEmail] = useState(user.email ?? "");
  const [access, setAccess] = useState<string[]>(user.access ?? []);
  const [busy, setBusy] = useState(false);
  const dirty =
    role !== user.role ||
    email !== (user.email ?? "") ||
    JSON.stringify([...access].sort()) !== JSON.stringify([...(user.access ?? [])].sort());

  async function save() {
    setBusy(true);
    try {
      const { status, data } = await api.updateUser(user.username, { role, email, access });
      if (status !== 200 || !data.ok) {
        onError(`update failed (${status})`);
        return;
      }
      onChange(`Updated ${user.username}.`);
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    const ok = await confirmDelete(user.username);
    if (!ok) return;
    setBusy(true);
    try {
      const { status, data } = await api.deleteUser(user.username);
      if (status !== 200 || !data.ok) {
        onError(`delete failed (${status})`);
        return;
      }
      onChange(`Deleted ${user.username}.`);
    } finally {
      setBusy(false);
    }
  }

  function toggle(r: string) {
    setAccess((cur) => cur.includes(r) ? cur.filter((x) => x !== r) : [...cur, r]);
  }

  return (
    <tr style={{ borderBottom: "1px solid var(--border)", verticalAlign: "middle" }}>
      <td style={{ padding: "8px 12px", fontFamily: "var(--mono)" }}>{user.username}</td>
      <td style={{ padding: "8px 12px" }}>
        <input
          className="input"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="(optional)"
          style={{ width: "100%" }}
        />
      </td>
      <td style={{ padding: "8px 12px" }}>
        <select className="input" value={role} onChange={(e) => setRole(e.target.value)}>
          <option value="viewer">viewer</option>
          <option value="admin">admin</option>
        </select>
      </td>
      <td style={{ padding: "8px 12px" }}>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
          {resources.map((r) => (
            <label key={r} className="meta" style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <input
                type="checkbox"
                checked={role === "admin" || access.includes(r)}
                disabled={role === "admin"}
                onChange={() => toggle(r)}
              />
              {r}
            </label>
          ))}
        </div>
      </td>
      <td style={{ padding: "8px 12px", fontSize: 11, color: "var(--text-dim)" }}>
        {user.created_at ? new Date(user.created_at).toLocaleDateString() : ""}
      </td>
      <td style={{ padding: "8px 12px", textAlign: "right", whiteSpace: "nowrap" }}>
        <button className="btn btn-primary" disabled={!dirty || busy} onClick={save} style={{ marginRight: 6 }}>
          Save
        </button>
        <button
          className="btn"
          disabled={busy}
          onClick={remove}
          style={{ color: "var(--error)", borderColor: "var(--error)" }}
        >
          Delete
        </button>
      </td>
    </tr>
  );
}

function CreateUserModal({
  resources,
  existing,
  onCreated,
  onClose,
}: {
  resources: string[];
  existing: string[];
  onCreated: () => void;
  onClose: () => void;
}) {
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("viewer");
  const [access, setAccess] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const nameTaken = existing.includes(username.trim());
  const nameValid = /^[a-zA-Z0-9_-]{1,32}$/.test(username.trim());
  const passOk = password.length >= 8;
  const canSave = nameValid && !nameTaken && passOk && !busy;

  function toggle(r: string) {
    setAccess((cur) => cur.includes(r) ? cur.filter((x) => x !== r) : [...cur, r]);
  }

  async function save() {
    setBusy(true);
    setErr(null);
    try {
      const { status, data } = await api.createUser({
        username: username.trim(),
        password,
        role,
        email: email.trim() || undefined,
        access: role === "admin" ? undefined : access,
      });
      if (status !== 200 || !data.ok) {
        throw new Error(`create failed (${status})`);
      }
      onCreated();
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
          borderRadius: 6, width: 540, maxHeight: "85vh", overflow: "auto",
          display: "flex", flexDirection: "column",
        }}
      >
        <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--border)" }}>
          <strong>New user</strong>
        </div>
        <div style={{ padding: 16, display: "grid", gridTemplateColumns: "max-content 1fr", gap: "10px 12px" }}>
          <label className="meta">username *</label>
          <div>
            <input
              className="input"
              autoFocus
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="letters, digits, _ and -"
              style={{ width: "100%" }}
            />
            {username && !nameValid && <div className="meta" style={{ color: "var(--error)" }}>1–32 chars; letters, digits, _, -</div>}
            {nameTaken && <div className="meta" style={{ color: "var(--error)" }}>username already exists</div>}
          </div>

          <label className="meta">email</label>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="(optional)" />

          <label className="meta">password *</label>
          <div>
            <input
              className="input"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="at least 8 characters"
              style={{ width: "100%" }}
            />
            {password && !passOk && <div className="meta" style={{ color: "var(--error)" }}>password must be ≥ 8 characters</div>}
          </div>

          <label className="meta">role *</label>
          <select className="input" value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="viewer">viewer</option>
            <option value="admin">admin</option>
          </select>

          <label className="meta" style={{ alignSelf: "start", paddingTop: 4 }}>access</label>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 10 }}>
            {resources.map((r) => (
              <label key={r} className="meta" style={{ display: "flex", alignItems: "center", gap: 4 }}>
                <input
                  type="checkbox"
                  checked={role === "admin" || access.includes(r)}
                  disabled={role === "admin"}
                  onChange={() => toggle(r)}
                />
                {r}
              </label>
            ))}
          </div>
        </div>

        {err && <div className="diag error" style={{ margin: "0 16px 12px" }}>{err}</div>}

        <div style={{ padding: "12px 16px", borderTop: "1px solid var(--border)", display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary" disabled={!canSave} onClick={save}>
            {busy ? "Creating…" : "Create user"}
          </button>
        </div>
      </div>
    </div>
  );
}
