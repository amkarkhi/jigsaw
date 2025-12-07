# Jigsaw Quick Reference

## 🚀 CLI Commands

```bash
# Serve
jigsaw serve --config ./configs --port 8080 --reload

# Validate
jigsaw validate --config ./configs

# List Resources
jigsaw list flows --config ./configs
jigsaw list tasks --config ./configs
jigsaw list providers --config ./configs
jigsaw list endpoints --config ./configs

# Describe
jigsaw describe flow <name> --config ./configs
jigsaw describe task <name> --config ./configs

# Test
jigsaw test flow <name> --config ./configs --sub 1 --input '{}'
```

## 📝 Configuration Syntax

### Task Definition

```yaml
tasks:
  - name: my_task
    description: "What this task does"
    inputs:
      - name: field_name
        type: string|int|bool|object|array|any
        required: true|false
        default: value
    outputs:
      - name: result
        type: string
        required: true
    provider: provider_name  # optional
    logic: logic_handler_name
    timeout: 5000  # milliseconds
    retry: 3
    fallback:
      strategy: abort|continue|switch_task|switch_provider
      defaults: {}
      target_task: task_name
      providers: [provider1, provider2]
    inherits: parent_task  # optional
```

### Flow Definition

```yaml
flows:
  - name: my_flow
    description: "What this flow does"
    tasks:
      - name: task1
      - name: task2
        overrides:
          - condition: {tag: "value"}
            action: skip|replace
            task: replacement_task
      - parallel:
          - name: task3
          - name: task4
      - name: task5
    inherits: parent_flow  # optional
```

### Provider Definition

```yaml
providers:
  - name: my_provider
    type: redis|mysql|elasticsearch|http|...
    config:
      host: localhost
      port: 6379
      # ... provider-specific config
    init_mode: lazy|eager|pooled
    pool_size: 10  # for pooled mode
```

### Endpoint Definition

```yaml
endpoints:
  - name: my_endpoint
    path: /api/path
    method: GET|POST|PUT|DELETE|PATCH
    description: "What this endpoint does"
    flows:
      - sub: 1
        flow_name: flow_for_sub_1
      - sub: 2
        flow_name: flow_for_sub_2
```

## 🔀 Flow Selection

**Sub parameter selects the flow:**

```bash
# Request with sub=1
curl -X POST http://localhost:8080/api/search \
  -d '{"sub": 1, "query": "test"}'
# → Executes flow mapped to sub=1
```

**Endpoint mapping:**
```yaml
flows:
  - sub: 1
    flow_name: basic_search
  - sub: 2
    flow_name: advanced_search
```

## 🏷️ Task Overrides

**Tag parameter overrides tasks within a flow:**

### Skip Task

```yaml
tasks:
  - name: cache_check
    overrides:
      - condition: {tag: "no-cache"}
        action: skip
```

```bash
curl -X POST http://localhost:8080/api/search \
  -d '{"sub": 2, "tag": "no-cache", "query": "test"}'
# → cache_check task is skipped
```

### Replace Task

```yaml
tasks:
  - name: search
    overrides:
      - condition: {tag: "premium"}
        action: replace
        task: premium_search
```

```bash
curl -X POST http://localhost:8080/api/search \
  -d '{"sub": 2, "tag": "premium", "query": "test"}'
# → search task is replaced with premium_search
```

### Multiple Conditions

```yaml
tasks:
  - name: my_task
    overrides:
      - condition: {tag: "option1"}
        action: skip
      - condition: {tag: "option2"}
        action: replace
        task: alternative_task
      - condition: {header_key: "value"}
        action: skip
```

## 🛡️ Fallback Strategies

### Abort

```yaml
fallback:
  strategy: abort
  message: "Critical error"
```
→ Stops flow execution immediately

### Continue

```yaml
fallback:
  strategy: continue
  defaults:
    result: null
    status: "failed"
```
→ Continues with default values

### Switch Task

```yaml
fallback:
  strategy: switch_task
  target_task: backup_task
```
→ Jumps to alternative task

### Switch Provider

```yaml
fallback:
  strategy: switch_provider
  providers: [elasticsearch, qdrant, mysql]
```
→ Tries providers in order until success

## 🧬 Inheritance

### Task Inheritance

```yaml
# Parent
tasks:
  - name: base_validator
    inputs: [...]
    logic: validate

# Child
tasks:
  - name: advanced_validator
    inherits: base_validator
    inputs: [...]  # extends parent
    timeout: 5000  # overrides parent
```

### Flow Inheritance

```yaml
# Parent
flows:
  - name: base_flow
    tasks: [task1, task2, task3]

# Child
flows:
  - name: extended_flow
    inherits: base_flow
    tasks: [task1, task2, new_task, task3]
```

## 📦 Using as Package

### Basic Usage

