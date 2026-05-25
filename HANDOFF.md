# Handoff — J1 / J2a / J3 + cached_call

Snapshot of changes made on `feat/config_manager_web` to unblock the gateway's
JIGSAW_TODO items J1, J2a, J3. Pick up here.

## What landed

### Engine surface

| Change | Where | Why |
|---|---|---|
| `LogicDispatcher` interface + `ExecutionContext.Engine` field | `pkg/types/types.go` | Lets handlers reach the engine from inside `Run` without an import cycle. |
| `Engine.Invoke(ctx, name, inputs, params, provider)` | `pkg/engine/engine.go` | Dispatch a registered logic by name. Reuses the same JSON round-trip the executor uses, so cached payloads are byte-identical to live ones. No implicit fallback handling — caller decides. |
| `execCtx.Engine = e` set in `ExecuteFlow`; propagated by `context.Fork` | `pkg/engine/engine.go`, `pkg/context/context.go` | Back-pointer is available everywhere a task runs, including parallel branches. |
| `Endpoint.RequestParams []string` | `pkg/types/types.go` | Endpoint declares which scope keys the HTTP layer will seed. |
| Validator pre-seeds scope from endpoints | `pkg/engine/flow_validator.go` | Always seeds `sub`, `tag`; unions `request_params` across endpoints that map to each flow. Flows whose endpoints don't declare params validate exactly as before — no forced migration. |
| Validator honors `bind.out` for schemaless handlers | `pkg/engine/flow_validator.go` | Dynamic wrappers (`I`/`O` = `map[string]any`) have no output-schema, so the validator wouldn't otherwise know what they publish. Adding the right-hand sides of `bind.out` to the simulated scope is what makes the wrapper pattern usable in real flows. |

Tests: `pkg/engine/invoke_test.go`, `pkg/engine/flow_validator_test.go`.
`go test ./...` and `go vet ./...` clean.

### UI

| Change | Where | Why |
|---|---|---|
| Endpoint API includes `request_params` | `pkg/dashboard/api.go` (`handleEndpoints`) | Surface declaration to the UI. |
| Task API includes `wraps_logic` when `task.params.inner` is a string | `pkg/dashboard/api.go` (`handleTasks`) | Detection signal for wrapper tasks. |
| `EndpointSummary.request_params`, `TaskSummary.wraps_logic` types | `web/src/api/client.ts` | Frontend types. |
| "seeds: [chip] [chip]" row on endpoint cards | `web/src/routes/Endpoints.tsx` | Only renders when params are declared. |
| Nested-container wrapper node in the flow graph | `web/src/routes/FlowGraph.tsx`, `web/src/styles.css` | Dashed accent-tinted outer box with `↻ wraps` chip and an embedded inner chip naming the inner logic. Reads as "X contains Y." |

`tsc --noEmit` and `vite build` clean.

### Example app

| Change | Where |
|---|---|
| Working `cached_call` wrapper logic + in-memory fallback | `examples/jig-test/main.go` |
| `parse_params_cached` task using `cached_call` | `examples/jig-test/configs/tasks/common.yml` |
| `cached_search` flow using the cached task | `examples/jig-test/configs/flows/cached_search.yml` |
| `search` endpoint maps `sub=2 → cached_search`, declares `request_params: [query]` | `examples/jig-test/configs/endpoints/search.yml` |

Verify:
```sh
cd examples/jig-test && go run .
# in another shell:
curl "http://localhost:8080/api/search?query=test&sub=2"   # MISS
curl "http://localhost:8080/api/search?query=test&sub=2"   # HIT (within 60s)
```

## Conventions

**Wrapper logic.** A task is treated as a wrapper if its YAML has
`params.inner: <some logic name>`. The wrapper's Go type must use
`map[string]any` for its `I` and `O` so the engine's `gatherFromBind` /
`publishOutputs` paths can move arbitrary data through it. The flow author
declares the data shape via `bind.in` / `bind.out` on the task ref.

**Endpoint request_params.** Add `request_params: [name, ...]` to any
endpoint whose first task reads request-supplied scope keys. Once added, the
validator will catch missing keys *and* spurious keys (since flows can only
read what some endpoint declares plus the framework's `sub` / `tag`).

## Gateway follow-ups (from the original JIGSAW_TODO)

1. **Drop the validator warning-downgrade.** With J2a in place, `eng.ValidateFlows()` should be allowed to abort startup again. Remove the `_ = eng.ValidateFlows()` shim in `internal/app/app.go` and the `HANDOFF.md` § "Known issues" entry that referenced it.
2. **Declare `request_params` on each endpoint.** Walk your endpoint YAMLs and list the query/path/header/body keys that the HTTP layer seeds into scope. Anything not declared will surface as a validator error against the first task that reads it.
3. **Implement a real `CacheBackend` provider.** The wrapper in `examples/jig-test/main.go` uses an in-memory store by default. In the gateway, wire a Redis client behind:
   ```go
   type CacheBackend interface {
       Get(ctx context.Context, key string) ([]byte, bool, error)
       Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
   }
   ```
   Return that from your provider's `GetConnection()`. The wrapper will pick it up automatically; no change to the wrapper code.
4. **Migrate the gateway's caching surface.** Replace the per-handler cache duplication in normalize / qu / buildquery / facetrerank with `cached_call` task wrappers. Pull the YAML pattern from `examples/jig-test/configs/flows/cached_search.yml`.

## Open / deliberately deferred

- **J4 (multi-phase orchestrator).** Not implemented. Left in user hands per
  the design discussion — the right shape is an orchestrator *logic* built on
  top of `Engine.Invoke` rather than a new engine-level loop primitive. The
  building blocks (J1/J3) are now in place.
- **Inner-logic providers.** `Engine.Invoke(...)` passes `nil` for the
  provider to the inner. `cached_call` is fine because it caches the
  inner's outputs, not its provider state. Wrappers that need to forward a
  provider should accept a provider name in their params and resolve it via
  `ctx.Providers.Get(name)`.
- **Cache-key stability across processes.** `json.Marshal` over a map sorts
  keys, but nested `[]any` / mixed types can still drift. If you need
  cross-process hit rate, normalize the input before hashing (sort slices,
  drop volatile fields like timestamps).

## File map

```
pkg/types/types.go                            (+ Engine field, + LogicDispatcher, + RequestParams)
pkg/engine/engine.go                          (+ Invoke, sets execCtx.Engine)
pkg/engine/flow_validator.go                  (scope pre-seeding, schemaless bind.out fallback)
pkg/engine/invoke_test.go                     (NEW)
pkg/engine/flow_validator_test.go             (NEW)
pkg/context/context.go                        (Fork propagates Engine)
pkg/dashboard/api.go                          (response shape: request_params, wraps_logic)
web/src/api/client.ts                         (types)
web/src/routes/Endpoints.tsx                  (seeds chip row)
web/src/routes/FlowGraph.tsx                  (NodeData.wrapsLogic, render)
web/src/styles.css                            (wrapper + chip styling)
examples/jig-test/main.go                     (CachedCallLogic, CacheBackend, memCache)
examples/jig-test/configs/tasks/common.yml    (parse_params_cached)
examples/jig-test/configs/flows/cached_search.yml  (NEW)
examples/jig-test/configs/endpoints/search.yml     (sub=2 mapping, request_params)
```
