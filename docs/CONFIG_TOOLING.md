# Config tooling: `check`, `fmt`, `dump-symbols`, `lsp`

Phases 1–4 of the Configuration Manager (see `CONFIG_MANAGER.md`) ship four
CLI tools, three Go packages, and a working language server. They are usable
today; the dashboard builds on top of the same foundations.

## Quick reference

| Command | What it does |
|---------|--------------|
| `jigsaw check` | Run all diagnostics over a config tree. Exits 1 on any error. |
| `jigsaw fmt` | Rewrite every config file in canonical style. `--check` for CI dry-run. |
| `jigsaw dump-symbols` | Write a symbols manifest from the current config + (optionally) the running engine's registered handlers. |
| `jigsaw lsp` | Run the Jigsaw language server on stdin/stdout. Editors attach to this command. |
| `jigsaw dashboard` | Launch the configuration dashboard (web UI). Read-only in Phase 5. |

All three accept the global `--config <path>` flag (default `./configs`).

## `jigsaw check`

Runs the validator over the config and emits diagnostics. Output is one
line per finding:

```
configs/flows/advanced_search.yml: error: flow 'advanced_search' has unknown task 'foo'
```

When a symbols manifest is available, the checker also warns on tasks
that reference logic handlers not in the manifest:

```
<unknown>: warning: task "search_execute" references logic handler "execute_search" which is not in the registry snapshot
```

Manifest resolution:

| `--manifest` | Behavior |
|--------------|----------|
| `auto` (default) | Reads `<config>/.jigsaw/symbols.json` if present; silent if absent. |
| `path/to/file.json` | Reads the explicit path; missing file is a hard error. |
| `''` (empty) | Disables the cross-check entirely. |

A manifest older than 24h triggers a stderr staleness warning.

### CI mode

```
jigsaw check --format=github
```

emits GitHub Actions `::error::` / `::warning::` annotations so PRs that
touch configs get inline diagnostics on the affected files.

## `jigsaw fmt`

Rewrites every `*.yml` / `*.yaml` under `tasks/`, `flows/`, `providers/`,
and `endpoints/` in canonical style:

- 2-space indent
- LF line endings
- Comments and key order preserved (yaml.v3 Node round-trip)

`--check` reports what would change without writing and exits non-zero if
anything would be modified — wire this into CI to enforce style.

Known limitation: blank lines between sequence items are not preserved
(yaml.v3 collapses them). This is accepted as part of the canonical style
for now.

## Wiring `--dump-symbols` into your own binary

This is the most common ask: "how do I make the dashboard know about my
registered handlers?" The standalone `jigsaw` CLI cannot import your
code, so the manifest has to be written by *your binary*. Add a flag
and a one-liner. The dashboard then reads the manifest from disk.

### Mental model

```
[your binary] --dump-symbols  →  writes  <configPath>/.jigsaw/symbols.json
                                            ↓ reads
[jigsaw dashboard]            uses the manifest for validation
```

### Recipe — three changes to your `main()`

**1. Add two imports:**

```go
import (
    "flag"                                       // ← new
    "github.com/amkarkhi/jigsaw/pkg/symbols"     // ← new
    // … your existing imports
)
```

**2. Parse the flag at the top of `main()`:**

```go
func main() {
    dumpSymbols := flag.Bool("dump-symbols", false, "Write symbols manifest and exit")
    flag.Parse()
    // … the rest of your existing setup
```

**3. Right after you finish registering handlers, exit early in dump mode:**

```go
eng := engine.New(cfg, val, log)
mypkg.RegisterAllHandlers(eng)   // your existing call

if *dumpSymbols {
    if err := symbols.DumpToFile(eng, cfg, configPath, "myapp"); err != nil {
        log.Error("dump-symbols failed", err, nil)
        os.Exit(1)
    }
    fmt.Println("wrote:", filepath.Join(configPath, symbols.DefaultManifestPath))
    return
}

// existing provider init, server start, etc. below — only run in normal mode.
```