```go
import (
    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/provider"
    "github.com/amkarkhi/jigsaw/pkg/validator"
)

// Load config
logger := logger.New("info", true)
loader := config.NewLoader(logger)
cfg, _ := loader.Load("./configs")

// Create engine
val := validator.New(logger)
eng := engine.New(cfg, val, logger)

// Create providers
providerReg := provider.NewRegistry(logger)
for _, p := range cfg.Providers {
    providerReg.RegisterConfig(p)
}

// Execute flow
result, err := eng.ExecuteFlow(
    ctx,
    "flow_name",
    1,  // sub
    map[string]any{"key": "value"},  // params
    map[string]string{},  // headers
    providerReg,
)
```

### Server Usage

```go
import (
    "github.com/amkarkhi/jigsaw/pkg/server"
)

srv := server.New(cfg, logger, server.Options{
    Port:      8080,
    HotReload: true,
})

srv.Start(8080, "./configs")
```

## 🔍 Context Data Flow

```
Request → ExecutionContext
    ↓
  Task 1 (inputs from context)
    ↓ (outputs stored in context)
  Task 2 (inputs from context + Task 1 outputs)
    ↓ (outputs stored in context)
  Task 3 (inputs from context + previous outputs)
    ↓
Response (last task output)
```

**Context contains:**
- `RequestID` - Unique request ID
- `Parameters` - Request parameters
- `Headers` - HTTP headers
- `Tag` - Tag for overrides
- `Sub` - Sub parameter
- `TaskOutputs` - All task outputs (map)
- `LastOutput` - Previous task output
- `Metadata` - Additional data

## 🔧 Makefile Commands

```bash
make build          # Build CLI
make install        # Install globally
make deps           # Download dependencies
make validate       # Validate config
make serve          # Start server
make test-flow      # Test a flow
make list-flows     # List all flows
make list-tasks     # List all tasks
make clean          # Clean build
make help           # Show help
```

## 📊 HTTP API

### Request Format

```json
{
  "sub": 1,
  "tag": "optional_tag",
  "param1": "value1",
  "param2": "value2"
}
```

### Response Format

```json
{
  "request_id": "req_abc123",
  "flow_name": "my_flow",
  "status": "success",
  "data": { ... },
  "execution_time_ms": 150,
  "metadata": { ... }
}
```

### Health Check

```bash
curl http://localhost:8080/health
```

Response:
```json
{
  "status": "ok",
  "config": {
    "tasks": 7,
    "flows": 4,
    "providers": 4,
    "endpoints": 2
  }
}
```

## 🎯 Common Patterns

### Caching Pattern

```yaml
flows:
  - name: cached_flow
    tasks:
      - name: cache_check
      - name: process_data
      - name: cache_save
```

### Fallback Pattern

```yaml
tasks:
  - name: primary_search
    provider: elasticsearch
    fallback:
      strategy: switch_provider
      providers: [qdrant, mysql]
```

### Conditional Pattern

```yaml
flows:
  - name: conditional_flow
    tasks:
      - name: validator
      - name: processor
        overrides:
          - condition: {tag: "fast"}
            action: replace
            task: fast_processor
          - condition: {tag: "slow"}
            action: replace
            task: slow_processor
```

### Parallel Pattern

```yaml
flows:
  - name: parallel_flow
    tasks:
      - name: prepare
      - parallel:
          - name: task_a
          - name: task_b
          - name: task_c
      - name: aggregate
```

## 🐛 Debugging

### Enable Debug Logging

```bash
jigsaw serve --config ./configs --log-level debug --pretty
```

### Check Configuration

```bash
jigsaw validate --config ./configs --log-level debug
```

### Test Flow Locally

```bash
jigsaw test flow my_flow \
  --config ./configs \
  --sub 1 \
  --input '{"key":"value"}' \
  --log-level debug \
  --pretty
```

### View Flow Details

```bash
jigsaw describe flow my_flow --config ./configs
jigsaw describe task my_task --config ./configs
```

## 📚 File Locations

```
configs/
├── tasks/           # Task definitions (*.yml)
├── flows/           # Flow definitions (*.yml)
├── providers/       # Provider configs (*.yml)
└── endpoints/       # Endpoint configs (*.yml)
```

## 🔗 Quick Links

- [README.md](../README.md) - Main documentation
- [ARCHITECTURE.md](ARCHITECTURE.md) - Architecture details
- [ERD.md](ERD.md) - Data model
- [GETTING_STARTED.md](GETTING_STARTED.md) - Tutorial
- [SUMMARY.md](SUMMARY.md) - Project summary

## 💡 Tips

1. **Start simple** - Begin with basic flows, add complexity later
2. **Use inheritance** - DRY principle for tasks and flows
3. **Test locally** - Use `jigsaw test flow` before deploying
4. **Enable hot-reload** - Iterate faster during development
5. **Use tags wisely** - Keep override conditions simple
6. **Log everything** - Use debug level during development
7. **Validate often** - Run `jigsaw validate` after changes

## ⚠️ Important Notes

- **Sub selects flow** (not tag)
- **Tag overrides tasks** (not flows)
- **Providers are placeholders** - implement actual connections
- **Task logic is extensible** - add your handlers
- **Hot-reload works** for tasks/flows/providers (not endpoints)
- **Context is scoped** - tasks see only their inputs
