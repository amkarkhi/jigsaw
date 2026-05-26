# Parallel Execution

This document is the design reference for parallel task execution in Jigsaw. It covers the schema, runtime model, data-flow rules, validation, failure semantics, and authoring best practices.

## 1. Goals

- Allow a flow to fan out into N independent **branches**, each of which is itself a sequence of tasks (and may contain further parallel blocks).
- Branches run concurrently; the flow continues only after all branches have completed.
- Outputs of parallel work are exposed to downstream tasks through a flat, namespaced scope key (`<branch_label>.<key>`). The engine never silently merges branch outputs.
- A failure in one branch does **not** cancel siblings by default. Authors may opt into cancel-on-failure per parallel block.

## 2. Schema

### 2.1 Parallel block

```yaml
- parallel:
    on_branch_failure: continue     # "continue" (default) | "cancel"
    branches:
      - label: left
        tasks:
          - name: fetch_profile
            bind:
              out:
                profile: profile
          - name: score_profile
            bind:
              in:
                profile: profile
      - label: right
        tasks:
          - name: fetch_activity
            bind:
              out:
                activity: activity
          - name: score_activity
            bind:
              in:
                activity: activity
```

A `TaskRef` is either a normal task (`name:` + optional `bind:` + optional `overrides:`) **or** a parallel block (`parallel:`), never both.

### 2.2 Scope wiring — `bind.in` / `bind.out`

Tasks inside a branch use `bind.in` and `bind.out` exactly as in a sequential flow. The only difference is that when a branch task publishes an output, the key lives in that branch's **local scope**.

After all branches complete, the engine merges each branch's local scope into the parent under `<branch_label>.<key>`. Downstream tasks read merged keys using `bind.in`:

```yaml
- name: combine_scores
  bind:
    in:
      profile_score: left.score    # branch "left", scope key "score"
      activity_score: right.score  # branch "right", scope key "score"
```

### 2.3 Branch path grammar

A branch output key in the parent scope is always `<branch_label>.<original_key>`.

For nested parallel blocks (a branch that itself contains a `parallel:` block), the path extends: `<outer_label>.<inner_label>.<key>`.

## 3. Runtime model

### 3.1 Execution

The flow executor uses one recursive routine: `executeTaskList(ctx, tasks, flowExec)`. The top-level flow calls it once; each branch calls it again on its own task list. Nested parallels fall out of recursion.

### 3.2 Concurrency primitive

`executeParallel` uses **`sync.WaitGroup` + `context.WithCancel`**:

- One goroutine per branch.
- All goroutines share a derived `gctx` from `context.WithCancel(execCtx.Context)`.
- `on_branch_failure: cancel` calls `cancel()` on the first hard failure; sibling tasks that respect `ctx.Context` short-circuit.
- `on_branch_failure: continue` lets every branch run to completion regardless.
- After `wg.Wait()`, results are merged back into the parent context serially (in branch-declaration order).

### 3.3 Branch context fork

Before spawning a branch, `context.Fork` creates a branch-local `ExecutionContext`:

| Field | Treatment |
|---|---|
| `RequestID`, `FlowName`, `FlowVersion`, `Sub`, `Tag`, `Parameters`, `Headers`, `Providers` | shared (read-only or internally thread-safe) |
| `Logger` | wrapped with `{"branch": <label>}` |
| `Context` | replaced with `gctx` |
| `Scope` | fresh empty map; reads fall back to the parent via `parentScope` |
| `Versions`, `Metadata` | shallow-cloned snapshot at fork time |

Branches write only to their own `Scope`; the parent's scope is never directly mutated during branch execution.

### 3.4 Join / merge

After all branches return, `context.Merge(parent, branch)` is called for each branch in declaration order:

1. Every key the branch wrote to its local `Scope` is published to the parent under `<branch_label>.<key>`.
2. Branch `Versions` merge into the parent (write-once semantics; no key collisions expected).
3. Branch `TaskExecution` records are appended to `flowExec.Tasks` in branch-declaration order (deterministic).

## 4. Validation

`engine.ValidateFlows()` performs a static scope-tracking pass over all flows. For parallel blocks it:

1. Gives each branch a copy of the current parent scope for read simulation.
2. Validates each branch's tasks against that snapshot.
3. After all branches, publishes new keys the branch introduced under `<branch_label>.<key>` in the parent scope snapshot.

