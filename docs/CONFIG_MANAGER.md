# Configuration Manager — Design Proposal (v2)

Status: **DRAFT / RFC**. Incorporates author feedback on v1.

## 1. Goals

Provide a safe authoring surface for Jigsaw configurations so humans don't hand‑edit YAML and produce subtle inconsistencies (missing logic handlers, dangling task references, type mismatches, broken inheritance).

Two co‑equal authoring surfaces, sharing one validation core:

1. **Graph dashboard** — minimal, futuristic, mouse‑driven editor. The primary surface for non‑developer contributors.
2. **Text editor experience** — `.jig.yml` / `.jig.yaml` files edited in any LSP‑aware editor (vim, VS Code, Monaco in the browser). Same diagnostics as the dashboard.

The TUI gains only a `jigsaw check` style command that runs the same diagnostics. No interactive TUI editing.

## 2. Operating modes

The manager runs in one of two modes. The mode controls auth, write semantics, and what the "save" action produces.

| Mode | How it starts | Auth | Write target | Use case |
|------|---------------|------|--------------|----------|
| **Local** | `jigsaw ui web --edit --local` *or* `jigsaw ui web --edit` against a local `configPath` | Bypassed | Writes files into the working `configs/` tree | Developer on their laptop |
| **Server** | Embedded in the consumer's binary via `jigsaw.ServeConfigManager(...)` *or* `jigsaw ui web --edit --serve --listen host:port` | **Required** (see §3) | Produces a downloadable **tar bundle** of the new config tree; never mutates the running process's live configs | Hosted UI sitting next to a production deployment |

Server mode never writes in place. The deliverable from a server‑mode session is a tarball the user downloads and applies to their config repo (commit + push) using their own workflow. Git is therefore the sync mechanism between the dashboard and production. **Race conditions and multi‑user editing are intentionally out of scope** — git handles them.

### 2.1 Flag layout

Three places set behavior; they layer:

1. **Package init (Go code)** — `jigsaw.WithConfigManager(opts ConfigManagerOptions)` when constructing the engine. Options:
   - `Enabled bool`
   - `Listen string` (e.g. `127.0.0.1:3000`)
   - `Auth AuthProvider` (interface — see §3). Nil + non‑loopback bind = startup error.
   - `Mode Mode` (`Local` | `Server`). Default `Server` when used from the package.
2. **HTTP endpoint registration** — the consumer can choose to mount the manager under an existing `*http.ServeMux` / gin engine rather than letting Jigsaw open a port. Lets the consumer's auth middleware front the manager naturally.
3. **CLI** — `jigsaw ui web` flags:
   - `--edit` enable mutations. Default off, preserves today's read‑only behavior.
   - `--local` force local mode (bypass auth, write files in place). Default when `configPath` is a local directory and `--listen` is loopback.
   - `--serve` force server mode (auth required, output is tar). Default when `--listen` is non‑loopback.
   - `--listen host:port` bind address. Non‑loopback requires `--serve` and auth configuration.
   - `--allow-remote` explicit acknowledgement when binding to `0.0.0.0` even with auth.

## 3. Authentication & authorization

Local mode: no auth. The OS already gates access to the developer's machine.

Server mode: auth is mandatory. The manager refuses to start without it.

### 3.1 Bearer tokens only (v1)

One mechanism: static bearer tokens. Small, easy to audit, no session/cookie surface. Custom auth stays available as an escape hatch for orgs that already have SSO middleware.

```go
jigsaw.WithConfigManager(jigsaw.ConfigManagerOptions{
    Mode:   jigsaw.Server,
    Listen: "0.0.0.0:3000",
    Auth: jigsaw.BearerTokens(map[string]jigsaw.TokenInfo{
        "tok_abc...": {Label: "alice", Role: jigsaw.RoleAdmin},
        "tok_xyz...": {Label: "bob",   Role: jigsaw.RoleViewer},
    }),
})
```

