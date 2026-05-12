# Parallel Execution

This document is the design reference for parallel task execution in Jigsaw. It covers the schema, runtime model, data-flow rules, validation, failure semantics, and authoring best practices.

## 1. Goals

- Allow a flow to fan out into N independent **branches**, each of which is itself a sequence of tasks (and may contain further parallel blocks).
- Branches run concurrently; the flow continues only after all branches have completed.
- Outputs of parallel work are exposed to downstream tasks through an explicit, label-based addressing scheme. The engine never silently merges branch outputs.
- A failure in one branch does **not** cancel siblings by default. Authors may opt into cancel-on-failure per parallel block.

## 2. Schema

### 2.1 Task — `label`

```yaml
- name: fetch_users
  label: user_data       # logical role of this task's outputs (optional)
  outputs:
    - name: items
```

`Task.Label` is a **flow-local logical name** for what the task produces. Two tasks with the same role can share a label so downstream consumers stay flow-agnostic.

> Labels are **not global**. They are scoped to the flow they execute in.

### 2.2 Input — `from` and `field`

```yaml
inputs:
  - name: items          # local input name (the variable the task sees)
    from: user_data      # which producer (by label) to read from
    field: items         # which field inside that producer's outputs; defaults to `name`
```

Resolution order:

1. `from` is set → resolve via the **label index** (see §4). `field` defaults to the input's `name`.
2. `from` is not set → fall back to the legacy field-name scan over `TaskOutputs` (backward compatible).

### 2.3 `from` path grammar

```
from := <label>                              # main-flow scope
      | <branchLabel> "." <from>             # nested, recursive (parallel inside parallel)
```

Examples:

- `from: user_data` — main-flow scope, latest producer with that label.
- `from: left.user_data` — branch labeled `left` produced this label.
- `from: outer.inner.user_data` — nested parallel, branch `inner` inside branch `outer`.

### 2.4 Parallel block

```yaml
- parallel:
    on_branch_failure: continue     # "continue" (default) | "cancel"
    branches:
      - label: left
        tasks:
          - name: fetch_profile
          - name: enrich_profile
          - name: score_profile
      - label: right
        tasks:
          - name: fetch_activity
          - name: aggregate_activity
          - name: score_activity
```

A `TaskRef` is either a normal task (`name:` + optional `overrides:`) **or** a parallel block (`parallel:`), never both.

## 3. Runtime model

### 3.1 Execution

The flow executor exposes one recursive routine: `executeTaskList(ctx, tasks, flowExec)`. The top-level flow calls it once; each branch calls it again on its own task list. Nested parallels fall out of recursion.

### 3.2 Concurrency primitive

`executeParallel` uses **`sync.WaitGroup` + `context.WithCancel`**:

- One goroutine per branch.
- All goroutines share a derived `gctx` from `context.WithCancel(execCtx.Context)`.
- `on_branch_failure: cancel` calls `cancel()` on the first hard failure; sibling tasks that respect `ctx.Context` short-circuit.
- `on_branch_failure: continue` lets every branch run to completion regardless.
- After `wg.Wait()`, results are merged back into the parent context serially.

### 3.3 Branch context fork

Before spawning a branch, the parent `ExecutionContext` is forked:

| Field | Treatment |
|---|---|
| `RequestID`, `FlowName`, `FlowVersion`, `Sub`, `Tag`, `Parameters`, `Headers`, `Providers` | shared (read-only or internally thread-safe) |
| `Logger` | wrapped via `logger.With({"branch": <label>})` |
| `Context` | replaced with `gctx` |
| `TaskOutputs`, `Versions`, `Metadata`, `LabelIndex` | **shallow-cloned** snapshot at fork time |
| `CurrentTask`, `LastOutput`, `UpdatedAt` | branch-private |

Branches never write to the parent's maps directly.

### 3.4 Join / merge

After all branches return:

