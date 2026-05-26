# Rewrite-With-Jigsaw — Agent Prompt

This document is a **drop-in prompt** for an AI coding agent (Claude Code,
Cursor, etc.). Paste the whole "Prompt" section below into the agent, then
attach (or point it at) the project you want refactored. The agent will
analyse the project and re-express its request-handling logic as Jigsaw
flows, tasks, providers, endpoints, and Go logic handlers.

Use it for two purposes:

1. **Refactor** — take an existing Go (or other-language) service and port
   its request paths into Jigsaw.
2. **Greenfield** — start a new project that uses Jigsaw from day one.

---

## How to use

1. Copy everything between the `BEGIN PROMPT` / `END PROMPT` markers below.
2. Paste it into your agent.
3. Append, at the end:
   - The project path (or a description of the project).
   - Which endpoints/use-cases you want Jigsaw to own first.
   - Any constraints (Go version, deployment target, providers already in
     use — Redis, Postgres, Kafka…).
4. The agent will return a Jigsaw-shaped scaffold: `configs/` tree + Go
   logic handlers + a `main.go` that wires it together.

---

## BEGIN PROMPT

You are refactoring (or scaffolding) a project to use **Jigsaw**
(`github.com/amkarkhi/jigsaw`), a configuration-driven workflow engine for
Go. Your job is to read the target project, identify request-handling
pipelines, and re-express them as Jigsaw artefacts.

### 1. Jigsaw mental model — learn this first

```
HTTP Request → Endpoint → Flow Router → Flow → Task → Logic + Provider
```

- **Endpoint** — an HTTP route (path + method). Declared in YAML. Selects a
  flow by a `sub` discriminator (an integer in the request body/query).
- **Flow** — an ordered list of tasks. Flows can `inherits:` another flow
  and add/replace steps. Flows can contain `parallel:` blocks with named
  **branches** that join before the next task.
- **Task** — a unit of work declared in YAML. References a **logic** by
  name and (optionally) a **provider**. Tasks declare `timeout`, `retry`,
  `fallback`, `params`. Tasks can `inherits:` another task.
- **Logic** — a Go struct that implements `LogicMeta() engine.LogicMeta`
  and `Run(ctx *types.ExecutionContext, in InputsT, p ParamsT[, prov types.ProviderInstance]) (*OutputsT, error)`.
  **Inputs and outputs are inferred from the struct types — never declare
  `inputs:` / `outputs:` in YAML.**
- **Provider** — an external dependency (Redis, Postgres, HTTP client…)
  with connection config and an init mode (`pooled` / `lazy` / `eager`).
- **Bindings** — `bind.in` maps flow-scope keys → logic input field;
  `bind.out` maps logic output field → flow-scope key;
  `bind.skip` lists input fields to omit from this task ref (the logic
  sees the Go zero value, and required-from-schema is satisfied). A field
  can only be listed in `bind.skip` when the logic's input struct marks
  it `jig:"skippable"`. Inside a parallel block, a branch's outputs are
  published as `<branch_label>.<key>` in the parent scope.
- **Fallback strategies** — `abort`, `continue` (with `defaults:`), or
  `switch_provider` (with `providers: [a, b, c]`).

### 2. Decision algorithm — apply to every request path

For each HTTP handler / RPC / background pipeline in the target project:

1. **Find the seams.** List the discrete steps the handler performs in
   order: parse input → validate → cache lookup → enrichment calls → core
   computation → persistence → response shaping. Each seam becomes a
   **task**.
2. **Promote shared seams.** If the same step appears in multiple
   handlers (input parsing, auth, response shaping), make it a single task
   reused across flows.
3. **Identify independent fan-outs.** Anywhere the original code launches
   goroutines / `errgroup` / waits on multiple I/O calls that don't depend
   on each other → encode as a `parallel:` block with one branch per
   independent path.
4. **Classify each external dependency** (DB, cache, HTTP API, queue) as a
   **provider**. Pick `init_mode: pooled` for connection-pooled clients
   (Redis, SQL), `eager` for must-be-ready-at-boot, `lazy` otherwise.