That's the whole change. The fourth argument to `DumpToFile` (`"myapp"`)
is just a label that lands in the manifest's `binary` field, so you can
tell which consumer produced it.

### If you'd rather not use `flag`

An env-var check works equally well and keeps you in your existing
style:

```go
if os.Getenv("MYAPP_DUMP_SYMBOLS") != "" {
    if err := symbols.DumpToFile(eng, cfg, configPath, "myapp"); err != nil {
        log.Fatal(err)
    }
    return
}
```

Then run `MYAPP_DUMP_SYMBOLS=1 ./myapp` to dump.

### Usage

```bash
# 1. Dump the manifest
./myapp --dump-symbols --config ./configs
# → wrote: configs/.jigsaw/symbols.json

# 2. Verify what landed
jq '.logic[].name' configs/.jigsaw/symbols.json
# → "handler_one"
# → "handler_two"

# 3. Start the dashboard against the same config dir
jigsaw dashboard --config ./configs --edit
# Open http://127.0.0.1:3300 — the Tasks page, graph editor, and
# diagnostics now know which handlers exist.
```

### Recommended for CI

Combine with `jigsaw check` so PRs that touch configs get inline
GitHub Actions annotations and the cross-check against your real
handler set:

```bash
./myapp --dump-symbols --config ./configs
jigsaw check --config ./configs --format=github
```

### Schemas (optional, high-value upgrade)

If you also want input/output type checking, register handlers with
schemas:

```go
import "github.com/amkarkhi/jigsaw/pkg/engine"
import "github.com/amkarkhi/jigsaw/pkg/types"

eng.MustRegisterLogic("search.byID", searchByID,
    engine.WithDescription("lookup by primary key"),
    engine.WithInputSchema(
        types.FieldDef{Name: "id", Type: "string", Required: true},
    ),
    engine.WithOutputSchema(
        types.FieldDef{Name: "result", Type: "object"},
    ),
)
```

After re-dumping, the dashboard validates that every task using
`search.byID` declares matching `inputs:` / `outputs:` and flags any
mismatch as an error.

### Skip the manifest entirely (embedded mode)

If you'd rather have the dashboard run inside your binary with no
manifest step, embed it directly:

```go
import "github.com/amkarkhi/jigsaw/pkg/dashboard"

d, _ := dashboard.New(dashboard.Options{
    ConfigPath: configPath,
    Listen:     "127.0.0.1:3300",
    Edit:       true,
    Logger:     log,
})
go d.ListenAndServe(ctx)
```

The dashboard now reads the live engine registry directly — no manifest,
never stale.

## `jigsaw dump-symbols`

Writes a JSON manifest of the symbols known to *the binary that ran this
command*. With the stock `jigsaw` binary that means providers from the
config plus an empty logic list (no user code is linked in).

The interesting case is a **consumer binary** that embeds Jigsaw — it
should call `symbols.BuildFromEngine` + `symbols.Write` directly from its
own `main()`, typically behind its own `--dump-symbols` flag:

```go
// in the consumer's main.go
if *dumpSymbols {
    m := symbols.BuildFromEngine(eng, cfg, "myapp")
    if err := symbols.Write(filepath.Join(configPath, symbols.DefaultManifestPath), m); err != nil {
        log.Fatal(err)
    }
    return
}
```

The resulting manifest lets `jigsaw check` (run later, by a developer or
in CI) cross-reference task `logic:` declarations against what the
consumer actually registered.

### Handler schemas (Phase 3)

When a handler declares input/output schemas, `jigsaw check` (and the LSP,
and the future dashboard) cross-reference every task that uses the handler
against the declared shape.

In the consumer's `main.go`:

```go
import (
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/types"
)

eng.MustRegisterLogic("search.byID", searchByID,
    engine.WithDescription("look up a record by primary key"),
    engine.WithVersion("1.2.0"),
    engine.WithInputSchema(
        types.FieldDef{Name: "id", Type: "string", Required: true},
        types.FieldDef{Name: "filter", Type: "string"},
    ),
    engine.WithOutputSchema(
        types.FieldDef{Name: "result", Type: "object"},
    ),
)
```

