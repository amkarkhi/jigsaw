# Dashboard — Features & Operator Guide

Companion to `docs/CONFIG_MANAGER.md` (design RFC). This file describes the
**shipped** feature surface of the configuration dashboard and how to wire
it up. The dashboard runs in either `local` mode (developer-on-laptop,
no auth, writes in place) or `server` mode (auth required, multi-user).

```
jigsaw dashboard --mode=local  --listen=127.0.0.1:3300 --edit      # solo dev
jigsaw dashboard --mode=server --listen=0.0.0.0:3300  --edit ...   # hosted
```

Server-mode operator config lives in two places:

- **Auth file** (`<configPath>/.jigsaw/auth.json`) — users, password hashes,
  bearer tokens, per-user GitLab settings. Managed via the `jigsaw user` /
  `jigsaw token` CLI or the in-app `/users` page.
- **Environment variables** — `JIGSAW_GITLAB_*` (SSO),
  `JIGSAW_GIT_SECRET_KEY` (PAT encryption). Both detailed below.

The pages and APIs below all assume `--edit` is set; without it the
dashboard is read-only.

---

## 1. Authentication

### Password login

Initialize once per config tree:

```bash
jigsaw user init --master-key "$(openssl rand -hex 24)"
jigsaw user create --username alice --password '<strong>' --role admin \
  --master-key '<master>'
```

Sign in at `/login`.

### Bearer tokens (CI / scripts)

```bash
jigsaw token create --name ci-bot --role viewer --master-key '<master>'
# copy the printed token (shown once)
```

Use as `Authorization: Bearer <token>`.

### GitLab SSO

Standard OAuth 2.0 Authorization Code flow against any self-hosted GitLab
or `gitlab.com`. Configure with:

| Env var / flag | Required | Purpose |
|---|---|---|
| `JIGSAW_GITLAB_BASE_URL` / `--gitlab-base-url` | yes | e.g. `https://gitlab.example.com` |
| `JIGSAW_GITLAB_CLIENT_ID` / `--gitlab-client-id` | yes | OAuth application id |
| `JIGSAW_GITLAB_CLIENT_SECRET` / `--gitlab-client-secret` | yes | OAuth application secret |
| `JIGSAW_GITLAB_REDIRECT_URL` / `--gitlab-redirect-url` | yes | Must match the registered URI exactly |
| `JIGSAW_GITLAB_DEFAULT_ROLE` / `--gitlab-default-role` | no | Role granted on first SSO login. Default `viewer` |

When all four required fields are set, the "Sign in with GitLab" button
renders on `/login`. First-time logins auto-provision the user in
`auth.json` with the default role and a random unrecoverable bcrypt hash
(no password login possible for SSO accounts). Subsequent logins reuse
the role recorded in the file — admin promotions stick.

Walkthrough: `docs/TESTING_SSO.md`.

---

## 2. Authorization model

Two-axis: **role** + **access list**.

| Role | Means |
|---|---|
| `admin` | All access. Sees the Users page. |
| `viewer` | Read-only. No edits permitted regardless of access list. |

The **access list** is a subset of `{flows, tasks, providers, endpoints}`.
It's ignored for admins (who always have everything) and viewers (who
have nothing). For non-admin "editor" identities the access list gates
which resource files they may write.

### Server-side enforcement

- `POST /api/files` reads each path's top-level directory and rejects
  with 403 if the caller lacks that resource. `.jigsaw/*` is always
  rejected so auth secrets can't be overwritten through the editor.
- `POST /api/git/push` requires authentication + `--edit`; non-admins
  push the same content but can only have authored files they have
  access to, since `/api/files` previously gated them.
- `/api/users*` endpoints all require `admin`.

### Managing users in-app

`/users` (admin-only) lists everyone in `auth.json` — including
SSO-provisioned accounts — and lets admins:

- Set role (`admin` / `viewer`)
- Set email
- Tick / untick per-resource access checkboxes
- Create new password users
- Delete users (self-delete blocked by the server)

