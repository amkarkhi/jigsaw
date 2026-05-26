# Getting Started with Jigsaw

## Installation

### Prerequisites
- Go 1.24 or higher
- Git

### Install Dependencies

```bash
# Clone or navigate to your jigsaw project
cd jigsaw

# Download dependencies
go mod download

# Verify installation
go mod verify
```

### Build the CLI

```bash
# Build the jigsaw CLI
go build -o bin/jigsaw ./cmd/jigsaw

# Or install globally
go install ./cmd/jigsaw
```

## Quick Start

### 1. Validate Configuration

First, validate your configuration files:

```bash
./bin/jigsaw validate --config ./configs
```

Expected output:
```
✓ Configuration is valid
  Tasks:     7
  Flows:     4
  Providers: 4
  Endpoints: 2
```

### 2. List Available Resources

```bash
# List all flows
./bin/jigsaw list flows --config ./configs

# List all tasks
./bin/jigsaw list tasks --config ./configs

# List all providers
./bin/jigsaw list providers --config ./configs

# List all endpoints
./bin/jigsaw list endpoints --config ./configs
```

### 3. Test a Flow

Test a flow with sample input:

```bash
./bin/jigsaw test flow basic_search \
  --config ./configs \
  --sub 1 \
  --input '{"query":"golang","limit":10}'
```

### 4. Start the Server

Start the HTTP server:

```bash
./bin/jigsaw serve --config ./configs --port 8080 --reload
```

The server will start on `http://localhost:8080` with hot-reload enabled.

### 5. Make API Requests

```bash
# Basic search (sub=1)
curl -X POST http://localhost:8080/api/search \
  -H "Content-Type: application/json" \
  -d '{"sub": 1, "query": "golang frameworks", "limit": 10}'

# Cached search (sub=2)
curl -X POST http://localhost:8080/api/search \
  -H "Content-Type: application/json" \
  -d '{"sub": 2, "query": "golang frameworks", "limit": 10}'

# Advanced search (sub=3)
curl -X POST http://localhost:8080/api/search \
  -H "Content-Type: application/json" \
  -d '{"sub": 3, "query": "golang frameworks", "limit": 10}'

# With tag override (premium cache)
curl -X POST http://localhost:8080/api/search \
  -H "Content-Type: application/json" \
  -d '{"sub": 2, "tag": "premium", "query": "golang", "limit": 10}'

# With tag override (no cache)
curl -X POST http://localhost:8080/api/search \
  -H "Content-Type: application/json" \
  -d '{"sub": 2, "tag": "no-cache", "query": "golang", "limit": 10}'

# Health check
curl http://localhost:8080/health
```

## Understanding the Flow

### How Sub Parameter Works

The `sub` parameter directly maps to a specific flow:

```yaml
# configs/endpoints/search.yml
endpoints:
  - name: search
    path: /api/search
    method: POST
    flows:
      - sub: 1
        flow_name: basic_search      # No caching
      - sub: 2
        flow_name: cached_search     # With caching
      - sub: 3
        flow_name: advanced_search   # Advanced with aggregations
      - sub: 4
        flow_name: parallel_search   # Parallel execution
```

**Key Points:**
- `sub` is used ONLY for flow selection
- Each `sub` value maps to exactly one flow
- `tag` is NOT used for flow selection

### How Tag Parameter Works

The `tag` parameter is used for **task-level overrides** within a flow:

```yaml
# configs/flows/cached_search.yml
flows:
  - name: cached_search
    tasks:
      - name: cache_check
        overrides:
          - condition: {tag: "no-cache"}
            action: skip                    # Skip cache check
          - condition: {tag: "premium"}
            action: replace
            task: premium_cache_check       # Use premium cache
```

**Key Points:**
- `tag` affects which tasks run or get replaced
- Tags are checked at task execution time
- Multiple override conditions can be defined per task

## Configuration Structure

```
configs/
├── tasks/           # Task definitions
│   ├── common.yml   # Common tasks (parse_params, response_builder)
│   ├── cache.yml    # Cache-related tasks
│   └── search.yml   # Search-related tasks
├── flows/           # Flow definitions
│   ├── basic_search.yml
│   └── advanced_search.yml
├── providers/       # Provider configurations
│   ├── redis.yml
│   └── mysql.yml
└── endpoints/       # HTTP endpoint definitions
    └── search.yml
```

## Creating Your First Flow

### Step 1: Define Tasks

Create `configs/tasks/my_tasks.yml`:

```yaml
tasks:
  - name: my_input_validator
    description: "Validate my custom input"
    inputs:
      - name: user_id
        type: int
        required: true
      - name: action
        type: string
        required: true
    outputs:
      - name: validated
        type: boolean
        required: true
      - name: user_id
        type: int
        required: true
    logic: validate_user_input
    timeout: 1000
    fallback:
      strategy: abort
      message: "Invalid input"

  - name: my_processor
    description: "Process the validated input"
    inputs:
      - name: user_id
        type: int
        required: true
      - name: action
        type: string
        required: true
    outputs:
      - name: result
        type: object
        required: true
    logic: process_action
    timeout: 5000
    fallback:
      strategy: continue
      defaults:
        result: {}
```

### Step 2: Define Flow

Create `configs/flows/my_flow.yml`:

```yaml
flows:
  - name: my_custom_flow
    description: "My custom workflow"
    tasks:
      - name: my_input_validator
      - name: my_processor
      - name: response_builder
```