Diagnostics emitted from this layer:

| When | Severity | Message shape |
|------|----------|---------------|
| Task is missing a required schema field | error | `task "X" is missing required input "Y" (declared by logic handler)` |
| Task field type ≠ schema type | error | `task "X" input "Y" has type "int" but logic handler declares "string"` |
| Task declares a field the schema doesn't | warning | `task "X" declares input "Y" which is not in the logic handler's schema` |

Schemas are opt-in per handler. Handlers without schemas are still
cross-referenced for *existence* (the Phase 2 check), just not shape.

## `jigsaw lsp` (Phase 4)

A minimal Language Server Protocol implementation on stdin/stdout. The
server runs `configlang.Check` over the workspace on every open / change /
save and publishes diagnostics back to the editor.

### What it does today

- Opens/changes/saves trigger a workspace re-check.
- Diagnostics from the validator and from handler-schema cross-checks are
  published to every open document.
- Reads the symbols manifest at `<workspace>/.jigsaw/symbols.json` when
  present; degrades to existence-only checks otherwise.

### Known limitations (deferred)

- **File-precise line/column locations are not yet attached.** Diagnostics
  pin to line 0; the message names the offending resource. Per-file
  attribution lands with the dashboard's provenance tracking.
- **No hover / completion / code-actions.** Hover is the obvious next add
  (show resource definition on identifier hover).
- **No incremental sync.** The server requests full document sync; the
  workspace is re-checked on every edit. Fine for config trees (small),
  will need rework if Jigsaw ever has thousands of files.
- **Buffered edits aren't overlaid onto the on-disk view.** Save first
  to see updated diagnostics. Overlay is a Phase 5 deliverable.

### Editor wiring

**neovim** (nvim-lspconfig):

```lua
local lspconfig = require("lspconfig")
local configs = require("lspconfig.configs")

if not configs.jigsaw then
  configs.jigsaw = {
    default_config = {
      cmd = { "jigsaw", "lsp" },
      filetypes = { "yaml" },
      root_dir = lspconfig.util.root_pattern("flows", "tasks", "providers", "endpoints"),
      single_file_support = false,
    },
  }
end
lspconfig.jigsaw.setup({})
```

**VS Code**: a thin client extension (forthcoming) launches `jigsaw lsp`
for files under a Jigsaw config root. For now, any generic LSP-client
extension can be pointed at the binary.

**Helix** (`.helix/languages.toml`):

```toml
[language-server.jigsaw]
command = "jigsaw"
args = ["lsp"]

[[language]]
name = "yaml"
language-servers = ["jigsaw"]
```

## `jigsaw dashboard` (Phase 5)

A single‑page web app for browsing the configuration tree. Phase 5 ships
the read‑only surface; Phase 6 will add the graph editor and save
pipeline.

```
jigsaw dashboard --config ./configs --listen 127.0.0.1:3300
```

Open `http://127.0.0.1:3300` in a browser. Pages: Overview, Flows, Tasks,
Providers, Endpoints, Logic registry, Diagnostics.

### Modes

| Mode | Trigger | Auth | Writes |
|------|---------|------|--------|
| local (default) | `jigsaw dashboard` | bypassed | (Phase 6) in‑place files |
| server | `jigsaw.ServeConfigManager(...)` from a consumer binary | bearer token required | (Phase 6) tar bundle download |

### Server mode with the auth file (recommended)

For production, use the auth file — real users with bcrypted passwords
and hashed bearer tokens, all manageable from the CLI.