1. Each branch's **new** `TaskOutputs` entries get inserted under `<branchPath>.<taskName>` in the parent. Sibling branches cannot collide because they live under distinct branch paths.
2. Each branch's **new** label entries get appended to the parent's `LabelIndex` under the branch's path (see §4).
3. Each branch's `Versions` map merges into the parent (versions are write-once by `task:` / `provider:` keys; no real collisions expected).
4. Branch-local `TaskExecution` records are appended to `flowExec.Tasks` in **branch-declaration order** (deterministic).
5. `LastOutput` is set to `nil`. The next task must read named outputs via `from`/`field`.
6. `FlowExecution.CurrentStep` stays frozen at the index of the parallel block until join; the block counts as a single step.

## 4. Label index

`LabelIndex` is the structure that backs `from:` resolution.

```go
type LabelIndex map[string][]LabeledProducer  // label -> producers, in order of completion

type LabeledProducer struct {
    TaskName   string
    BranchPath []string       // empty for main-flow producers
    Outputs    map[string]any
}
```

Resolution of `from: P1.P2...Pn.label` for a consumer at branch path `C = [c1, c2, ..., ck]`:

1. The expected producer path is `C + [P1, P2, ..., Pn]` (parallel scopes are nested under the consumer's scope).
2. If `n == 0` (unqualified `from: label`):
   - **Same-scope** sequential producer → return the latest one whose `BranchPath == C`.
   - If multiple sibling branches under `C` produced `label`, that is **ambiguous** — return an error. Author must qualify with a branch path.
3. If `n > 0`: look up producers with `BranchPath == C + [P1..Pn]`. Pick the latest. If none, return "not produced."

"Not produced" causes the consumer's input slot to be absent. Whether that is fatal is decided by the consumer's own input validator (`Required` + `Default`), not by the engine.

### 4.1 Sequential same-label producers

If two non-parallel tasks in the same scope both publish `user_data`, **latest wins** at lookup time. This is the same semantics as variable rebinding in a sequential script.

### 4.2 Reachability validation

A `from: <branchLabel>...` path is only legal if the consumer is **downstream of the join** of that branch. Specifically:

- A task **inside** branch A may not reference `from: B.something` for a sibling branch B (race).
- A task **inside** branch A **may** reference labels produced earlier in branch A or in the enclosing scopes.
- A task **after** the parallel block may reference any `from: <anyBranchLabel>...`.

The validator enforces this statically.

## 5. Failure semantics

### 5.1 `on_branch_failure: continue` (default)

- A task inside a branch fails. If the task has `fallback.strategy: continue`, the branch keeps going (existing behavior).
- Otherwise, the branch stops at that point. Its already-completed tasks' outputs still merge back.
- Other branches keep running.
- The parallel block as a whole is **failed** iff every branch failed and none produced useful output — but the engine does not short-circuit the rest of the flow on that alone. Whether the next task can proceed depends on whether its required `from` lookups resolve. This pushes the policy into the next task, where it belongs.
- All branch errors are aggregated via `errors.Join` into `FlowExecution.Error` for the parallel step.

### 5.2 `on_branch_failure: cancel`

- First hard failure (non-`continue` fallback) calls `cancel()`. All sibling goroutines and any task respecting `ctx.Context` abort.
- The flow as a whole is failed; subsequent steps do not run.

## 6. Validation rules

The validator must reject configs that violate any of the following at load time:

1. A `TaskRef` has **both** `name` and `parallel` set, or neither.
2. A parallel block has zero branches.
3. A branch has zero tasks.
4. A `from:` path references a non-existent label, branch path, or field.
5. A `from:` path crosses into a sibling branch from inside a parallel block (§4.2).
6. Two sibling branches publish the same label **and** a downstream consumer references it unqualified.
7. `on_branch_failure` is set to anything other than `continue` / `cancel`.

Per-task input requirements (`required`, `default`, type) are checked by the task at execution time, **not** the engine.

## 7. Best practices for task authors

These apply specifically to running inside parallel blocks.

### 7.1 Respect `ctx.Context`

Every blocking call inside a `LogicHandler` must accept and respect the `context.Context` from `ExecutionContext.Context`. Without this, `on_branch_failure: cancel` cannot actually stop sibling work.

```go
// Good
req, _ := http.NewRequestWithContext(execCtx.Context, "GET", url, nil)
resp, err := http.DefaultClient.Do(req)

// Good — db driver that supports context
rows, err := db.QueryContext(execCtx.Context, q, args...)

// Bad — ignores cancellation
time.Sleep(5 * time.Second)

// Good — cancellable sleep
select {
case <-time.After(5 * time.Second):
case <-execCtx.Context.Done():
    return nil, execCtx.Context.Err()
}
```

### 7.2 Keep producer tasks reusable

Producer tasks should declare one `label` describing what they produce semantically. Do **not** name a label after the branch you happen to use it in (`user_data`, not `left_user_data`). Branch disambiguation belongs on the consumer's `from:` path.

### 7.3 Don't reach across branches at runtime

A task inside branch A must not depend on data from branch B. Even if the validator catches it, treating branches as truly independent units keeps the model clean and the data-flow legible.

### 7.4 Treat absent inputs explicitly

When a branch may fail (`on_branch_failure: continue`), downstream tasks should mark their `from:`-resolved inputs as `required: false` with a `default`, or handle `nil` in the logic handler. The engine does not synthesize defaults for missing labels.

### 7.5 Use a normalizer task after a fan-out

When two branches do "the same kind of work" (e.g. fetch chunks of a list), put a normalizer task immediately after the parallel block that reads each branch's contribution via `from: <branch>.<label>` and produces a single combined output. The engine has no special-case for this — it's just a regular task.

```yaml
- parallel:
    branches:
      - label: A
        tasks: [fetch_chunk]
      - label: B
        tasks: [fetch_chunk]
- name: merge_chunks
  inputs:
    - name: chunk_a
      from: A.user_data
      field: items
    - name: chunk_b
      from: B.user_data
      field: items
```

### 7.6 Provider safety

Providers are accessed from concurrent branches via the shared registry. The registry is mutex-protected, but provider **instances** must be safe for concurrent use by your `LogicHandler`. Pooled providers (`init_mode: pooled`) are the safe default for parallel workloads. Lazy providers initialize once and are then shared — fine if the underlying connection (HTTP client, DB pool) is concurrency-safe.

## 8. Observability

- Branch logs are emitted with `branch=<label>` (and nested branches add their labels in order).
- `flowExec.Tasks` is ordered: parent tasks in declaration order; branch tasks grouped together in branch-declaration order; nested branches recursively.
- `FlowExecution.CurrentStep` advances only at the parent flow level. The UI should detect a parallel step (the `TaskRef.Parallel != nil`) and render branches as concurrent lanes.

## 9. Non-goals (v1)

- Dynamic fan-out over a runtime list (`for each x in users: do X`).
- Per-branch timeouts distinct from per-task `Timeout`.
- Global concurrency caps. Per-block `max_concurrent` may be added later.
- Streaming partial results from branches.

## 10. Worked example

```yaml
flows:
  - name: enrich_user
    tasks:
      - name: fetch_user
      - name: validate_user
      - parallel:
          on_branch_failure: continue
          branches:
            - label: profile
              tasks:
                - name: fetch_profile
                - name: score_profile      # label: profile_score
            - label: activity
              tasks:
                - name: fetch_activity
                - name: score_activity     # label: activity_score
      - name: combine_scores               # reads from profile.profile_score and activity.activity_score
      - name: persist_result
```

```yaml
tasks:
  - name: combine_scores
    inputs:
      - name: profile_score
        from: profile.profile_score
        field: score
      - name: profile_confidence
        from: profile.profile_score
        field: confidence
      - name: activity_score
        from: activity.activity_score
        field: score
      - name: activity_confidence
        from: activity.activity_score
        field: confidence
    outputs:
      - name: combined_score
      - name: combined_confidence
    logic: combine_scores
```
