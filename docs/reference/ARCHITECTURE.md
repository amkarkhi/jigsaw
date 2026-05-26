# Jigsaw Architecture

## Overview

Jigsaw is a modular, configuration-driven task orchestration engine that enables dynamic workflow execution through YAML configurations. It provides a flexible framework for chaining tasks, managing providers, and routing requests through configurable flows.

## Core Concepts

### 1. Task
A **Task** is the smallest unit of execution in Jigsaw. Each task:
- Has defined **inputs** and **outputs**
- Can use one or more **providers** to perform work
- Contains **fallback strategies** for error handling
- Supports **inheritance** from other tasks
- Can have **wrappers** for cross-cutting concerns (v2.0+)
- Executes business logic (API calls, DB queries, transformations, etc.)

### 2. Flow
A **Flow** is a sequence of tasks executed in order. Each flow:
- Chains multiple tasks together
- Manages data passing between tasks
- Validates inputs/outputs at each step
- Supports **inheritance** from other flows
- Can be dynamically selected based on parameters

### 3. Provider
A **Provider** is an external service or resource that tasks use to perform operations:
- Database connections
- Cache systems
- Search engines
- External APIs (HTTP clients)
- Message queues

Providers support:
- **Lazy loading** (connect on first use)
- **Pre-initialization** (connect at startup)
- **Connection pooling** (managed connections)

### 4. Endpoint
An **Endpoint** is an HTTP route that:
- Maps to flows using the `sub` parameter (direct mapping)
- Each `sub` value corresponds to exactly one flow
- Returns structured responses

**Note:** Tags and headers are used for task-level overrides, not flow selection.

### 5. Context
A **Context** object that:
- Carries data through the entire flow execution
- Stores outputs from previous tasks
- Holds metadata (headers, parameters, tags)
- Provides scoped access to tasks (selective data exposure)

## System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        HTTP Server (Gin)                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│  │ Endpoint │  │ Endpoint │  │ Endpoint │                  │
│  │ /search  │  │ /process │  │ /analyze │                  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘                  │
└───────┼─────────────┼─────────────┼────────────────────────┘
        │             │             │
        └─────────────┴─────────────┘
                      │
        ┌─────────────▼──────────────┐
        │    Flow Router/Selector     │
        │  (based on sub, tag, etc.)  │
        └─────────────┬──────────────┘
                      │
        ┌─────────────▼──────────────┐
        │      Flow Executor          │
        │  ┌──────────────────────┐  │
        │  │   Execution Context   │  │
        │  │  - Input/Output Data  │  │
        │  │  - Metadata           │  │
        │  │  - Scoped Access      │  │
        │  └──────────────────────┘  │
        │                             │
        │  ┌─────┐  ┌─────┐  ┌─────┐│
        │  │Task1├─►│Task2├─►│Task3││
        │  └──┬──┘  └──┬──┘  └──┬──┘│
        │     │        │        │    │
        └─────┼────────┼────────┼────┘
              │        │        │
        ┌─────▼────────▼────────▼────┐
        │     Provider Registry       │
        │  ┌──────┐  ┌──────┐        │
        │  │Cache │  │Database │  ...   │
        │  └──────┘  └──────┘        │
        └─────────────────────────────┘
              │
        ┌─────▼─────────────────────┐
        │   Configuration Loader     │
        │   (Hot-reload support)     │
        │  - Tasks YAML              │
        │  - Flows YAML              │
        │  - Providers YAML          │
        │  - Endpoints YAML          │
        └────────────────────────────┘
```

## Data Flow

### Request Flow
```
1. HTTP Request → Endpoint Handler
2. Extract parameters (sub as primary flow selector, tag/headers for context)
3. Flow Router maps sub → flow (direct mapping)
4. Initialize Execution Context with tag and metadata
5. Execute the task list sequentially; any `parallel:` block runs its branches concurrently and joins before the next step
6. Each task:
   - Check for override conditions (tag, headers, context keys)
   - Validates inputs
   - Executes logic (uses providers if needed)
   - Stores outputs in context
   - Handles fallback on error