```bash
# 1. Choose a master key and keep it in the prod .env.
#    The master key gates user/token mutations; rotate by re-running init.
export JIGSAW_MASTER_KEY="$(openssl rand -hex 24)"

# 2. Initialize the auth file (once).
jigsaw user init --config ./configs

# 3. Create users. Role is admin (edit) or viewer (read-only).
jigsaw user create --config ./configs --username alice --role admin
jigsaw user create --config ./configs --username bob   --role viewer
jigsaw user list   --config ./configs

# 4. Optional: create bearer tokens for CI / scripts.
jigsaw token create --config ./configs --name ci-bot --role viewer
# (token shown once — copy now)

jigsaw token list   --config ./configs
jigsaw token revoke --config ./configs --name ci-bot

# 5. Start the dashboard. It auto-detects the auth file.
jigsaw dashboard --config ./configs --mode=server --edit \
  --listen 0.0.0.0:3300 --allow-remote \
  --service search-flow
```

What the user gets:

- A login page (username + password) when they hit the dashboard
  unauthenticated. The session is a HTTP-only cookie, valid 12 hours.
- The current user + role appear in the sidebar with a logout (⎋) button.
- Bearer tokens still work for API/CI access:
  `curl -H "Authorization: Bearer $TOKEN" http://.../api/overview`.
- Mutating endpoints (POST /api/files, /api/bundle, /api/layout)
  require `admin`; viewers get 403.

The file lives at `<configPath>/.jigsaw/auth.json`. It contains the
master-key fingerprint (SHA-256), bcrypt password hashes, and SHA-256
token hashes — no recoverable secrets. Permissions are `0600`.

### CLI server mode without the auth file (legacy, for quick tests)

If you just want to wave a bearer token at the dashboard without
initializing users:

```bash
jigsaw dashboard --mode=server --edit \
  --listen 0.0.0.0:3300 --allow-remote \
  --admin-token=$JIGSAW_ADMIN_TOKEN \
  --viewer-token=$JIGSAW_VIEWER_TOKEN
```

Tokens can also come from the comma-separated env vars
`JIGSAW_ADMIN_TOKENS` / `JIGSAW_VIEWER_TOKENS`. There's no login page
in this mode — every request must carry the `Authorization: Bearer`
header.

For SSO or anything custom, embed the package and use
`dashboard.CustomAuth`.

### Backend API

All endpoints return JSON unless noted. The frontend talks to these; you
can also use them directly (`curl http://localhost:3300/api/overview`).

Read-only:

| GET path | What it returns |
|----------|-----------------|
| `/api/info` | server name, mode, edit flag, config path |
| `/api/overview` | counts + `manifest_loaded` flag |
| `/api/flows` / `/api/flows?name=X` | flow list / single flow |
| `/api/tasks` / `/api/tasks?name=X` | task list / single task |
| `/api/providers` | provider list |
| `/api/endpoints` | endpoint list |
| `/api/logic` | manifest‑backed handler registry (empty when no manifest) |
| `/api/diagnostics` | diagnostics array (same as `jigsaw check`) |
| `/api/tree` | sorted list of every config file's relative path |
| `/api/file?path=X` | raw text contents of one file |

Editing (require `--edit`; viewer tokens get 403):

| Method + path | What it does |
|---------------|--------------|
| `POST /api/files` | Local-mode save. Body `{files:{path:contents}}` — files round-trip through the parser+formatter, the full overlay is validated, and on success files are written atomically. Returns `{ok, written, diagnostics}`. On validation failure: 422 with diagnostics, nothing written. |
| `POST /api/bundle` | Server-mode save. Same body; on success streams the full validated overlay as `tar.gz`. Caller extracts over their repo and commits. |

The save pipeline applies the canonical formatter, so files written by
the editor are always parseable and consistently styled. Validation
covers everything `jigsaw check` covers, including handler-schema
cross-checks when a manifest is loaded.

### Embedding from a consumer binary

Three auth paths are supported. Pick whichever matches your setup.

#### Option A — auth file with login page (recommended for prod)

The same auth file `jigsaw user`/`jigsaw token` manages. Users get a login
dialog in the browser; CI/scripts use bearer tokens against the same file.

