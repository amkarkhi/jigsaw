# Generic Task Wrapper Pattern

## Overview

The wrapper pattern allows you to define generic, reusable task wrappers (like caching, logging, metrics) at the **task level** rather than at the flow level. This makes flows cleaner and more maintainable.

## How It Works

### Task-Level Wrapper Definition

Define a wrapper directly on the task:

```yaml
tasks:
  - name: search
    description: "Search with automatic caching"
    logic: search
    wrapper:
      task: cache          # The wrapper task to use
      params:              # Parameters for the wrapper
        keys: [query]      # Cache key fields
        ttl: 120s          # Cache TTL
```

### Clean Flow Definition

The flow simply references the task - the wrapper is automatic:

```yaml
flows:
  - name: search_flow
    tasks:
      - name: search       # Wrapper automatically applied
        bind:
          in:
            query: query
          out:
            results: search_results
```

## Comparison: Old vs New Pattern

### Old Pattern (Flow-Level Nesting)

```yaml
# Flow has to explicitly define the nesting
flows:
  - name: cached_search
    tasks:
      - name: cache
        nested:
          task: search     # Inner task specified in flow
        params:
          keys: [query]
          ttl: 120s
        bind:
          in:
            query: query
          out:
            results: search_results
```

**Problems:**
- Flow is cluttered with wrapper details
- Caching logic mixed with flow logic
- Hard to reuse across multiple flows
- I/O binding is on the wrapper, not the actual task

### New Pattern (Task-Level Wrapper)

```yaml
# Task definition - search.yml
tasks:
  - name: search
    logic: search
    wrapper:
      task: cache
      params:
        keys: [query]
        ttl: 120s

# Flow definition - clean!
flows:
  - name: cached_search
    tasks:
      - name: search       # That's it!
        bind:
          in:
            query: query
          out:
            results: search_results
```

**Benefits:**
- Flow focuses on business logic only
- Caching is a task property, not a flow concern
- Easy to enable/disable by changing task definition
- I/O binding directly on the logical task
- One task definition → many flows can use it

## Writing a Wrapper Task

A wrapper task must:

1. Accept `map[string]any` as input/output (transparent I/O)
2. Check `ctx.Nested.Task` to know which task to invoke
3. Call `ctx.Engine.InvokeTask(ctx, ctx.Nested.Task, inputs, nil)`

### Example: Cache Wrapper

```go
func (CacheWrapperLogic) Run(
    ctx *types.ExecutionContext,
    in map[string]any,           // Pass-through inputs
    p cacheWrapperParams,
    prov types.ProviderInstance,
) (*map[string]any, error) {
    if ctx.Nested == nil || ctx.Nested.Task == "" {
        return nil, fmt.Errorf("wrapper requires ctx.Nested")
    }

    // Build cache key from specified input fields
    key := buildCacheKey(ctx.Nested.Task, p.Keys, in)
    
    // Check cache
    if cached, hit := getCache(key); hit {
        return &cached, nil
    }
    
    // Invoke the actual task
    result, err := ctx.Engine.InvokeTask(ctx, ctx.Nested.Task, in, nil)
    if err != nil {
        return nil, err
    }
    
    // Save to cache
    setCache(key, result, p.TTL)
    
    return &result, nil
}
```

## Use Cases

### 1. Caching

```yaml
tasks:
  - name: expensive_api_call
    logic: call_api
    wrapper:
      task: cache
      params:
        keys: [user_id, query]
        ttl: 300s
```

### 2. Metrics/Observability

```yaml
tasks:
  - name: critical_operation
    logic: process_data
    wrapper:
      task: metrics_wrapper
      params:
        metric_name: "critical_op_duration"
        track_errors: true
```

### 3. Rate Limiting

```yaml
tasks:
  - name: external_api
    logic: call_external
    wrapper:
      task: rate_limiter
      params:
        max_requests: 100
        window: 1m
```

### 4. Retry with Backoff

```yaml
tasks:
  - name: flaky_service
    logic: call_service
    wrapper:
      task: retry_wrapper
      params:
        max_attempts: 3
        backoff: exponential
```

## Implementation Details

### Execution Flow

1. Flow executor encounters a task reference
2. Task executor checks if task has a `wrapper` field
3. If wrapper exists:
   - Resolve wrapper task
   - Gather inputs using **original task's schema**
   - Set `ctx.Nested` to point to original task
   - Execute **wrapper task** with original task's I/O
   - Publish outputs using **original task's bindings**
4. If no wrapper, execute task normally

### Key Features

- **Transparent I/O**: Wrapper inherits the wrapped task's input/output schema
- **Parameter Merging**: Wrapper params + task params + flow params (flow wins)
- **Inheritance Support**: Wrappers work with task inheritance
- **Nested Wrappers**: Can chain multiple wrappers (future enhancement)

## Migration Guide

To migrate from the old nested pattern to the new wrapper pattern:

### Step 1: Move wrapper definition to task

**Before:**
```yaml
flows:
  - name: my_flow
    tasks:
      - name: cache
        nested:
          task: search
        params:
          keys: [query]
```

**After:**
```yaml
tasks:
  - name: search
    logic: search
    wrapper:
      task: cache
      params:
        keys: [query]

flows:
  - name: my_flow
    tasks:
      - name: search
```

### Step 2: Update flow references

Change all flow references from the wrapper task to the actual task.

### Step 3: Test

The behavior should be identical, but with cleaner configuration.

## Best Practices

1. **Keep wrappers generic**: Don't hardcode task names in wrapper logic
2. **Use transparent I/O**: Wrappers should use `map[string]any` for I/O
3. **Document wrapper params**: Make it clear what parameters wrappers need
4. **Default parameters**: Provide sensible defaults in wrapper task definition
5. **Composability**: Design wrappers to work well together

## Troubleshooting

### "wrapper requires ctx.Nested" error

The wrapper logic needs to check for `ctx.Nested`. Ensure your wrapper task is properly registered and the task has a `wrapper` field defined.

### Cache not working

Check:
- `keys` parameter matches your input field names
- TTL is properly formatted (e.g., "60s", "5m", "1h")
- Cache provider is properly configured

### Wrong I/O schema

Remember: I/O bindings are on the **wrapped task**, not the wrapper. The wrapper sees the same input/output as the wrapped task.
