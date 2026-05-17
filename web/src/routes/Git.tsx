import { useEffect, useState } from "react";
import { api } from "../api/client";

// Git — per-user GitLab integration. Two panels:
//   1) Settings: base URL, project (group/repo), default branch, author
//      identity, and the Personal Access Token. The PAT is write-only from
//      the UI — the server returns just a "configured" flag.
//   2) Push: commit message + branch, runs the dashboard's git pipeline,
//      streams the redacted transcript back into the panel, and surfaces
//      the GitLab URL of the pushed branch.

interface Settings {
  base_url: string;
  project: string;
  default_branch: string;
  author_name: string;
  author_email: string;
  pat_configured: boolean;
  secret_key_set: boolean;
}

export default function Git() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  // Form state for the settings panel (separate from the loaded snapshot so
  // unsaved edits don't clobber the "configured" flag).
  const [baseURL, setBaseURL] = useState("");
  const [project, setProject] = useState("");
  const [defaultBranch, setDefaultBranch] = useState("");
  const [authorName, setAuthorName] = useState("");
  const [authorEmail, setAuthorEmail] = useState("");
  const [pat, setPat] = useState("");
  const [clearPAT, setClearPAT] = useState(false);
  const [savingSettings, setSavingSettings] = useState(false);
  const [settingsFlash, setSettingsFlash] = useState<string | null>(null);
  const [settingsErr, setSettingsErr] = useState<string | null>(null);

  // Push panel state.
  const [branch, setBranch] = useState("");
  const [commitMsg, setCommitMsg] = useState("Update configs from dashboard");
  const [pushing, setPushing] = useState(false);
  const [pushOutput, setPushOutput] = useState<string | null>(null);
  const [pushErr, setPushErr] = useState<string | null>(null);
  const [pushBrowseURL, setPushBrowseURL] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const s = await api.getGitSettings();
        setSettings(s);
        setBaseURL(s.base_url);
        setProject(s.project);
        setDefaultBranch(s.default_branch);
        setAuthorName(s.author_name);
        setAuthorEmail(s.author_email);
        setBranch(s.default_branch);
      } catch (e) {
        setLoadErr((e as Error).message);
      }
    })();
  }, []);

  const repoURL = baseURL && project
    ? `${baseURL.replace(/\/$/, "")}/${project}`
    : null;

  async function saveSettings(e: React.FormEvent) {
    e.preventDefault();
    setSavingSettings(true);
    setSettingsFlash(null);
    setSettingsErr(null);
    try {
      const { status, data } = await api.saveGitSettings({
        base_url: baseURL,
        project,
        default_branch: defaultBranch,
        author_name: authorName,
        author_email: authorEmail,
        pat: pat || undefined,
        clear_pat: clearPAT || undefined,
      });
      if (status !== 200 || !data.ok) throw new Error(`save failed (${status})`);
      setSettingsFlash("Saved.");
      setPat("");
      setClearPAT(false);
      // Reload to pick up the new pat_configured flag.
      const s = await api.getGitSettings();
      setSettings(s);
    } catch (e) {
      setSettingsErr((e as Error).message);
    } finally {
      setSavingSettings(false);
    }
  }

  async function doPush() {
    setPushing(true);
    setPushOutput(null);
    setPushErr(null);
    setPushBrowseURL(null);
    try {
      const { status, data } = await api.gitPush(branch, commitMsg);
      if (status === 200 && data.ok) {
        setPushOutput(data.output ?? "");
        setPushBrowseURL(data.browse_url ?? null);
      } else {
        setPushErr(data.error ?? `push failed (${status})`);
        setPushOutput(data.output ?? null);
      }
    } catch (e) {
      setPushErr((e as Error).message);
    } finally {
      setPushing(false);
    }
  }

  if (loadErr) return <div className="empty">Error: {loadErr}</div>;
  if (!settings) return <div className="loading">Loading…</div>;

  const readyToPush =
    settings.pat_configured && settings.base_url && settings.project && settings.secret_key_set;

  return (
    <>
      <h1 style={{ display: "flex", alignItems: "center", gap: 12 }}>
        GitLab
        {repoURL && (
          <a
            href={repoURL}
            target="_blank"
            rel="noreferrer"
            className="btn"
            style={{ marginLeft: "auto", textDecoration: "none", borderColor: "#fc6d26", color: "#fc6d26" }}
          >
            View repo in browser
          </a>
        )}
      </h1>

      {!settings.secret_key_set && (
        <div className="diag error" style={{ marginBottom: 16 }}>
          The server has no <code>JIGSAW_GIT_SECRET_KEY</code> configured, so PATs cannot be stored
          or read. Set the env var on the dashboard process and restart before saving credentials.
        </div>
      )}

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16 }}>
        <section className="row" style={{ flexDirection: "column", alignItems: "stretch", gap: 12 }}>
          <h2 style={{ margin: 0 }}>Settings</h2>
          <div className="meta">Per-user. Stored encrypted in the auth file on the server.</div>
          <form onSubmit={saveSettings} style={{ display: "grid", gridTemplateColumns: "max-content 1fr", gap: "10px 12px" }}>
            <label className="meta">base URL *</label>
            <input
              className="input"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder="https://gitlab.example.com"
            />

            <label className="meta">project *</label>
            <input
              className="input"
              value={project}
              onChange={(e) => setProject(e.target.value)}
              placeholder="group/repo"
            />

            <label className="meta">default branch</label>
            <input
              className="input"
              value={defaultBranch}
              onChange={(e) => setDefaultBranch(e.target.value)}
              placeholder="main"
            />

            <label className="meta">author name</label>
            <input
              className="input"
              value={authorName}
              onChange={(e) => setAuthorName(e.target.value)}
              placeholder="(falls back to your username)"
            />

            <label className="meta">author email</label>
            <input
              className="input"
              type="email"
              value={authorEmail}
              onChange={(e) => setAuthorEmail(e.target.value)}
              placeholder="(falls back to <username>@gitlab.local)"
            />

            <label className="meta" style={{ alignSelf: "start", paddingTop: 6 }}>PAT</label>
            <div>
              <input
                className="input"
                type="password"
                value={pat}
                onChange={(e) => setPat(e.target.value)}
                placeholder={settings.pat_configured ? "•••••••• (leave blank to keep)" : "paste a personal access token"}
                style={{ width: "100%" }}
                disabled={!settings.secret_key_set || clearPAT}
              />
              <div className="meta" style={{ marginTop: 4 }}>
                Needs <code>write_repository</code> scope.{" "}
                {settings.pat_configured ? "A token is currently stored." : "No token stored yet."}
              </div>
              {settings.pat_configured && (
                <label className="meta" style={{ marginTop: 6, display: "flex", alignItems: "center", gap: 6 }}>
                  <input
                    type="checkbox"
                    checked={clearPAT}
                    onChange={(e) => { setClearPAT(e.target.checked); if (e.target.checked) setPat(""); }}
                  />
                  Remove the stored token on save
                </label>
              )}
            </div>

            <span />
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <button type="submit" className="btn btn-primary" disabled={savingSettings || !settings.secret_key_set}>
                {savingSettings ? "Saving…" : "Save settings"}
              </button>
              {settingsFlash && <span className="meta" style={{ color: "var(--accent)" }}>{settingsFlash}</span>}
            </div>
          </form>
          {settingsErr && <div className="diag error">{settingsErr}</div>}
        </section>

        <section className="row" style={{ flexDirection: "column", alignItems: "stretch", gap: 12 }}>
          <h2 style={{ margin: 0 }}>Push</h2>
          <div className="meta">Replaces the remote branch's tracked content with the current config tree, then commits and pushes as you.</div>
          {!readyToPush && (
            <div className="diag warning">
              {!settings.secret_key_set
                ? "Set JIGSAW_GIT_SECRET_KEY on the server."
                : !settings.pat_configured
                  ? "Add a PAT in the settings panel first."
                  : "Set base URL and project in the settings panel."}
            </div>
          )}
          <div style={{ display: "grid", gridTemplateColumns: "max-content 1fr", gap: "10px 12px" }}>
            <label className="meta">branch *</label>
            <input
              className="input"
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              placeholder={defaultBranch || "main"}
            />
            <label className="meta" style={{ alignSelf: "start", paddingTop: 6 }}>commit message *</label>
            <textarea
              className="input"
              rows={3}
              value={commitMsg}
              onChange={(e) => setCommitMsg(e.target.value)}
              style={{ width: "100%", fontFamily: "var(--mono)", fontSize: 12 }}
            />
          </div>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <button
              className="btn btn-primary"
              onClick={doPush}
              disabled={pushing || !readyToPush || !commitMsg.trim() || !branch.trim()}
            >
              {pushing ? "Pushing…" : "Push to GitLab"}
            </button>
            {pushBrowseURL && (
              <a href={pushBrowseURL} target="_blank" rel="noreferrer" className="btn" style={{ textDecoration: "none" }}>
                Open branch ↗
              </a>
            )}
          </div>
          {pushErr && <div className="diag error">{pushErr}</div>}
          {pushOutput !== null && (
            <pre
              style={{
                background: "var(--bg)", border: "1px solid var(--border)", borderRadius: 4,
                padding: 10, fontSize: 11, fontFamily: "var(--mono)",
                whiteSpace: "pre-wrap", maxHeight: 320, overflow: "auto",
              }}
            >
              {pushOutput || "(no output)"}
            </pre>
          )}
        </section>
      </div>
    </>
  );
}
