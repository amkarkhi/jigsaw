# Jigsaw Entity Relationship Diagram

## Core Entities and Relationships

```
┌─────────────────────────────────────────────────────────────────────┐
│                         CONFIGURATION LAYER                          │
└─────────────────────────────────────────────────────────────────────┘

┌──────────────────┐
│    Endpoint      │
│──────────────────│
│ + name: string   │
│ + path: string   │
│ + method: string │
│ + flows: []FlowMapping │
└────────┬─────────┘
         │ 1
         │ maps to
         │ *
         ▼
┌──────────────────┐
│   FlowMapping    │
│──────────────────│
│ + sub: int       │  (direct sub → flow mapping)
│ + flow_name: str │
└────────┬─────────┘
         │
         │ references
         │
         ▼
┌──────────────────┐         ┌──────────────────┐
│      Flow        │         │   Task           │
│──────────────────│         │──────────────────│
│ + name: string   │         │ + name: string   │
│ + description    │         │ + description    │
│ + inherits: str? │         │ + inherits: str? │
│ + tasks: []TR    │   *─────│ + inputs: []Inp  │
│ + metadata: map  │    uses │ + outputs: []Out │
│                  │─────────│ + label: str?    │
│                  │         │ + provider: str? │
└────────┬─────────┘         │ + fallback: FB   │
         │                   │ + logic: string  │
         │ can inherit       │ + timeout: int   │
         │                   │ + retry: int     │
         ▼                   │ + metadata: map  │
┌──────────────────┐         └────────┬─────────┘
│   Flow (parent)  │                  │
│──────────────────│                  │ uses
│ + name: string   │                  │ 0..1
│ + tasks: []str   │                  ▼
└──────────────────┘         ┌──────────────────┐
                             │    Provider      │
                             │──────────────────│
                             │ + name: string   │
                             │ + type: string   │
                             │ + config: map    │
                             │ + init_mode: str │
                             │ + pool_size: int │
                             └──────────────────┘


┌─────────────────────────────────────────────────────────────────────┐
│                         RUNTIME LAYER                                │
└─────────────────────────────────────────────────────────────────────┘

┌──────────────────┐
│   HTTPRequest    │
│──────────────────│
│ + method         │
│ + path           │
│ + headers        │
│ + body           │
│ + query_params   │
└────────┬─────────┘
         │ creates
         │ 1
         ▼
┌──────────────────────────┐
│   ExecutionContext       │
│──────────────────────────│
│ + request_id: string     │
│ + flow_name: string      │
│ + current_task: string   │
│ + parameters: map        │
│ + headers: map           │
│ + tags: []string         │
│ + task_outputs: map      │
│ + last_output: any       │
│ + metadata: map          │
│ + created_at: time       │
│ + updated_at: time       │
└────────┬─────────────────┘
         │ executes
         │ 1
         ▼
┌──────────────────────────┐
│   FlowExecution          │
│──────────────────────────│
│ + flow: Flow             │
│ + context: ExecContext   │
│ + status: string         │
│ + current_step: int      │
│ + started_at: time       │
│ + completed_at: time?    │
│ + error: error?          │
└────────┬─────────────────┘
         │ contains
         │ 1..*
         ▼
┌──────────────────────────┐
│   TaskExecution          │
│──────────────────────────│
│ + task: Task             │
│ + inputs: map            │
│ + outputs: map           │
│ + status: string         │
│ + started_at: time       │
│ + completed_at: time?    │
│ + error: error?          │
│ + fallback_used: bool    │
│ + retry_count: int       │
└────────┬─────────────────┘
         │ may use
         │ 0..*
         ▼
┌──────────────────────────┐
│   ProviderInstance       │
│──────────────────────────│
│ + provider: Provider     │
│ + connection: interface  │
│ + status: string         │
│ + connected_at: time     │
│ + last_used: time        │
│ + pool: ConnectionPool?  │
└──────────────────────────┘
```

## Detailed Entity Definitions

### Configuration Entities

#### Endpoint
Defines an HTTP route that maps to flows.

**Attributes:**
- `name` (string): Unique endpoint identifier
- `path` (string): HTTP path (e.g., "/search")
- `method` (string): HTTP method (GET, POST, etc.)
- `flows` ([]FlowMapping): Mapping rules to select flows

**Relationships:**
- 1 Endpoint → * FlowMapping

