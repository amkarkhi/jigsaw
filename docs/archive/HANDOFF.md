# Handoff — Task Wrappers + Engine.InvokeTask

Snapshot of changes on `feat/config_manager_web`. The earlier J1/J2a/J3 work
(logic dispatch via `Engine.Invoke`, request_params validation, schemaless
bind.out support) is in. This update adds **task-level wrappers**: a
declarative way for one task to intercept another's execution.

## Mental model

```
flow ──refs──▶ TaskRef ──resolves──▶ Task ──wrapper?──▶ Wrapper Task
                                                            │
                                              ctx.Nested = wrapped task
                                              logic runs, may call
                                              ctx.Engine.InvokeTask(...)
                                                            │
                                                  wrapped task executes
                                                  (with leaf's bind & I/O)
```

- The flow author writes `- name: search` with its normal `bind:` — no
  decoration.
- The task author writes `wrapper: { task: cache }` on `search`.
- The engine sees the wrapper, runs the `cache` task instead with
  `ctx.Nested.Task = "search"`, gathers inputs via `search`'s schema, and
  publishes outputs via the ref's `bind.out`.
- Cache logic decides what to do: build a key, check, return cached or
  dispatch the wrapped task via `ctx.Engine.InvokeTask(ctx, ctx.Nested.Task,
  in, nil)`.

## What landed

### Engine surface

