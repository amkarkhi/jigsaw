# Versioning in Jigsaw

Jigsaw provides comprehensive versioning support to track which versions of flows, tasks, and providers are being executed. This is crucial for debugging, auditing, and maintaining production systems.

## Overview

Versioning in Jigsaw allows you to:
- **Track component versions** during execution
- **Debug issues** by knowing exactly which version of a task or flow was used
- **Audit execution history** with complete version information
- **Manage deployments** by versioning your flows and tasks independently
- **Monitor provider versions** to ensure compatibility

## Version Fields

### Flow Versioning

Add a `version` field to your flow configuration:

```yaml
flows:
  - name: basic_search
    description: "Search flow with caching"
    version: "1.2.0"  # Semantic versioning recommended
    tasks:
      - name: parse_params
      - name: use_cache
      - name: response_builder
```

**Best Practices:**
- Use semantic versioning (MAJOR.MINOR.PATCH)
- Increment MAJOR for breaking changes
- Increment MINOR for new features
- Increment PATCH for bug fixes

### Task Versioning

Add a `version` field to your task configuration:

```yaml
tasks:
  - name: parse_params
    description: "Parse and validate input parameters"
    version: "2.1.0"
    inputs:
      - name: query
        type: string
        required: true
    outputs:
      - name: parsed_query
        type: string
        required: true
    logic: parse_and_validate_params
```

**Version Tracking:**
- Each task can have its own version
- Versions are tracked independently
- Useful for A/B testing different task implementations

### Provider Versioning

Add a `version` field to your provider configuration:

```yaml
providers:
  - name: redis
    type: redis
    version: "7.2.0"  # Redis server version
    config:
      host: localhost
      port: 6379
    init_mode: pooled
    pool_size: 10
```

**Use Cases:**
- Track which database/service version is being used
- Ensure compatibility with specific provider versions
- Document infrastructure dependencies

## Execution Context Versioning

During execution, Jigsaw automatically tracks all versions in the `ExecutionContext`:

```go
type ExecutionContext struct {
    FlowVersion string            // Version of the flow being executed
    Versions    map[string]string // All component versions
    // ... other fields
}
```

The `Versions` map contains:
- `"flow"` → Flow version
- `"task:<task_name>"` → Task version for each executed task
- `"provider:<provider_name>"` → Provider version for each used provider

## Execution Results

Version information is included in the execution result:

```json
{
  "request_id": "req_abc123",
  "flow_name": "basic_search",
  "flow_version": "1.2.0",
  "status": "success",
  "execution_time_ms": 45,
  "versions": {
    "flow": "1.2.0",
    "task:parse_params": "2.1.0",
    "task:use_cache": "1.5.2",
    "task:response_builder": "3.0.1",
    "provider:redis": "7.2.0"
  },
  "data": {
    "response": {...}
  }
}
```

## Logging

Versions are automatically logged during execution:

```
2025-10-25T12:59:03 DBG Executing flow tasks flow=basic_search version=1.2.0 task_count=3
2025-10-25T12:59:03 DBG Executing task task=parse_params version=2.1.0 logic=parse_and_validate_params
2025-10-25T12:59:03 DBG Task completed successfully task=parse_params version=2.1.0 duration=5
2025-10-25T12:59:03 INF Flow execution completed flow=basic_search flow_version=1.2.0 versions={"flow":"1.2.0","task:parse_params":"2.1.0",...}
```

## Programmatic Access

### In Logic Handlers

Access version information from the execution context:

```go
eng.MustRegisterLogic("my_logic", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
    // Get flow version
    flowVersion := ctx.FlowVersion
    
    // Get all versions
    versions := ctx.Versions
    
    // Check specific task version
    if taskVersion, ok := versions["task:parse_params"]; ok {
        ctx.Logger.Info("Using parse_params", map[string]any{
            "version": taskVersion,
        })
    }
    
    return outputs, nil
})
```

### In Task Execution

Task execution records include version information:

```go
type TaskExecution struct {
    TaskVersion     string  // Version of task executed
    ProviderVersion string  // Version of provider used
    LogicVersion    string  // Version of logic handler (if available)
    // ... other fields
}
```