**Example:**
```yaml
name: search_endpoint
path: /api/search
method: POST
flows:
  - sub: 1
    flow: basic_search
  - sub: 2
    flow: advanced_search
  - sub: 3
    flow: premium_search
```

---

#### FlowMapping
Defines direct mapping from sub parameter to flow.

**Attributes:**
- `sub` (int): Sub parameter value (unique per endpoint)
- `flow_name` (string): Target flow to execute

**Relationships:**
- * FlowMapping → 1 Flow (references)

**Note:** Tag and headers are stored in context and used for task-level overrides, not flow selection.

---

#### Flow
Defines a sequence of tasks to execute.

**Attributes:**
- `name` (string): Unique flow identifier
- `description` (string): Human-readable description
- `inherits` (string?): Parent flow to inherit from
- `tasks` ([]TaskRef): Ordered list of task references; each is either a single task or a `parallel:` block
- `metadata` (map): Additional configuration

**Related entities:**
- `TaskRef`: exactly one of `name` or `parallel` must be set.
- `ParallelBlock { on_branch_failure: "continue"|"cancel", branches: []Branch }`.
- `Branch { label: string, tasks: []TaskRef }` — branches run concurrently and may themselves contain parallel blocks (recursive).
- `Task.label` (string?): flow-local logical name for the task's outputs.
- `FieldDef.from` (string?): for inputs, a dotted path `[branch.]*label` selecting a producer; `field` (string?) picks one output field. See [parallel-execution.md](parallel-execution.md).

**Relationships:**
- 1 Flow → * Task (uses)
- 1 Flow → 0..1 Flow (inherits from)

**Example:**
```yaml
name: search_flow
description: Standard search workflow
tasks:
  - parse_params
  - cache_check
  - search
  - cache_save
  - response_builder
```

---

#### Task
Defines a unit of work with inputs, outputs, and logic.

**Attributes:**
- `name` (string): Unique task identifier
- `description` (string): What the task does
- `inherits` (string?): Parent task to inherit from
- `inputs` ([]Input): Required input fields
- `outputs` ([]Output): Output fields produced
- `provider` (string?): Provider to use (if any)
- `fallback` (Fallback): Error handling strategy
- `overrides` ([]Override): Conditional task overrides
- `logic` (string): Logic identifier or script
- `timeout` (int): Max execution time (ms)
- `retry` (int): Number of retries on failure
- `metadata` (map): Additional configuration

**Relationships:**
- * Task → 0..1 Provider (uses)
- 1 Task → 0..1 Task (inherits from)

**Example:**
```yaml
name: cache_check
description: Check Redis cache for result
inputs:
  - name: key
    type: string
    required: true
  - name: tag
    type: string
    required: false
outputs:
  - name: value
    type: string
  - name: found
    type: boolean
provider: redis
fallback:
  strategy: continue
  defaults:
    value: null
    found: false
timeout: 1000
retry: 2
```

---

#### Override
Defines conditional task execution overrides based on context.

**Attributes:**
- `condition` (map): Key-value pairs to match (e.g., tag: "premium", header: "X-Version: v2")
- `action` (string): Action to take: "skip", "replace"
- `task` (string): Replacement task name (if action is "replace")

**Example:**
```yaml
overrides:
  - condition: {tag: "no-cache"}
    action: skip
  - condition: {tag: "premium"}
    action: replace
    task: premium_cache_check
```

---

#### Input
Defines an input field for a task.

**Attributes:**
- `name` (string): Field name
- `type` (string): Data type (string, int, bool, object, array)
- `required` (bool): Whether field is mandatory
- `default` (any): Default value if not provided
- `validation` (string): Validation rules (regex, range, etc.)

---

#### Output
Defines an output field from a task.

**Attributes:**
- `name` (string): Field name
- `type` (string): Data type
- `required` (bool): Whether field must be produced

---

#### Fallback
Defines error handling strategy for a task.

**Attributes:**
- `strategy` (string): One of: abort, continue, switch_task, switch_provider
- `message` (string): Error message
- `defaults` (map): Default values (for continue strategy)
- `target_task` (string): Task to switch to (for switch_task)
- `providers` ([]string): Alternate providers (for switch_provider)

---

#### Provider
Defines an external service or resource.

**Attributes:**
- `name` (string): Unique provider identifier
- `type` (string): Provider type (redis, mysql, http, etc.)
- `config` (map): Connection configuration
- `init_mode` (string): lazy, eager, pooled
- `pool_size` (int): Connection pool size (for pooled mode)