```go
import "github.com/amkarkhi/jigsaw/pkg/dashboard"

fa := dashboard.NewFileAuth(configPath)
if err := fa.EnsureInitialized(); err != nil {
    log.Fatalf("auth not initialized — run `jigsaw user init`: %v", err)
}

d, err := dashboard.New(dashboard.Options{
    ConfigPath:  configPath,
    Mode:        dashboard.ModeServer,
    Listen:      "0.0.0.0:3300",
    AllowRemote: true,
    Edit:        true,
    Auth:        fa,
    ServiceName: "search-flow",     // shown in dashboard footer
    Logger:      log,
})
if err != nil {
    log.Fatal(err) // even on error you still get a valid degraded
                   // dashboard that 503s; calling d.Handler() never NPEs.
}
go d.ListenAndServe(ctx)
```

The CLI verbs to seed it (run once per environment, master key in your
.env):

```bash
export JIGSAW_MASTER_KEY=$(openssl rand -hex 24)
jigsaw user init    --config ./configs
jigsaw user create  --config ./configs --username alice --role admin
jigsaw token create --config ./configs --name ci-bot   --role viewer
```

#### Option B — static bearer tokens (good for tests/CI)

No login page. Every request must carry `Authorization: Bearer <token>`.

```go
d, err := dashboard.New(dashboard.Options{
    ConfigPath:  configPath,
    Mode:        dashboard.ModeServer,
    Listen:      "0.0.0.0:3300",
    AllowRemote: true,
    Edit:        true,
    Auth: dashboard.BearerTokens(map[string]dashboard.TokenInfo{
        os.Getenv("JIGSAW_ADMIN_TOKEN"):  {Label: "ci",  Role: dashboard.RoleAdmin},
        os.Getenv("JIGSAW_VIEWER_TOKEN"): {Label: "ops", Role: dashboard.RoleViewer},
    }),
    ServiceName: "search-flow",
    Logger:      log,
})
```

#### Option C — your existing middleware (SSO, mTLS, signed cookies)

Plug in an `AuthProvider` of your own. `Identity` you return from
`Authenticate(r)` is treated as authoritative.

```go
auth := dashboard.CustomAuth(func(r *http.Request) (dashboard.Identity, error) {
    user, err := mysso.IdentityFromRequest(r) // your existing SSO check
    if err != nil {
        return dashboard.Identity{}, err
    }
    role := dashboard.RoleViewer
    if user.IsAdmin {
        role = dashboard.RoleAdmin
    }
    return dashboard.Identity{Label: user.Username, Role: role}, nil
})

d, _ := dashboard.New(dashboard.Options{
    ConfigPath: configPath,
    Mode:       dashboard.ModeServer,
    Auth:       auth,
    Edit:       true,
    Logger:     log,
})
```

#### Mount under your own router

In every option above, you can either let the dashboard open its own
port (`d.ListenAndServe(ctx)`) or mount it as an `http.Handler` so your
middleware (TLS, rate limit, IP allowlist) fronts it:

```go
http.Handle("/jigsaw/", http.StripPrefix("/jigsaw", d.Handler()))
```

#### `ServiceName` option

Optional field on `Options`. When set, the dashboard sidebar footer shows
this string instead of the (often long, often secret) config path.
Useful when you don't want operators reading the absolute mount path
over a shoulder. If unset, the dashboard falls back to a short derived
name from the config path's parent directory.

#### Failure modes you can rely on

- `New()` with invalid options (e.g. `ModeServer` + no `Auth`) returns
  an error **and** a non-nil `*Dashboard` whose handler responds with
  503 to every request. So a consumer who ignores the error never gets
  a nil-pointer panic.
- `EnsureInitialized()` on a `FileAuth` checks the auth file exists and
  has at least one user or token. Fail fast on this in your
  `main` — if you skip it, the dashboard still works, but no human can
  log in (no users) and bearer-token requests still go through.

### Auth

Bearer tokens via `Authorization: Bearer <token>`. Two roles: `RoleAdmin`
(read + write) and `RoleViewer` (read only). Viewer tokens that issue a
mutating verb get a 403. For SSO or anything more elaborate, use
`dashboard.CustomAuth(fn)` and plug your own logic in.

Failed auth attempts are logged via the consumer's `types.Logger`.

### Graph editor