5. **Map error handling.** For every step the original code recovers
   from, set `fallback: { strategy: continue, defaults: {...} }`. For
   fatal steps use `abort`. For steps with alternates (primary→secondary
   search, primary→replica DB) use `switch_provider`.
6. **Discriminate variants** of an endpoint via `sub`. If the handler has
   "cheap path vs. heavy path", "v1 vs. v2", or "logged-in vs. anonymous"
   branches, model each as a separate flow under the same endpoint with
   different `sub` values.
7. **Naming.** Use `snake_case` for tasks, flows, providers, endpoints,
   and logic. Task names describe the action (`fetch_user`,
   `score_profile`). Flow names describe the use case (`enrich_user`,
   `search_with_cache`).

### 3. Output contract — what you must produce

Produce the following, and **nothing else** in `configs/`:

```
configs/
  endpoints/   # *.yml — one file per logical endpoint group
  flows/      # *.yml — one file per flow or family of related flows
  tasks/      # *.yml — group by domain (auth.yml, cache.yml, search.yml…)
  providers/  # *.yml — one file per provider type or per provider
```

Plus Go code:

```
main.go                 # loader + validator + engine + provider registry + server
internal/logic/*.go    # one file per logic struct (or grouped by domain)
```

Every YAML file must conform to the rules below. If you are unsure about a
field, **omit it** rather than invent it — the validator will reject
unknown fields.

#### 3.1 Task YAML — allowed shape

```yaml
tasks:
  - name: <snake_case>
    description: <string>
    version: <semver>            # optional
    logic: <logic_name>          # MUST match a registered LogicMeta.Name
    provider: <provider_name>    # optional — only if logic uses a provider
    params:                      # optional — passed to logic's params struct
      <key>: <value>
    timeout: <ms>
    retry: <int>                  # optional
    fallback:
      strategy: abort | continue | switch_provider
      message: <string>           # for abort
      defaults: { ... }           # for continue — must match output field names
      providers: [a, b, c]        # for switch_provider
    inherits: <parent_task>       # optional
```

**Do NOT include `inputs:` or `outputs:` — they are derived from Go
structs.**

#### 3.2 Flow YAML

```yaml
flows:
  - name: <snake_case>
    description: <string>
    version: <semver>
    inherits: <parent_flow>       # optional
    tasks:
      - name: <task_name>
        bind:
          in:   { <logic_input_field>: <flow_scope_key> }
          out:  { <logic_output_field>: <flow_scope_key> }
          skip: [<logic_input_field>, ...]   # optional — see §3.5
      - parallel:                  # optional parallel block
          on_branch_failure: continue | cancel
          branches:
            - label: <branch_label>
              tasks:
                - name: <task_name>
                  bind: { in: {...}, out: {...} }
      - name: <task_name>
        bind:
          in:
            <input_field>: <branch_label>.<key>   # consume parallel output
```

#### 3.3 Provider YAML

```yaml
providers:
  - name: <snake_case>
    type: cache | sql | http | kafka | <custom>
    version: <semver>
    config:
      <provider-specific>: <value>
    init_mode: pooled | lazy | eager
    pool_size: <int>             # for pooled
```

#### 3.4 Endpoint YAML

```yaml
endpoints:
  - name: <snake_case>
    path: /api/<path>
    method: GET | POST | PUT | DELETE
    description: <string>
    flows:
      - sub: 1
        flow_name: <flow_name>
      - sub: 2
        flow_name: <other_flow>
```

#### 3.5 Go logic handler — required shape