### Step 3: Define Endpoint

Create or update `configs/endpoints/my_endpoint.yml`:

```yaml
endpoints:
  - name: my_endpoint
    path: /api/my-action
    method: POST
    description: "My custom endpoint"
    flows:
      - sub: 1
        flow_name: my_custom_flow
```

### Step 4: Test

```bash
# Validate
./bin/jigsaw validate --config ./configs

# Test the flow
./bin/jigsaw test flow my_custom_flow \
  --config ./configs \
  --sub 1 \
  --input '{"user_id": 123, "action": "process"}'

# Start server and test via HTTP
./bin/jigsaw serve --config ./configs --port 8080

curl -X POST http://localhost:8080/api/my-action \
  -H "Content-Type: application/json" \
  -d '{"sub": 1, "user_id": 123, "action": "process"}'
```

## Using as a Package

### Basic Usage

```go
package main

import (
    "context"
    "log"

    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/provider"
    "github.com/amkarkhi/jigsaw/pkg/validator"
)

func main() {
    // Create logger
    logger := logger.New("info", true)
    
    // Load configuration
    loader := config.NewLoader(logger)
    cfg, err := loader.Load("./configs")
    if err != nil {
        log.Fatal(err)
    }
    
    // Validate
    val := validator.New(logger)
    if err := val.ValidateConfig(cfg); err != nil {
        log.Fatal(err)
    }
    
    // Create engine
    eng := engine.New(cfg, val, logger)
    
    // Create provider registry
    providerReg := provider.NewRegistry(logger)
    for _, prov := range cfg.Providers {
        providerReg.RegisterConfig(prov)
    }
    
    // Execute flow
    result, err := eng.ExecuteFlow(
        context.Background(),
        "basic_search",
        1,
        map[string]any{
            "query": "golang",
            "limit": 10,
        },
        make(map[string]string),
        providerReg,
    )
    
    if err != nil {
        log.Fatal(err)
    }
    
    log.Printf("Result: %+v", result)
}
```

### Embedding in Your Application

```go
package main

import (
    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/server"
)

func main() {
    logger := logger.New("info", false)
    
    loader := config.NewLoader(logger)
    cfg, _ := loader.Load("./configs")
    
    opts := server.Options{
        Port:      8080,
        HotReload: true,
        LogLevel:  "info",
        Pretty:    false,
    }
    
    srv := server.New(cfg, logger, opts)
    srv.Start(8080, "./configs")
}
```

## Hot-Reload

When hot-reload is enabled, Jigsaw watches for changes in configuration files and automatically reloads them.

**What gets reloaded:**
- ✅ Tasks
- ✅ Flows
- ✅ Providers (new providers are registered)
- ⚠️ Endpoints (require restart)

**Example:**

1. Start server with hot-reload:
```bash
./bin/jigsaw serve --config ./configs --port 8080 --reload
```

2. Edit a task file:
```bash
vim configs/tasks/cache.yml
# Make changes and save
```

3. The server automatically reloads:
```
INFO Configuration file changed file=configs/tasks/cache.yml
INFO Configuration reloaded successfully tasks=7 flows=4
```

## Task Override Examples

### Skip Task Based on Tag

```yaml
flows:
  - name: my_flow
    tasks:
      - name: cache_check
        overrides:
          - condition: {tag: "no-cache"}
            action: skip
```

Request:
```bash
curl -X POST http://localhost:8080/api/search \
  -d '{"sub": 2, "tag": "no-cache", "query": "test"}'
```

Result: `cache_check` task is skipped.

### Replace Task Based on Tag

```yaml
flows:
  - name: my_flow
    tasks:
      - name: search_execute
        overrides:
          - condition: {tag: "advanced"}
            action: replace
            task: advanced_search
```

Request:
```bash
curl -X POST http://localhost:8080/api/search \
  -d '{"sub": 2, "tag": "advanced", "query": "test"}'
```

Result: `search_execute` is replaced with `advanced_search`.

### Multiple Conditions

```yaml
flows:
  - name: my_flow
    tasks:
      - name: cache_check
        overrides:
          - condition: {tag: "no-cache"}
            action: skip
          - condition: {tag: "premium"}
            action: replace
            task: premium_cache_check
          - condition: {tag: "debug"}
            action: replace
            task: debug_cache_check
```

## Next Steps

- Read the [Architecture Guide](../reference/ARCHITECTURE.md) for deep dive
- Check [ERD Documentation](../reference/ERD.md) for data model
- Explore [examples/](../../examples/) for more usage patterns
- Implement your own task logic handlers
- Add provider implementations (Redis, MySQL, etc.)

## Troubleshooting

### Configuration Validation Fails

```bash
# Check syntax
./bin/jigsaw validate --config ./configs

# Enable debug logging
./bin/jigsaw validate --config ./configs --log-level debug
```

### Flow Not Found

- Verify `sub` parameter matches a flow mapping in endpoint
- Check flow name spelling in endpoint configuration
- Run `./bin/jigsaw list flows` to see available flows

### Task Override Not Working

- Ensure `tag` is passed in request body or parameters
- Check override condition matches exactly (case-sensitive)
- Enable debug logging to see override evaluation

### Provider Connection Issues

- Providers are placeholders by default
- Implement actual provider connections in `pkg/provider/base.go`
- Check provider configuration in `configs/providers/`

## Support

For issues and questions:
- Check the documentation in `docs/`
- Review example configurations in `configs/`
- Look at example code in `examples/`