Or, for an externally managed auth layer:

```go
Auth: jigsaw.CustomAuth(func(r *http.Request) (jigsaw.Identity, error) { ... })
```

Clients send `Authorization: Bearer <token>`. No `/login` page; no cookies; 401 on missing/invalid token.

### 3.2 Two roles

| Role | Can do |
|------|--------|
| **admin** | Read everything, edit, save (local), download bundle (server) |
| **viewer** | Read everything; mutating endpoints return 403 |

The role comes from the token. The dashboard hides edit affordances for viewers (no `+ Flow` button, no side‑panel save) — they still render to keep the server honest, but the API is the authoritative gate.

Custom auth providers return an `Identity{Label, Role}`; the consumer maps their own users to one of the two roles.

### 3.3 Audit trail

Git is the audit trail. Every change reaches production via a commit reviewed by a human, so "who changed what" is already recorded with full context (diff, message, reviewer). The manager itself does not maintain a separate audit log.

For visibility while the dashboard is running, denied requests still surface in normal server logs (a single `WARN` line on 403), but successful saves are not logged — they will appear in `git log` shortly after.

## 4. Output: file mode vs bundle mode

### 4.1 Local (file mode)

A successful save writes the modified files in place, atomically (tmp file → fsync → rename). Multi‑file layout (today's `tasks/*.yml`, `flows/*.yml`, etc.) is preserved by default. The user can opt into single‑file mode if they prefer; see §5.4.

### 4.2 Server (bundle mode)

A successful save stages changes in an in‑memory overlay. The user clicks **Download config bundle** and gets `jigsaw-config-<timestamp>.tar.gz` containing the full new `configs/` tree. They drop it into their repo (`tar -xzf … -C configs/`), review the diff, commit, push. Production picks it up on next deploy or via hot‑reload.

Because the entire config tree ships as one unit, partial updates are impossible by construction. This is what makes hot‑reload safe: at any moment the running engine sees a complete, validated config. **In‑flight flow executions continue on the old config; new executions pick up the new one.** (This is already the behavior of the existing reloader; this design relies on it, doesn't change it.)

There is no auto‑commit. The user commits.

## 5. Dashboard

Design language: minimal, futuristic. Optimized for mouse‑only authoring. Developer surfaces (raw YAML view, diagnostics panel) are present but tucked behind a toggle.

### 5.1 Pages

| Page | Purpose |
|------|---------|
| Overview | Counts, unimplemented logic, validation status |
| Flows | List + graph editor per flow |
| Tasks | Cards / form editor; cross‑refs to flows that use the task |
| Providers | Cards / form editor; cross‑refs to tasks that use the provider |
| Endpoints | Form editor; sub → flow mapping |
| Logic registry | Read‑only — what the consumer binary registered |
| Templates | Boilerplate browser (see §5.5) |
| Pending changes | Unified diff of dirty buffers vs disk; "save" (local) / "download bundle" (server) / "discard" |

### 5.2 Graph editor (flows)

Library: **React Flow** (MIT, n8n‑style, mature). The graph is a **view over the AST** — every visual edit produces an AST patch which re‑renders the graph. Single source of truth, no drift.

Mouse‑only authoring:

1. `+ Flow` → modal asks only required fields; everything else editable from the side panel later.
2. Right‑click canvas → `Add task` → searchable picker scoped to **registered + configured** tasks. Unknown task names can only appear via raw‑YAML editing (where they show as red diagnostics).
3. Selecting a node opens the right side panel with the task's editable fields. Free‑text fields share the linter with the text editor and show squiggles inline.
4. `+ Parallel group` creates a container node; tasks dragged inside become its children.
5. Edges reorder the underlying `tasks: []` list.

### 5.3 Undo / redo

Required, asked for explicitly.

Implementation: the AST‑patch model makes this natural. Each user action emits a patch; patches push onto a per‑document stack. `Cmd/Ctrl‑Z` pops and applies the inverse; `Cmd/Ctrl‑Shift‑Z` redoes. Stack is per browser session (does not survive reload — that's what "save" is for). Stack cap at e.g. 200 patches to bound memory.

This is the right approach because the same patches are what we'd send to a future collaborative backend, and they make `dry‑run` (§5.6) cheap (just snapshot the AST, apply patches, validate).

### 5.4 File layout choices

Default: multi‑file, organized by resource type, one resource per file named after the resource (`tasks/search_db.jig.yml`, `flows/advanced_search.jig.yml`). This matches the author's stated preference.

A toggle in **Settings** offers two alternates:

- **Multi‑file grouped** (today's layout): `tasks/common.yml`, `tasks/cache.yml`, multiple resources per file.
- **Single‑file**: the entire config in one `jigsaw.jig.yml`. Useful for small projects and for the bundle download.

The chosen layout is recorded in a `.jigsawconfig` file in the config root so it survives sessions.

### 5.5 Templates vs inherits

Both are kept and are explicitly different:

- **Templates** are boilerplates the user browses and copies. Clicking "Use template" creates a real, independent resource in the user's config tree from the template. After instantiation there is no link back to the template.
- **`inherits:`** is the existing runtime inheritance mechanism. A child resource references a parent; changes to the parent affect the child at load time.

The UI labels them clearly: templates have a "📋 Copy" action, inheritance is shown as a "↳ inherits from X" link on the resource.

### 5.6 Dry‑run (planned, documented)

A `▶ Dry‑run` button on a flow opens a panel where the user provides sample input and sees the executor's plan: ordered task list, parallel groups, resolved inputs/outputs per step, expected types. No side effects — providers are stubbed.

v1: document the design, do not implement. Plumbing: reuse `pkg/engine` with a `DryRun bool` flag that short‑circuits provider calls.

### 5.7 Search and find‑references

- `Cmd/Ctrl‑K` palette: jump to any task/flow/provider/endpoint by name.
- Right‑click any resource → **Find references** → popover lists every place the resource is mentioned (flows that use a task, tasks that use a provider, endpoints that map to a flow). Click to jump.

### 5.8 Read‑only snapshot link

In server mode, a session can mint a tokened URL (`/snapshot/<id>`) that serves the current dashboard state read‑only. Token has a TTL and can be revoked. Useful for sharing a design with a reviewer who shouldn't have edit rights.

### 5.9 Export

`Export → PNG` and `Export → SVG` from the graph editor toolbar. Pure client‑side, uses React Flow's `toImage` helpers.

### 5.10 YAML view

A toggle on every resource page reveals the raw YAML for that resource (Monaco, full LSP). Edits save back through the same pipeline. Hidden by default so the mouse‑only path stays clean.

## 6. File format: `.jig.yml` / `.jig.yaml`

YAML stays the underlying format. The `.jig.yml` extension is the trigger for Jigsaw‑specific tooling:

- Editor associations (`*.jig.yml` → Jigsaw LSP) without overriding plain `.yml`.
- Allows YAML parsers, linters, schema validators (`yaml-language-server` + `jigsaw.schema.json`) to keep working unchanged.
- The formatter (`jigsaw fmt`) recognizes the extension and applies canonical layout.

Existing `*.yml` files in `configs/` keep working. New files written by the dashboard use `.jig.yml`. A `jigsaw fmt --rename` flag can opt‑in to migrating extensions.

## 7. LSP and CLI tooling

One LSP server (`pkg/lsp`), three consumers: Monaco in the browser (over WebSocket), VS Code extension, vim/neovim.

Diagnostics (same set in dashboard, LSP, and `jigsaw check`):

- Unknown task / provider / flow / endpoint references → **error**.
- Task references a `logic:` handler not in the registry snapshot → **warning** (could be registered by a peer binary; see §8).
- Inheritance cycles, missing parents → **error**.
- Type mismatches in `inputs` / `outputs` against task / logic handler schemas → **error** when schemas are available, otherwise no signal.
- Endpoint `sub` collisions → **error**.
- Templated values that fail to resolve → **error**.
- Style: trailing whitespace, inconsistent indentation → formatter‑level (not LSP diagnostics).

CLI:

```
jigsaw check ./configs                    # diagnostics, exits non‑zero on error
jigsaw check ./configs --format=github    # GitHub Actions annotations for PR CI
jigsaw fmt ./configs                      # canonical formatter
jigsaw fmt ./configs --check              # exits non‑zero if anything would change
```

`--format=github` covers the CI‑mode request: PRs that touch configs get inline diagnostics on the affected lines.

## 8. Registered‑symbols problem

The dashboard needs to know what the consumer's binary registered (logic handlers, providers, tasks). The dashboard binary itself doesn't import the consumer's code. Two delivery paths, both supported:

### 8.1 Manifest mode (for the `jigsaw` CLI on a developer laptop)

The consumer's binary exposes `--dump-symbols` (or runs it on every startup) that writes `./.jigsaw/symbols.json`:

```json
{
  "logic":     [{"name":"search.byID", "inputs":{...schema...}, "outputs":{...schema...}}],
  "providers": [{"name":"mysql", "type":"sql"}],
  "tasks":     ["..."],
  "generated_at": "2026-05-13T...",
  "binary":   "myapp"
}
```

The dashboard reads it on startup and refreshes on focus. A stale manifest is clearly labelled in the UI ("manifest is 2h old — symbols may be outdated").

### 8.2 Live mode (for hosted server deployments)

The consumer embeds the manager directly: `jigsaw.ServeConfigManager(ctx, engine, opts)`. The manager has live access to the engine's registries — no manifest file, no staleness. This is the recommended path for "hosted dashboard sitting next to the main service."

The CLI also supports `--symbols-server http://prod:8080/_jigsaw/symbols` to point at a running consumer binary that exposes a read‑only symbols endpoint, for the case where the developer wants the same fidelity without the file dance.

### 8.3 Logic handler schemas (in‑browser type checking)

The consumer can register handlers with schemas:

```go
engine.RegisterLogic("search.byID", handler,
    jigsaw.WithInputSchema(searchInputSchema),
    jigsaw.WithOutputSchema(searchOutputSchema))
```

When schemas are present, the validator performs real type checks against task `inputs:` / `outputs:` blocks. When absent, it falls back to reference checking only. This is opt‑in per handler, so it can be adopted incrementally.

## 9. Schema versioning

Every config file declares its schema:

```yaml
apiVersion: jigsaw/v1
kind: Flow
```

`apiVersion` lets the loader and LSP warn on future migrations and lets `jigsaw fmt` perform automated upgrades. v1 is the current shape; nothing forced on existing users beyond a one‑time `fmt --add-version`.

## 10. Frontend separation

The frontend lives under `web/` in this repo for v1 (kept clean and decoupled — no shared state with Go beyond the REST/WS contract) so it can be lifted into its own repo later without code surgery. Specifically:

- All API access goes through a single `src/api/` client. No fetch calls scattered across components.
- No Go templating (`html/template`) inside React routes. The Go side serves a static SPA + JSON API.
- The OpenAPI spec for the REST API is checked into `web/openapi.yaml` and the TS client is generated from it.

If the embedded JS bundle ever becomes a footprint problem, the frontend repo split is then mechanical: copy `web/`, point at the same OpenAPI spec, ship from a CDN.

## 11. Engineering risks

1. **Comment & order preservation on save remains the #1 risk.** `gopkg.in/yaml.v3` Node round‑trip is workable but has sharp edges. This is the single feature that distinguishes "an editor people use" from "an editor that destroys their files on first save." Budget real time.
2. **Concurrent editors — out of scope.** Single‑user policy is assumed. No mtime checks, no locks, no warnings. If two people edit at once, git's merge review catches it on the way to production.
3. **Hot‑reload safety.** Atomic rename + the existing reloader's whole‑file load semantics + "ship the entire tree as one unit" rule (server mode) means partial states are unreachable. In‑flight executions keep their original config snapshot.
4. **Symbol manifest staleness (local CLI path).** UI labels the manifest age. Refusing to validate on a stale manifest would be more correct but less ergonomic; we choose ergonomic + visible warning.
5. **Auth surface area.** Bearer + custom is a small, auditable surface.
6. **Schema drift between LSP and dashboard.** Mitigated by both speaking through `pkg/configlang` + `pkg/validator`. No second implementation.

## 12. Out of scope (v1)

- Auto‑commit / commit message generation. User commits.
- CRDT‑style multi‑user editing. Git is the sync mechanism.
- TUI interactive editor. CLI only gets `check` / `fmt`.
- Custom non‑YAML format. `.jig.yml` stays YAML under the hood.
- Dry‑run execution. Documented, deferred.
- Importing from other orchestrators.

## 13. Phasing

1. ✅ **Foundation** — `pkg/configlang` (AST + round‑trip), `jigsaw check`, `jigsaw fmt`. User docs: [`CONFIG_TOOLING.md`](CONFIG_TOOLING.md).
2. ✅ **Symbols (manifest)** — `pkg/symbols`, `jigsaw dump-symbols`, `jigsaw check --manifest=...`. Logic‑handler cross‑check is live.
3. ✅ **Symbols (schemas)** — `engine.WithInputSchema` / `WithOutputSchema` options. Validator + LSP do real input/output type checking when schemas are declared.
4. ✅ **LSP** — `pkg/lsp`, `jigsaw lsp`. Workspace‑level diagnostics on open/change/save. Line‑precise locations and hover/completion deferred.
5. ✅ **Dashboard read** — `pkg/dashboard` (new), Vite+React SPA at `web/`, `jigsaw dashboard`. Local + server modes, bearer auth (server), two‑tier roles. Read‑only views: overview, flows, tasks, providers, endpoints, logic registry, diagnostics.
6. ✅ **Dashboard write (local mode)** — Monaco YAML editor, save pipeline with parse → format → validate → atomic write. Files‑level API: `/api/tree`, `/api/file`, `/api/files`.
   - ✅ **6a Graph editor (React Flow)** — visualize and edit a flow with the mouse: add task (palette modal), delete, drag‑to‑reorder, side panel inspector, undo/redo (Cmd/Ctrl‑Z), save via the existing pipeline. Parallel blocks are visualized but not yet mouse‑editable.
7. ✅ **Server mode (write)** — `/api/bundle` streams a validated `tar.gz` of the full overlay; user downloads it, extracts over their repo, commits. CLI exposes `--mode=server --admin-token=… --viewer-token=…`.
8. **Polish** — find references, Cmd‑K search, PNG/SVG export, GitHub annotations (✅ already in `jigsaw check --format=github`), snapshot links.
9. **Future** — dry‑run, fancier diff viewer, possibly a dedicated frontend repo.

## 14. Decisions locked

- **Auth**: bearer tokens only in v1 + `CustomAuth` escape hatch. No Basic, no OIDC.
- **Roles**: two tiers — `admin` (edit) and `viewer` (read‑only). Token carries the role.
- **Audit trail**: git commits are the audit trail. The manager does not maintain its own log.
- **Concurrency safeguards**: none. Single‑user assumption; git review handles conflicts.
- **Bundle scope**: always the whole `configs/` tree. Eliminates "forgot a file" bugs and matches the ship‑the‑whole‑process rule.
- **Snapshot links**: a viewer‑role token is sufficient — no separate snapshot auth mechanism. Snapshots are deferred to phase 7 polish.
- **Layout preference**: stored in `.jigsawconfig` (small JSON, top of the config tree). Single new dotfile, no migration of existing files.