```go
package logic

import (
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/types"
)

type fetchUserIn struct {
    UserID  string   `json:"user_id"`
    // Mark a field skippable so individual flows may opt out of providing
    // it via bind.skip. Logic will see the Go zero value (here: nil slice).
    Filters []string `json:"filters" jig:"skippable"`
}
type fetchUserOut struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}
type fetchUserParams struct {
    IncludeDeleted bool `json:"include_deleted"`
}

type FetchUserLogic struct{}

func (FetchUserLogic) LogicMeta() engine.LogicMeta {
    return engine.LogicMeta{
        Name:        "fetch_user",   // must match task.logic
        Description: "Fetch a user by ID",
        Version:     "1.0.0",
    }
}

// Add the prov arg ONLY if the task declares a provider.
func (FetchUserLogic) Run(
    ctx *types.ExecutionContext,
    in fetchUserIn,
    p fetchUserParams,
    prov types.ProviderInstance,
) (*fetchUserOut, error) {
    // use prov.GetConnection().(*sql.DB) / .(*redis.Client) etc.
    return &fetchUserOut{Name: "...", Email: "..."}, nil
}
```

Register handlers in `main.go`:

```go
engine.MustRegister(eng, logic.ParseLogic{})
engine.MustRegisterWithProvider(eng, logic.FetchUserLogic{}) // when Run takes prov
```

#### 3.5.1 Skippable inputs — opt-in per field

The logic author decides which inputs may be bypassed by individual flows.
Mark such fields on the input struct with the `jig:"skippable"` struct tag:

```go
type sendEmailIn struct {
    To      string `json:"to"`                        // required, never skippable
    Subject string `json:"subject"`                   // required, never skippable
    CC      []string `json:"cc"      jig:"skippable"` // optional per-flow
    Locale  string   `json:"locale"  jig:"skippable"` // optional per-flow
}
```

Now a flow can declare on a task ref:

```yaml
- name: send_email
  bind:
    in:   { to: target_email, subject: notification_subject }
    skip: [cc, locale]
```

Rules:

- `bind.skip` may only contain names that appear in the logic's input struct
  AND carry `jig:"skippable"`. Anything else is a config error.
- A skipped field is omitted from the input map; the Go logic receives the
  zero value for that field's type. Use this to bypass otherwise-required
  inputs without changing the standard contract.
- `bind.skip` and `bind.in[<field>]` are mutually exclusive for the same
  field — picking one in the UI clears the other.
- The UI surfaces a "skip" checkbox on each input row whose field is
  declared skippable; non-skippable required fields must still be bound.

#### 3.6 main.go — must include in order

1. `config.NewLoader(log).Load("./configs")`
2. `validator.New(log).ValidateConfig(cfg)`
3. `engine.New(cfg, val, log)` + `engine.MustRegister*` for every logic
4. `eng.ValidateFlows()` and `eng.ValidateLogicHandlers()` — fail-fast on errors
5. `provider.NewRegistry(log)` + `RegisterConfig` for every provider + `InitAllEager(ctx)`
6. `server.NewWithEngine(eng, providerReg, cfg, log, opts).Start(port, "./configs")`

Use the canonical example at `examples/jig-test/main.go` in the Jigsaw repo
as the spine — copy its shutdown handling and validation gating.

### 4. Worked micro-example — for grounding only

Input (original):

```go
// GET /api/search?q=...
func handleSearch(w http.ResponseWriter, r *http.Request) {
    q := strings.TrimSpace(r.URL.Query().Get("q"))
    if q == "" { http.Error(w, "q required", 400); return }
    if hit, ok := cache.Get(q); ok { json.NewEncoder(w).Encode(hit); return }
    res, err := search.Run(q)
    if err != nil { http.Error(w, err.Error(), 500); return }
    cache.Set(q, res)
    json.NewEncoder(w).Encode(res)
}
```

Output:

`configs/tasks/search.yml`
```yaml
tasks:
  - name: parse_params
    logic: parse_and_validate_params
    params: { max_length: 100 }
    fallback: { strategy: abort, message: "q required" }
  - name: use_cache
    logic: check_cache
    provider: cache
    fallback: { strategy: continue }
  - name: run_search
    logic: run_search
    provider: search_engine
    fallback: { strategy: switch_provider, providers: [search_engine, search_fallback] }
  - name: save_cache
    logic: save_cache
    provider: cache
    fallback: { strategy: continue }
  - name: response_builder
    logic: build_response
```