## Use Cases

### 1. Debugging Production Issues

When investigating an issue, you can see exactly which versions were running:

```json
{
  "request_id": "req_error123",
  "flow_version": "1.2.0",
  "versions": {
    "task:query_builder": "2.0.1",  // Aha! This is the buggy version
    "provider:mysql": "8.0.32"
  }
}
```

### 2. A/B Testing

Version different task implementations:

```yaml
tasks:
  - name: recommendation_engine_v1
    version: "1.0.0"
    logic: recommend_basic
    
  - name: recommendation_engine_v2
    version: "2.0.0"
    logic: recommend_ml_based
```

Then use task overrides to route traffic:

```yaml
flows:
  - name: product_recommendations
    tasks:
      - name: recommendation_engine_v1
        overrides:
          - condition:
              tag: "beta"
            action: replace
            task: recommendation_engine_v2
```

### 3. Audit Compliance

Track which versions processed sensitive data:

```go
// Log version information for compliance
ctx.Logger.Info("Processing payment", map[string]any{
    "request_id": ctx.RequestID,
    "versions": ctx.Versions,
    "timestamp": time.Now(),
})
```

### 4. Gradual Rollouts

Deploy new versions gradually:

```yaml
# Week 1: Deploy v2.0.0 to staging
tasks:
  - name: payment_processor
    version: "2.0.0"

# Week 2: Monitor metrics, then promote to production
# Week 3: Deprecate v1.0.0
```

## Best Practices

1. **Always Version Your Components**
   - Even if you start with "1.0.0", having a version is better than none
   - Makes it easier to track changes over time

2. **Use Semantic Versioning**
   - MAJOR.MINOR.PATCH format
   - Clear communication about change impact

3. **Document Version Changes**
   - Keep a CHANGELOG.md for your flows and tasks
   - Link versions to specific features or bug fixes

4. **Monitor Version Usage**
   - Track which versions are running in production
   - Plan deprecation of old versions

5. **Include Versions in Alerts**
   - When errors occur, include version information
   - Helps quickly identify if a recent deployment caused issues

6. **Version Logic Handlers**
   - Consider versioning your logic handler implementations
   - Store version in metadata or comments

## Example: Complete Versioned Configuration

```yaml
# flows/search.yml
flows:
  - name: advanced_search
    version: "2.3.1"
    description: "Advanced search with ML ranking"
    tasks:
      - name: parse_query
      - name: fetch_results
      - name: rank_results
    metadata:
      changelog: "v2.3.1 - Fixed ranking bug for long queries"
      author: "search-team"
      last_updated: "2025-10-25"

# tasks/search.yml
tasks:
  - name: parse_query
    version: "1.5.0"
    description: "Parse and normalize search query"
    logic: parse_search_query
    
  - name: fetch_results
    version: "3.2.1"
    description: "Fetch results from Elasticsearch"
    provider: elasticsearch
    logic: fetch_from_es
    
  - name: rank_results
    version: "2.0.0"
    description: "ML-based result ranking"
    logic: ml_rank_results

# providers/elasticsearch.yml
providers:
  - name: elasticsearch
    type: elasticsearch
    version: "8.10.0"
    config:
      hosts: ["localhost:9200"]
```

## Migration Guide

If you have existing configurations without versions:

1. **Add versions to all components**
   ```yaml
   version: "1.0.0"  # Start with 1.0.0 for existing components
   ```

2. **Update incrementally**
   - No need to version everything at once
   - Start with critical flows and tasks

3. **Backward Compatible**
   - Version field is optional
   - Existing configs work without versions
   - Empty version strings are handled gracefully

## Conclusion

Versioning in Jigsaw provides powerful capabilities for tracking, debugging, and managing your workflow components. By adopting versioning best practices, you can build more maintainable and observable systems.

For more information, see:
- [Configuration Guide](./GETTING_STARTED.md)
- [Task Documentation](./ARCHITECTURE.md)
- [Provider Setup](./PACKAGE_USAGE.md)