7. Response builder formats final output
8. HTTP Response returned
```

### Task Execution Flow
```
┌─────────────────────────────────────────┐
│            Task Execution                │
│                                          │
│  0. Wrapper Check (NEW!)                 │
│     ├─ Check if task has wrapper         │
│     ├─ If yes: execute wrapper instead   │
│     └─ Wrapper invokes actual task       │
│                                          │
│  1. Pre-execution                        │
│     ├─ Validate inputs                   │
│     └─ Get required providers            │
│                                          │
│  2. Execution                            │
│     ├─ Access context (scoped)           │
│     ├─ Execute business logic            │
│     └─ Use providers (if needed)         │
│                                          │
│  3. Post-execution                       │
│     ├─ Validate outputs                  │
│     ├─ Store in context                  │
│     └─ Prepare for next task             │
│                                          │
│  4. Error Handling (on failure)          │
│     ├─ Abort flow                        │
│     ├─ Continue with defaults            │
│     ├─ Switch to fallback task           │
│     └─ Switch to fallback provider       │
│                                          │
└─────────────────────────────────────────┘
```

## Fallback Strategies

### 1. Abort Strategy
Immediately stops flow execution and returns error.
```yaml
fallback:
  strategy: abort
  message: "Critical task failed"
```

### 2. Continue Strategy
Continues flow with default values or null outputs.
```yaml
fallback:
  strategy: continue
  defaults:
    key: "default_value"
```

### 3. Switch Task Strategy
Jumps to a different task on failure.
```yaml
fallback:
  strategy: switch_task
  target_task: "alternative_search"
```

### 4. Switch Provider Strategy
Tries alternative provider on failure.
```yaml
fallback:
  strategy: switch_provider
  providers: ["search_engine", "search_fallback", "database"]
```

## Task Wrappers (v2.0+)

Wrappers allow you to add cross-cutting concerns (caching, metrics, logging, rate limiting) to tasks without cluttering flow definitions.

### How Wrappers Work

1. **Define wrapper once** - Generic wrapper task (e.g., `cache`)
2. **Apply to any task** - Add `wrapper:` field to task definition
3. **Automatic execution** - Wrapper intercepts task execution
4. **Transparent I/O** - Wrapper inherits task's input/output schema

### Wrapper Execution Flow

```
Flow references task "search"
        ↓
Task "search" has wrapper: cache
        ↓
Execute wrapper task "cache"
        ↓
Wrapper checks ctx.Nested (points to "search")
        ↓
Wrapper invokes actual "search" task
        ↓
Wrapper processes result (e.g., caches it)
        ↓
Returns result to flow
```

### Example Configuration

```yaml
# Define wrapper task
tasks:
  - name: cache
    logic: cache_wrapper

# Apply wrapper to any task
tasks:
  - name: search
    logic: search
    wrapper:
      task: cache
      params:
        keys: [query]
        ttl: 120s

# Use in flows (wrapper is automatic)
flows:
  - name: search_flow
    tasks:
      - name: search    # Caching happens automatically
```

### Benefits

- ✅ **Separation of concerns** - Business logic separate from infrastructure
- ✅ **Reusability** - Same wrapper works with any task
- ✅ **Maintainability** - Change caching strategy in one place
- ✅ **Clean flows** - No wrapper boilerplate in flow definitions
- ✅ **Composability** - Can chain multiple wrappers (future)

### Common Use Cases

1. **Caching** - Automatically cache expensive operations
2. **Metrics** - Track execution time and success rates
3. **Rate Limiting** - Throttle API calls
4. **Retry Logic** - Add exponential backoff
5. **Circuit Breaking** - Prevent cascading failures
6. **Logging** - Enhanced structured logging
7. **Validation** - Input/output validation layers

👉 See [WRAPPER_PATTERN.md](WRAPPER_PATTERN.md) for complete guide

## Configuration Inheritance

### Task Inheritance
```yaml
# base_cache.yml
tasks:
  - name: base_cache_check
    inputs: [key]
    outputs: [value]
    provider: cache
    logic: check_cache

# specialized_cache.yml
tasks:
  - name: search_cache_check
    inherits: base_cache_check
    inputs: [key, tag, sub]  # extends inputs
    ttl: 3600  # adds new field
```

### Flow Inheritance
```yaml
# base_search_flow.yml
flows:
  - name: base_search
    tasks: [parse_params, cache_check, search, response]

# specialized_search_flow.yml
flows:
  - name: advanced_search
    inherits: base_search
    tasks:
      - parse_params
      - cache_check
      - query_builder  # adds new task
      - search
      - response