`configs/flows/search.yml`
```yaml
flows:
  - name: search_with_cache
    tasks:
      - name: parse_params
        bind: { in: { Q: query }, out: { parsed_query: pq } }
      - name: use_cache
        bind: { in: { parsed_query: pq }, out: { cache_hit: hit, cached_result: cached } }
      - name: run_search
        bind: { in: { parsed_query: pq }, out: { results: results } }
      - name: save_cache
        bind: { in: { parsed_query: pq, results: results } }
      - name: response_builder
        bind: { in: { parsed_query: pq, cache_hit: hit, cached_result: cached, results: results } }
```

`configs/endpoints/search.yml`
```yaml
endpoints:
  - name: search
    path: /api/search
    method: GET
    flows:
      - sub: 1
        flow_name: search_with_cache
```

Logic structs and `main.go` follow §3.5–3.6.

### 5. What you must do, in order

1. **Read** the target project. List every HTTP/RPC/CLI entry point and
   its dependencies.
2. **Propose** a mapping table: `entry point → flow → ordered tasks →
   providers`. Show it to the user before writing files **only if** the
   project has more than one entry point; for a single-flow project,
   proceed directly.
3. **Generate** the `configs/` tree, the `internal/logic/` Go files, and
   `main.go`. Reuse tasks across flows whenever possible.
4. **Self-validate** before finishing:
   - Every `task.logic` has a matching `LogicMeta.Name` in Go.
   - Every `task.provider` is declared in `configs/providers/`.
   - Every `flow.tasks[].name` resolves to a task in `configs/tasks/`.
   - Every `bind.in` field exists on the logic's input struct (same
     `json:` tag). Every `bind.out` field exists on the output struct.
   - Every name in `bind.skip` exists on the input struct AND has the
     `jig:"skippable"` tag. Never list non-skippable fields.
   - No `inputs:` / `outputs:` keys in any YAML.
   - `fallback.defaults` keys match output-struct fields.
5. **Report** what you produced and what you intentionally left out
   (e.g. middleware that doesn't fit the task model, background jobs,
   anything that should stay as plain Go).

### 6. Hard rules — do not violate

- Do **not** declare `inputs:` or `outputs:` in YAML.
- Do **not** invent YAML fields that aren't in §3. The validator rejects
  unknown fields.
- Do **not** put business logic in `main.go`; it goes in logic structs.
- Do **not** use globals for provider clients — always obtain them from
  the `prov types.ProviderInstance` argument.
- Do **not** swallow errors inside `Run` — return them and rely on
  `fallback:` to decide behaviour.
- Do **not** add tests, README files, or commentary files unless the user
  asked for them.

### 7. Reference material in the Jigsaw repo

If you can read the Jigsaw source, prefer these over your own assumptions:

- `examples/jig-test/main.go` — canonical wiring.
- `examples/jig-test/configs/` — canonical config tree.
- `pkg/types/types.go` — authoritative field names for Config/Task/Flow/Provider/Endpoint.
- `pkg/validator/validator.go` — exact validation rules.
- `pkg/engine/flow_executor.go` — flow execution + binding semantics.
- `docs/reference/ARCHITECTURE.md`, `docs/reference/LOGIC_VALIDATION.md`,
  `docs/reference/parallel-execution.md` — design details.

## END PROMPT

---

## Appendix — tips for the human running the agent

- **Start small.** Point the agent at one endpoint or one pipeline first.
  Review the YAML and Go it produces, fix any naming you don't like, then
  let it continue with the rest.
- **Keep providers shared.** Don't let the agent create one provider per
  task — `cache`, `primary_db`, `search_engine` should be reused across
  dozens of tasks.
- **Inheritance is a refactor, not a starting point.** Let the agent
  generate flat flows first; introduce `inherits:` only after duplication
  is visible.
- **`sub` is the variant knob.** When you want A/B or cheap/heavy
  variants of the same URL, ask the agent to add a new flow under the
  same endpoint with a new `sub`, not a new endpoint.
- **Validate locally** with `jigsaw validate --config ./configs` after
  the agent finishes — it catches most field-name and binding mistakes
  in seconds.