| Change | Where | Why |
|---|---|---|
| `Task.Wrapper *WrapperRef` | `pkg/types/types.go` | Declarative interception. `{ task, params? }`. Params merge on top of the wrapper task's params for this binding. |
| `Engine.InvokeTask(ctx, name, inputs, paramOverrides)` | `pkg/engine/engine.go` | Dispatches a task by name (inheritance + params + provider applied) with no bind. Reuses the JSON round-trip, so payloads match live runs. Clears `ctx.Nested` while the inner runs and restores on return. |
| `LogicDispatcher.InvokeTask` | `pkg/types/types.go` | Interface contract surfaced on `ctx.Engine`. |
| `ExecutionContext.Nested *WrapperRef` | `pkg/types/types.go` | Set by the executor before running a wrapper; restored on return. Points at the wrapped task (`{Task, Params}` — params are the wrapped task's defaults, in case the wrapper wants to forward them). |
| `executeWithWrapper` | `pkg/engine/task_executor.go` | When `Task.Wrapper` is set, the executor runs the wrapper instead of the original task. Inputs come from the original task's schema + the ref's bind. Outputs publish via the ref's bind too. Param precedence (low→high): wrapper task params → original task params → `wrapper.params` (per-binding) → `ref.Params`. |
| Wrapper validation (exists + cycles) | `pkg/validator/validator.go` (`validateWrappers`) | Rejects unknown wrapper task names and detects cycles like `A.wrapper = B, B.wrapper = A`. Errors include the path so you can untangle them. |
| Config loader picks up `wrapper:` | `pkg/config/loader.go` | `rawTask.Wrapper` plus mapping in `toTask`. Without this the YAML field was silently dropped. |

Tests: `pkg/engine/wrapper_test.go`, `pkg/engine/invoke_test.go`,
`pkg/engine/flow_validator_test.go`. `go test ./...` and `go vet ./...` clean.

### UI

| Change | Where |
|---|---|
| Tasks API exposes `wrapped_by` (was `wraps_logic`) | `pkg/dashboard/api.go` |
| Frontend type: `TaskSummary.wrapped_by?` | `web/src/api/client.ts` |
| `NodeData.wrappedBy?` + per-task lookup table | `web/src/routes/FlowGraph.tsx` |
| Chip relabelled `↻ wrapped`, container arrow `↑` pointing at wrapper name | `web/src/routes/FlowGraph.tsx` |
| Removed dead `refParams` / `collectRefParams` plumbing (was for the abandoned per-flow `nested_tasks` direction) | `web/src/routes/FlowGraph.tsx` |

`vite build` clean; bundle re-emitted at `pkg/dashboard/dist/`.

### Example app

| Change | Where |
|---|---|
| `SearchLogic` + `CacheWrapperLogic` (generic, reads `ctx.Nested.Task`) + `memCache`/`CacheBackend` retained | `examples/jig-test/main.go` |
| `cache` task (generic) + `search` task with `wrapper: { task: cache, params: {...} }` | `examples/jig-test/configs/tasks/common.yml` |
| `nested_search` flow — plain `- name: search` ref, no wrapper config | `examples/jig-test/configs/flows/nested_search.yml` |
| `search` endpoint maps `sub=2 → nested_search`, declares `request_params: [query]` | `examples/jig-test/configs/endpoints/search.yml` |
| Removed: old `cached_call` logic + `parse_params_cached` task + `cached_search` flow + duplicate `search_with_wrapper`/`cache_wrapper.yml` task files | (deleted) |

Verify:
```sh
cd examples/jig-test && go run .
# in another shell:
curl "http://localhost:8080/api/search?query=test&sub=2"   # MISS — cache_wrapper runs search
curl "http://localhost:8080/api/search?query=test&sub=2"   # HIT  — cache_wrapper returns cached
```

Logs include `cache_wrapper MISS` then `cache_wrapper HIT`. The `search:
running` log appears only on the MISS, confirming the inner is bypassed on
cache hits.

## Conventions

**Wrapper task.** A task that intercepts another task's execution. It is
declared from the *wrapped* side: `search.wrapper = { task: cache }` reads
as "search is wrapped by cache." The wrapper's Go type uses `map[string]any`
for I/O so values pass through unchanged; the wrapped task's schema is what
gates input validation.

**Per-binding params on the wrapper.** `wrapper.params` apply only when the
wrapper is invoked *from this binding* — a different task wrapped by the
same wrapper can pass different params. Precedence: wrapper task defaults →
wrapped task params → `wrapper.params` → flow ref's `params` override.

**Cache-key construction.** `cache_wrapper` reads `params.keys: [name, ...]`
and folds those fields out of the (schemaless) input map into a stable key.
The example also prefixes with `ctx.Nested.Task` so two wrapped tasks don't
collide.

**Calling InvokeTask from a wrapper.** Always:

```go
raw, err := ctx.Engine.InvokeTask(ctx, ctx.Nested.Task, in, nil)
```

`ctx.Nested` is cleared while the inner runs (so the inner doesn't see its
wrapper's nested handle) and restored on return.

## Gateway follow-ups

1. **Drop the validator warning-downgrade.** `eng.ValidateFlows()` should be
   allowed to abort startup again. Remove the `_ = eng.ValidateFlows()` shim
   in `internal/app/app.go`.
2. **Declare `request_params` on each endpoint.** Same as before — the
   validator will now flag missing keys.
3. **Implement a real `CacheBackend` provider.** Return a Redis client (or
   compatible) from `GetConnection()`; `CacheWrapperLogic` will pick it up
   automatically.
4. **Migrate caching surface.** Replace per-handler cache duplication in
   normalize / qu / buildquery / facetrerank with a single `cache` wrapper
   task referenced via `wrapper:` on each target task. Pull the pattern from
   `examples/jig-test/configs/tasks/common.yml`.

## Open / deliberately deferred

- **Per-flow wrapper override.** The wrapper is task-scoped; every flow
  using `search` gets caching. No escape hatch. If a flow needs an uncached
  variant, define a second task. Add `TaskRef.NoWrapper` only when a real
  use case forces it.
- **Chained wrappers via plural list.** Recursive composition works today
  (`search.wrapper = cache`, `cache.wrapper = retry`) — cycles are rejected
  and order is determined by the chain. A flat `wrappers: [...]` list was
  considered and dropped: recursion reads more naturally and the validator
  already catches cycles.
- **Cache-key stability across processes.** `json.Marshal` over a map sorts
  keys, but nested `[]any` / mixed types can drift. For cross-process hit
  rate, normalize before hashing (sort slices, drop volatile fields).

## File map

```
pkg/types/types.go                              (+ Task.Wrapper, WrapperRef, ctx.Nested, LogicDispatcher.InvokeTask)
pkg/engine/engine.go                            (+ InvokeTask)
pkg/engine/task_executor.go                     (+ executeWithWrapper, param merge)
pkg/engine/wrapper_test.go                      (NEW — wrapper happy path)
pkg/validator/validator.go                      (+ validateWrappers: exists + cycle)
pkg/config/loader.go                            (rawTask.Wrapper plumbing)
pkg/dashboard/api.go                            (response shape: wrapped_by)
web/src/api/client.ts                           (TaskSummary.wrapped_by)
web/src/routes/FlowGraph.tsx                    (NodeData.wrappedBy + render; refParams removed)
examples/jig-test/main.go                       (SearchLogic, CacheWrapperLogic, CacheBackend/memCache; CachedCallLogic removed)
examples/jig-test/configs/tasks/common.yml      (cache + search w/ wrapper)
examples/jig-test/configs/flows/nested_search.yml  (NEW — plain search ref)
examples/jig-test/configs/endpoints/search.yml     (sub=2 → nested_search)
```