Each flow is editable as a DAG at `/flows/:name`. The page expects the
file to live at `flows/<name>.yml` with the flow as a single entry in the
`flows:` list (the repo's existing convention). If your layout differs,
use the raw YAML editor.

**Editing model.** Free placement, manual edges. You add task nodes
wherever, then draw edges between them by dragging from a node's bottom
port (●) to another node's top port (●). Edges define execution order.
There's no implicit "next task in the list" — only what you connect.

| Action | How |
|--------|-----|
| Add a task | Click **+ Task** in the toolbar, pick from the palette. The node drops on the canvas. |
| Connect tasks | Drag from a bottom port to another node's top port. |
| Select | Click a node or an edge. Selected edges turn red, selected nodes get the accent border. |
| Delete | Select something, hit Backspace/Delete (or the **Delete** button). |
| Move | Drag nodes anywhere. Positions persist in `flow.metadata.layout`. |
| Auto‑arrange | **Auto‑arrange** rebuilds the layout by topological depth. |
| Parallel branches | Just draw the edges: connect one node to two successors, and reconverge them on a common node downstream. The compiler will emit a `parallel:` block automatically. |
| Undo / redo | Cmd/Ctrl‑Z, Cmd/Ctrl‑Shift‑Z (or Cmd/Ctrl‑Y). |
| Show / hide YAML | **Show YAML** toggles a Monaco editor that mirrors the graph. Edit YAML there, click **Apply** to push back into the graph. |
| Save | **Save** runs the compile pipeline and writes the file (or downloads a bundle in server mode). |

**The compile pipeline** (graph → YAML, run on Save) enforces a few
structural rules and refuses to save with a clear message if the graph
violates them:

- Graph must be **acyclic**.
- Exactly **one entry node** (a node with no incoming edges).
- Every node must be **reachable from the entry**.
- Every **fork** (node with multiple outgoing edges) must have a matching
  **join** further downstream — i.e. all branches eventually converge on
  the same node. Branches that never reconverge can't be expressed as a
  Jigsaw `parallel:` block.

Once those pass, the compiler turns the DAG into a sequence of
`TaskRef`s plus `parallel:` blocks. Comments in the YAML are lost on a
graph save (js‑yaml doesn't preserve them); use the raw YAML editor for
files that are heavily commented.

**Multiple flows per file are supported.** Flow files don't have to be
named after their flow — a file like `flows/searches.yml` can contain
`flows: [{name: basic}, {name: advanced}]`. The graph editor looks up
the right file via `/api/flow-location?name=X`, and saves replace only
the specific flow entry (other flows in the same file are preserved).

**Node positions are sidecar-stored.** Drags don't churn your flow YAML.
Positions live in `.jigsaw/layouts/<flow>.json` (one file per flow,
ignored by the engine). Add `.jigsaw/` to `.gitignore` if you don't want
them committed; commit them if you do want the layout to follow the repo.

**Visual aids and context menus**:

- The source node (no incoming edges) is tagged **▶ start**; sinks (no
  outgoing edges) are tagged **■ end**. These help spot disconnected or
  multi-rooted graphs at a glance.
- Right-click a node to delete it. Right-click an edge to delete it.
  Right-click empty canvas to add a task at that position or drop a
  2-step boilerplate. Esc closes any open menu.

**Inspector**: clicking a node opens the right-side inspector with two
sections:

- **Task parameters** — fields from the underlying Task definition
  (label, description, version, timeout, retry, logic, provider,
  inherits) shown as editable inputs. Hit **Save task** to write the
  changes through to `tasks/<file>.yml`. This is a separate save from
  the flow Save: task edits affect every flow that uses the task and
  are committed independently. Inputs/outputs aren't editable here yet
  — open the raw editor for those.
- **Overrides** — the per-flow TaskRef override list (`skip` /
  `replace` based on `condition`). These travel with the flow's unsaved
  state and write on flow **Save**.

**Live validation.** The graph runs structural validation on every edit
and shows issues above the canvas. Save is disabled while any error is
present. Detected:

| Severity | Detection |
|----------|-----------|
| error | Multiple start nodes (more than one task with no incoming edges). |
| error | Multiple end nodes (more than one task with no outgoing edges). |
| error | No start / no end (the graph is cyclic in some way). |
| error | Cycles. |
| error | Unreachable nodes (orphan nodes not connected to the entry). |
| error | A node references a task that doesn't exist under `tasks/`. |
| warning | A node references a logic handler that's not in the manifest. |
| warning | The same task is placed multiple times in one flow. |

Problem nodes get a red outline; warning nodes get an orange outline.
Save is greyed out until all errors are cleared.

**Unsaved-changes guard.** Navigating away from a flow with unsaved
changes — via the sidebar, browser back, or even refresh/close — prompts
to confirm before discarding. Use **Save** to keep changes, or confirm
the dialog to lose them.

**Round‑tripping existing parallel blocks.** When a flow already
contains `parallel:` blocks, the decompiler inserts visible `·fork` /
`·join` virtual nodes so the structure is unambiguous in the graph.
Those nodes get suppressed when the file is saved again, so the YAML
round‑trips cleanly.

### Frontend

Source: `web/` (Vite + React + TypeScript). Build: `make web` (runs
`npm install && vite build`). Output lands in `pkg/dashboard/dist/`
and is embedded into the Go binary at build time via `go:embed`.

Developing the frontend with hot reload:

```
cd web
npm run dev   # http://localhost:5173, proxies /api → localhost:3300
```

The frontend is intentionally kept separable: it only talks to the Go
side through the typed `src/api/client.ts` module. If embed size becomes
a problem later, the frontend can be lifted into its own repo and served
from a CDN with no changes to the Go side.

## Manifest format

`<config>/.jigsaw/symbols.json`:

```json
{
  "version": "1",
  "generated_at": "2026-05-13T13:00:00Z",
  "binary": "myapp",
  "logic": [
    {
      "name": "search.byID",
      "description": "lookup by primary key",
      "version": "1.2.0",
      "input_schema": [
        { "name": "id", "type": "string", "required": true }
      ],
      "output_schema": [
        { "name": "result", "type": "object" }
      ]
    }
  ],
  "providers": [
    { "name": "mysql", "type": "sql" }
  ]
}
```

Stability rules:

- `version` is mandatory. Readers reject any value they don't recognize.
- Adding new optional fields is a minor change; readers ignore unknown
  fields.
- Renaming or removing fields requires bumping `version`.

## Go API

Two packages back the CLI; both are stable enough for consumers to use.

### `pkg/configlang`

```go
file, err := configlang.LoadFile("configs/tasks/cache.yml") // comment-preserving AST
out, err := configlang.Format(file)                          // canonical bytes

diags := configlang.Check(cfg, configlang.CheckOptions{
    LogicRegistry:    []string{"search.byID", "cache.get"},
    RegistryProvided: true,
})
```

`Check` is the single source of truth for diagnostics; the dashboard and
LSP will call the same function with the same options.

### `pkg/symbols`

```go
m := symbols.BuildFromEngine(eng, cfg, "myapp")
_ = symbols.Write(path, m)

// later, in any tool:
m, err := symbols.Read(path)        // (nil, nil) if the file does not exist
names := m.LogicNames()             // []string of registered handlers
age   := m.Age()                    // time.Duration since GeneratedAt
```

`Read` returning `(nil, nil)` for a missing file is intentional: callers
treat absence as "no signal" rather than a hard error.

## Recommended workflow

1. **In the consumer binary**: add a `--dump-symbols` flag that calls
   `symbols.BuildFromEngine` + `symbols.Write`.
2. **In CI**: run `jigsaw check --format=github` against the config tree.
   If the consumer binary builds in CI, run `--dump-symbols` first so the
   logic cross-check has signal.
3. **As a developer**: run `jigsaw fmt --check` and `jigsaw check` before
   sending a PR. Both are fast (no engine startup) and exit non-zero on
   failure, so they're easy to wire into a pre-commit hook.