```

## Task Override System

Tasks can be conditionally overridden based on context values:
```yaml
flows:
  - name: search_flow
    tasks:
      - name: parse_params
      
      - name: cache_check
        overrides:
          - condition: {tag: "no-cache"}
            action: skip
          - condition: {tag: "premium"}
            task: premium_cache_check
            
      - name: search
        overrides:
          - condition: {tag: "advanced"}
            task: advanced_search
```

## Parallel Execution Support

A flow can fan out into N concurrent **branches**, each of which is itself a
sequence of tasks. The flow continues only after every branch has joined.
Failure policy is per-block: `continue` (default) lets siblings run to
completion; `cancel` aborts in-flight siblings via context cancellation.

Branch outputs are exposed to downstream tasks through a label-based
addressing scheme — never silently merged by the engine. See
[parallel-execution.md](parallel-execution.md) for the full design (schema,
runtime model, label resolution, validation, authoring best practices).

```yaml
flows:
  - name: parallel_search
    tasks:
      - name: parse_params
      - parallel:
          on_branch_failure: continue
          branches:
            - label: cache
              tasks:
                - name: cache_check
            - label: audit
              tasks:
                - name: audit_log
      - name: search       # downstream tasks join after every branch returns
      - name: response
```

## Context Management

### Context Structure
```go
type ExecutionContext struct {
    // Request metadata
    RequestID   string
    Parameters  map[string]interface{}
    Headers     map[string]string
    Tags        []string
    
    // Task data
    TaskOutputs map[string]interface{}  // All task outputs
    LastOutput  interface{}              // Previous task output
    
    // Flow control
    CurrentTask string
    FlowName    string
    
    // Provider access
    Providers   *ProviderRegistry
    
    // Logger
    Logger      zerolog.Logger
}
```

### Scoped Access
Tasks only see:
1. Their defined inputs (from context)
2. Last task output (if needed)
3. Specific context keys (configurable)
4. Their assigned providers

## Package Structure

```
jigsaw/
├── cmd/
│   └── jigsaw/
│       └── main.go          # CLI entry point
├── pkg/
│   ├── config/              # Configuration loading & hot-reload
│   ├── context/             # Execution context
│   ├── engine/              # Flow and task execution
│   ├── provider/            # Provider interfaces & registry
│   ├── router/              # Flow routing logic
│   ├── server/              # Gin HTTP server
│   └── logger/              # Zerolog wrapper
├── internal/
│   ├── loader/              # YAML loader implementations
│   └── validator/           # Input/Output validation
├── configs/                 # Example configurations
│   ├── tasks/
│   ├── flows/
│   ├── providers/
│   └── endpoints/
├── examples/                # Example usage
└── docs/                    # Documentation
```

## Technology Stack

- **Language**: Go 1.24
- **HTTP Server**: Gin (github.com/gin-gonic/gin)
- **CLI Framework**: Cobra (github.com/spf13/cobra)
- **Logger**: Zerolog (github.com/rs/zerolog)
- **Config Format**: YAML (gopkg.in/yaml.v3)
- **Hot Reload**: fsnotify (github.com/fsnotify/fsnotify)

## Key Features

1. ✅ **Configuration-driven**: Everything defined in YAML
2. ✅ **Hot-reload**: Changes to configs apply without restart
3. ✅ **Inheritance**: Tasks and flows can inherit and extend
4. ✅ **Flexible fallbacks**: Multiple error handling strategies
5. ✅ **Provider abstraction**: Swappable service implementations
6. ✅ **Parallel execution**: Run tasks concurrently when possible
7. ✅ **Dynamic routing**: Select flows based on parameters
8. ✅ **Scoped context**: Tasks access only what they need
9. ✅ **Package-first**: Designed for reuse across projects
10. ✅ **Structured logging**: Zerolog integration throughout

## Design Principles

1. **Simplicity**: Keep interfaces and implementations straightforward
2. **Modularity**: Each component has clear boundaries
3. **Extensibility**: Easy to add new providers, tasks, strategies
4. **Testability**: Interfaces allow easy mocking and testing
5. **Performance**: Lazy loading, connection pooling, parallel execution
6. **Observability**: Comprehensive logging at every step