---

## 3. Flow graph editor (`/flows/:name`)

The graph editor compiles a free-form DAG to Jigsaw's sequential YAML
representation and back. Highlights:

- **Right-click on the pane** → Add task at cursor / Insert 2-step
  boilerplate / Insert flow as template. The cursor position is
  converted from viewport to graph coords (`screenToFlowPosition`) so
  zoom and pan don't displace the new node.
- **Insert flow as template** opens a flow picker, copies the source
  flow's nodes + edges with fresh ids at the cursor, preserving the
  source's relative layout. Drop the same template multiple times — ids
  never collide.
- **Auto-arrange** uses a layered DAG layout: each node's column is the
  barycenter of its predecessors' columns, so branches stay in their
  own lane instead of collapsing to canvas center.
- **Parallel branches**: flat parallels render with no synthetic fork /
  join nodes. Each node in a branch shows an `⌥ branch_x` chip; renaming
  it in the inspector propagates to every node in the same branch and
  round-trips into `parallel.branches[].label` on save.
- **Persistent layout**: node positions are written into
  `flow.metadata.layout` in the YAML (the engine ignores `metadata`), so
  positions travel with `git push`. A server-side sidecar
  (`.jigsaw/layouts/<flow>.json`) is also written for legacy reads.
- **Move-to-save**: dragging a node registers a history entry and marks
  the editor dirty so Save enables for layout-only changes.
- **Per-placement labels**: layout keys are `taskName@label` so a task
  placed multiple times in the same flow keeps distinct positions.
- **Drafts**: "Save draft" stashes the current graph as YAML in
  `localStorage` (`jigsaw:draft:flow:<name>`). "Drafts" opens a list
  with Load / Rename / Delete. Pure browser — never touches the
  server, useful for demos and scratch.

---

## 4. Resource pages

- **`/providers`** — list + `+ New provider` modal. Writes
  `providers/<name>.yml` directly via `/api/files`.
- **`/endpoints`** — list + `+ New endpoint` modal. Each card has
  `+ Flow` to add a `(sub, flow_name)` mapping (auto-suggests the
  smallest unused sub) and `×` to remove one. Backed by a new
  `/api/endpoint-location` lookup.
- **`/tasks`** — existing list / detail pages.
- **`/editor`** — raw multi-file editor.

---

## 5. Playground (`/playground`)

Dry-run a flow against test inputs to trace per-task data and pinpoint
failures.

```
POST /api/playground/run
{
  "flow":   "search",
  "inputs": { "query": "hello" },
  "sub":    1
}
```

Response carries an ordered `tasks: [{ name, label, status, inputs,
outputs, error, duration_ms, ... }]`. The frontend renders each task
as a collapsible card with inputs and outputs side-by-side.

**This is a sandbox, not the real engine.** The dashboard binary doesn't
carry your service's logic handlers, so tasks land in the engine's
built-in "echo inputs as outputs" fallback. Provider lookups are
answered by a stub registry that performs zero I/O. Result: you see
the data shape flowing through every task, with no real backends
touched. The UI surfaces this caveat above the trace.

---

## 6. GitLab push (`/git`)

Per-user pipeline that overwrites the remote branch's tracked content
with the live config tree, then commits and pushes as the logged-in
user.

### One-time setup (operator)

```bash
export JIGSAW_GIT_SECRET_KEY="$(openssl rand -hex 32)"   # any string ≥ 32 chars
jigsaw dashboard --mode=server --edit ...
```

Without `JIGSAW_GIT_SECRET_KEY`, PATs cannot be stored or read; the
push page will refuse and the settings form will be disabled.

### Per-user setup (in the UI)

`/git` page → **Settings** panel:

| Field | Notes |
|---|---|
| base URL | e.g. `https://gitlab.example.com`, no trailing slash |
| project | `group/repo` |
| default branch | optional, used as the push branch fallback |
| author name | git commit author override (defaults to your username) |
| author email | defaults to `<username>@gitlab.local` |
| PAT | needs `write_repository` scope. Write-only from the UI |

PATs are encrypted with AES-256-GCM using a key derived from
`JIGSAW_GIT_SECRET_KEY` (SHA-256 of the string) and stored under
`AuthFile.GitSettings[username].EncPAT`. They're decrypted only at push
time and never logged — the response transcript redacts the token in
any URL echo.

### Pushing

`/git` page → **Push** panel: commit message + branch (defaults to the
configured default). Click **Push to GitLab**. The dashboard:

1. clones the target branch (`--depth 1`); falls back to default + local
   `checkout -B <branch>` if the branch doesn't exist
2. wipes the working tree except `.git/`
3. copies the live config tree in, **skipping `.jigsaw/`** so auth
   secrets never leave the server
4. `git add -A`; bails with "nothing to commit" if the tree matches
5. commits with the configured author identity (`GIT_AUTHOR_*` /
   `GIT_COMMITTER_*` env-overrides)
6. `git push -u origin <branch>`

On success the panel shows the redacted transcript and a **Open
branch ↗** link.

### Header buttons

- **View repo in browser** — opens `<base>/<project>` in a new tab.
- **Open branch ↗** — appears after a successful push, points at
  `<base>/<project>/-/tree/<branch>`.

---

## 7. API reference (added in this round)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/auth-info` | public | Which auth methods are enabled (password, gitlab) |
| GET | `/auth/gitlab/login` | public | Start the SSO flow |
| GET | `/auth/gitlab/callback` | public | Complete the SSO flow |
| GET | `/api/users` | admin | List users + canonical resources |
| POST | `/api/users` | admin | Create password user |
| PATCH | `/api/users/{name}` | admin | Update role / email / access |
| DELETE | `/api/users/{name}` | admin | Delete user |
| GET | `/api/git/settings` | auth | Current user's GitLab settings (no PAT) |
| POST | `/api/git/settings` | auth | Save GitLab settings (incl. PAT) |
| POST | `/api/git/push` | auth + edit | Run the push pipeline |
| POST | `/api/playground/run` | auth | Dry-run a flow with traces |
| GET | `/api/endpoint-location` | auth | Resolve `<name>` to its file path |

`/api/me` now also returns `access: string[]` so the SPA can hide
forbidden actions.

---

## 8. CLI summary (added flags)

```
jigsaw dashboard
  --gitlab-base-url=URL          # JIGSAW_GITLAB_BASE_URL
  --gitlab-client-id=ID          # JIGSAW_GITLAB_CLIENT_ID
  --gitlab-client-secret=SECRET  # JIGSAW_GITLAB_CLIENT_SECRET
  --gitlab-redirect-url=URL      # JIGSAW_GITLAB_REDIRECT_URL
  --gitlab-default-role=ROLE     # JIGSAW_GITLAB_DEFAULT_ROLE (admin|viewer)
                                 # JIGSAW_GIT_SECRET_KEY (env-only; PAT encryption)
```

Existing flags (`--mode`, `--edit`, `--listen`, `--admin-token`, etc.)
are unchanged.

---

## 9. Operational notes

- The dashboard never mutates `.jigsaw/auth.json` outside of the
  user-management endpoints. CLI changes (`jigsaw user create`, etc.)
  take effect for new logins immediately; existing sessions cache role
  + access until they expire (12 h).
- Layout in `metadata.layout` is the new source of truth. The sidecar
  is still written for backward compatibility but can be ignored or
  deleted on greenfield installs.
- Playground spins up a fresh `FlowExecutor` per request; reloads
  config from disk so it sees your latest saved edits.
- GitLab push is intentionally "snapshot replaces remote tree" rather
  than incremental — the dashboard isn't a merge tool. Pull-request
  workflows live in GitLab itself.