This catches missing `bind.in` sources and type collisions before any request is served.

## 5. Failure semantics

### 5.1 `on_branch_failure: continue` (default)

- A task inside a branch fails. If the task has `fallback.strategy: continue`, the branch keeps going.
- Otherwise, the branch stops at that point; already-completed task outputs still merge back.
- Other branches keep running.
- The parallel block as a whole fails only if **every** branch failed.

### 5.2 `on_branch_failure: cancel`

- The first hard failure calls `cancel()`. All sibling goroutines that respect `ctx.Context` abort.
- The flow as a whole is failed; subsequent steps do not run.

## 6. Validation rules (static)

`ValidateFlows` (and the YAML-level `ValidateConfig`) reject:

1. A `TaskRef` with both `name` and `parallel` set, or neither.
2. A parallel block with zero branches.
3. A branch with zero tasks.
4. A branch with a duplicate label in the same parallel block.
5. A `bind.in` key that refers to a scope variable not yet produced at that point in the flow.
6. A `bind.out` rename that would overwrite a scope key with an incompatible type.
7. `on_branch_failure` set to anything other than `continue` / `cancel`.

## 7. Best practices for task authors

### 7.1 Respect `ctx.Context`

Every blocking call inside a `LogicHandler` must accept and respect the `context.Context` from `ExecutionContext.Context`. Without this, `on_branch_failure: cancel` cannot stop sibling work.

```go
// Good
req, _ := http.NewRequestWithContext(execCtx.Context, "GET", url, nil)
resp, err := http.DefaultClient.Do(req)

// Good — cancellable sleep
select {
case <-time.After(5 * time.Second):
case <-execCtx.Context.Done():
    return nil, execCtx.Context.Err()
}
```

### 7.2 Don't reach across branches at runtime

A task inside branch A must not depend on data from branch B. Branches are truly independent; treat their scopes as isolated.

### 7.3 Handle absent inputs explicitly

When a branch may fail (`on_branch_failure: continue`), downstream tasks should handle absent scope keys. Mark handler input fields as optional (not in `required`) or provide schema defaults.

### 7.4 Use a collector task after a fan-out

When two branches do similar work, put a collector task immediately after the parallel block that reads each branch's contribution via `bind.in` with the `<branch_label>.<key>` form.

```yaml
- parallel:
    branches:
      - label: A
        tasks:
          - name: fetch_chunk_a
      - label: B
        tasks:
          - name: fetch_chunk_b
- name: merge_chunks
  bind:
    in:
      chunk_a: A.items
      chunk_b: B.items
```

### 7.5 Provider safety

Providers are accessed from concurrent branches via the shared registry (mutex-protected). Provider **instances** must be safe for concurrent use. Pooled providers (`init_mode: pooled`) are the safe default for parallel workloads.

## 8. Observability

- Branch logs are emitted with `branch=<label>`.
- `flowExec.Tasks` is ordered: branch tasks are grouped in branch-declaration order.
- `FlowExecution.CurrentStep` advances only at the parent flow level. The parallel block counts as a single step.

## 9. Non-goals (v1)

- Dynamic fan-out over a runtime list (`for each x in items: do X`).
- Per-branch timeouts distinct from per-task `Timeout`.
- Global concurrency caps. Per-block `max_concurrent` may be added later.
- Streaming partial results from branches.

## 10. Worked example

```yaml
flows:
  - name: enrich_user
    tasks:
      - name: fetch_user
        bind:
          out:
            user_id: user_id
      - parallel:
          on_branch_failure: continue
          branches:
            - label: profile
              tasks:
                - name: fetch_profile
                  bind:
                    in:
                      user_id: user_id
                - name: score_profile
                  bind:
                    in:
                      profile: profile
            - label: activity
              tasks:
                - name: fetch_activity
                  bind:
                    in:
                      user_id: user_id
                - name: score_activity
                  bind:
                    in:
                      activity: activity
      - name: combine_scores
        bind:
          in:
            profile_score: profile.score
            profile_confidence: profile.confidence
            activity_score: activity.score
            activity_confidence: activity.confidence
      - name: persist_result
        bind:
          in:
            combined_score: combined_score
            combined_confidence: combined_confidence
```