**Example:**
```yaml
name: redis
type: redis
config:
  host: localhost
  port: 6379
  db: 0
  password: ""
init_mode: pooled
pool_size: 10
```

---

### Runtime Entities

#### HTTPRequest
Incoming HTTP request.

**Attributes:**
- `method` (string): HTTP method
- `path` (string): Request path
- `headers` (map): HTTP headers
- `body` ([]byte): Request body
- `query_params` (map): Query parameters

---

#### ExecutionContext
Runtime context for flow execution.

**Attributes:**
- `request_id` (string): Unique request identifier
- `flow_name` (string): Current flow being executed
- `current_task` (string): Current task being executed
- `parameters` (map): Request parameters
- `headers` (map): HTTP headers
- `tags` ([]string): Tags for routing
- `task_outputs` (map): Outputs from all tasks
- `last_output` (any): Output from previous task
- `metadata` (map): Additional runtime data
- `created_at` (time): Context creation time
- `updated_at` (time): Last update time

---

#### FlowExecution
Represents a flow execution instance.

**Attributes:**
- `flow` (Flow): Flow definition
- `context` (ExecutionContext): Execution context
- `status` (string): pending, running, completed, failed
- `current_step` (int): Current task index
- `started_at` (time): Execution start time
- `completed_at` (time?): Execution end time
- `error` (error?): Error if failed

**Relationships:**
- 1 FlowExecution → 1..* TaskExecution

---

#### TaskExecution
Represents a task execution instance.

**Attributes:**
- `task` (Task): Task definition
- `inputs` (map): Input values
- `outputs` (map): Output values
- `status` (string): pending, running, completed, failed, skipped
- `started_at` (time): Execution start time
- `completed_at` (time?): Execution end time
- `error` (error?): Error if failed
- `fallback_used` (bool): Whether fallback was triggered
- `retry_count` (int): Number of retries attempted

**Relationships:**
- * TaskExecution → 0..* ProviderInstance

---

#### ProviderInstance
Runtime instance of a provider.

**Attributes:**
- `provider` (Provider): Provider definition
- `connection` (interface): Actual connection object
- `status` (string): disconnected, connecting, connected, error
- `connected_at` (time): Connection establishment time
- `last_used` (time): Last usage time
- `pool` (ConnectionPool?): Connection pool if pooled mode

---

## Entity Lifecycle

### Configuration Lifecycle
```
1. YAML files loaded → Parsed → Validated
2. Entities created and stored in registry
3. Hot-reload watches for file changes
4. On change: Reload → Re-validate → Update registry
```

### Runtime Lifecycle
```
1. HTTP Request arrives
2. Create ExecutionContext
3. Route to Flow based on conditions
4. Create FlowExecution
5. For each Task in Flow:
   a. Create TaskExecution
   b. Get/Create ProviderInstance (if needed)
   c. Execute task logic
   d. Store outputs in context
   e. Handle fallback on error
6. Return response
7. Cleanup (optional connection release)
```

## Cardinality Summary

| Relationship | Cardinality | Description |
|--------------|-------------|-------------|
| Endpoint → FlowMapping | 1:* | One endpoint can map to multiple flows |
| FlowMapping → Flow | *:1 | Many mappings can reference same flow |
| Flow → Task | 1:* | One flow uses multiple tasks |
| Flow → Flow (inherit) | *:0..1 | Many flows can inherit from one parent |
| Task → Task (inherit) | *:0..1 | Many tasks can inherit from one parent |
| Task → Provider | *:0..1 | Many tasks can use same provider |
| HTTPRequest → ExecutionContext | 1:1 | Each request creates one context |
| ExecutionContext → FlowExecution | 1:1 | Each context executes one flow |
| FlowExecution → TaskExecution | 1:* | One flow execution has many task executions |
| TaskExecution → ProviderInstance | *:* | Tasks can use multiple providers |

## Data Flow Diagram

```
[HTTP Request]
      │
      ▼
[Endpoint Matcher] ──────► [Flow Registry]
      │                           │
      │                           ▼
      │                    [Flow Definition]
      │                           │
      ▼                           ▼
[ExecutionContext] ◄──────[Flow Executor]
      │                           │
      │                           ▼
      │                    [Task Executor] ◄──► [Provider Registry]
      │                           │                      │
      │                           ▼                      ▼
      └──────────────────► [Context Updates]    [Provider Instance]
                                  │
                                  ▼
                            [Response Builder]
                                  │
                                  ▼
                            [HTTP Response]
```
