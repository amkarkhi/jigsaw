# 🧩 Jigsaw

**A modular, configuration-driven task orchestration engine for Go**

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

---

## 🎯 What is Jigsaw?

Jigsaw is a powerful, flexible workflow orchestration framework that allows you to build complex data pipelines and business logic flows using simple YAML configurations. Define tasks, chain them into flows, and let Jigsaw handle the execution, error handling, and data passing—all without writing boilerplate code.

**Perfect for:**

- 🔄 Building data processing pipelines
- 🔍 Creating search and aggregation workflows
- 🌐 Orchestrating microservice interactions
- 💾 Managing complex caching strategies
- 🔀 Implementing multi-step business processes

---

## ✨ Key Features

### 🎨 **Configuration-Driven**

Define everything in YAML—tasks, flows, providers, and endpoints. No code changes needed for new workflows.

### 🔥 **Hot-Reload**

Modify configurations on the fly. Changes are applied instantly without restarting the server.

### 🧬 **Inheritance System**

Create base tasks and flows, then extend them with inheritance. DRY principle applied to configurations.

### 🛡️ **Flexible Error Handling**

Multiple fallback strategies: abort, continue with defaults, switch tasks, or failover to alternate providers.

### ⚡ **Parallel Execution**

Run independent tasks concurrently to maximize throughput and minimize latency.

### 🔌 **Provider Abstraction**

Swap databases, caches, and services without changing task definitions. Support for lazy loading and connection pooling.

### 🎯 **Dynamic Routing**

Route requests to different flows based on parameters, headers, or custom tags.

### 📦 **Package-First Design**

Import Jigsaw into any Go project as a library. Use it as a standalone service or embed it in your application.

### 📊 **Structured Logging**

Built-in zerolog integration provides detailed, structured logs for debugging and monitoring.

---

## 🏗️ Architecture Overview

```
HTTP Request → Endpoint → Flow Router → Flow Executor
                                            │
                                            ▼
                                    Task 1 → Task 2 → Task 3
                                       ↓        ↓        ↓
                                    Providers (Cache, Database, APIs...)
```

**Core Concepts:**

- **Task**: A unit of work with inputs, outputs, and logic
- **Flow**: A sequence of tasks executed in order
- **Provider**: External services (DB, cache, API) used by tasks
- **Endpoint**: HTTP routes that trigger flows
- **Context**: Runtime data carrier that passes through the flow

👉 [Read the full architecture documentation](docs/ARCHITECTURE.md)  
👉 [View the ERD and data model](docs/ERD.md)

---

## 🚀 Quick Start

### Installation

```bash
# Install as a package
go get github.com/amkarkhi/jigsaw

# Or clone the repository
git clone https://github.com/amkarkhi/jigsaw.git
cd jigsaw
```

### Basic Usage

```go
package main

import (
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/server"
)

func main() {
    // Load configurations
    cfg := engine.LoadConfig("./configs")

    // Start the server
    srv := server.New(cfg)
    srv.Start(":8080")
}
```

### Using the CLI

```bash
# Start the server with hot-reload
jigsaw serve --config ./configs --port 8080

# Launch Terminal UI (TUI)
jigsaw ui tui --config ./configs

# Launch Web UI
jigsaw ui web --config ./configs --port 3000

# Validate configurations
jigsaw validate --config ./configs

# Test a specific flow
jigsaw test flow --name search_flow --input '{"query": "test"}'

# List all flows and tasks
jigsaw list flows
jigsaw list tasks
```

---

## 📝 Configuration Examples

### 1. Define a Task

**File: `configs/tasks/cache_check.yml`**

```yaml
tasks:
  - name: cache_check
    description: Check if result exists in cache
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
    logic: check_key_exists
    timeout: 1000
    retry: 2
    fallback:
      strategy: continue
      defaults:
        value: null
        found: false
```

### 2. Define a Flow

**File: `configs/flows/search_flow.yml`**

```yaml
flows:
  - name: search_flow
    description: Standard search workflow with caching
    tasks:
      - parse_params
      - cache_check
      - query_builder
      - search_execute
      - cache_save
      - response_builder
```

### 3. Define a Provider

**File: `configs/providers/cache.yml`**

```yaml
providers:
  - name: cache
    type: cache
    config:
      host: localhost
      port: 6379
      db: 0
      password: ""
      max_retries: 3
    init_mode: pooled
    pool_size: 10
```

### 4. Define an Endpoint

**File: `configs/endpoints/search.yml`**

```yaml
endpoints:
  - name: search
    path: /api/search
    method: POST
    flows:
      - condition:
          sub: 1
        flow: basic_search_flow
      - condition:
          sub: 2
          tag: advanced
        flow: advanced_search_flow
```

---

## 🎯 Use Case Example

### Search API with Multi-Level Caching

**Scenario:** Build a search API that checks cache, queries a database, and saves results back to cache.

#### Flow Definition

```yaml
flows:
  - name: search_with_cache
    description: Search with cache and database fallback
    tasks:
      - name: parse_input
        logic: validate_and_parse_search_params

      - name: check_cache
        provider: cache
        logic: get_cached_result
        fallback:
          strategy: continue

      - name: check_if_cached
        logic: evaluate_cache_hit

      - name: build_query
        logic: create_search_query
        condition: cache_miss

      - name: search_database
        provider: search_engine
        logic: execute_search
        fallback:
          strategy: switch_provider
          providers: [search_fallback, database]

      - name: save_cache
        provider: cache
        logic: store_result
        condition: cache_miss
        fallback:
          strategy: continue

      - name: format_response
        logic: build_json_response
```

**Endpoint Configuration:**

```yaml
endpoints:
  - name: search
    path: /api/search
    method: POST
    flows:
      - condition: { sub: 1 }
        flow: search_with_cache
```

**Request:**

```bash
curl -X POST http://localhost:8080/api/search \
  -H "Content-Type: application/json" \
  -d '{"query": "golang frameworks", "sub": 1}'
```

**What Happens:**

1. ✅ Input is validated and parsed
2. 🔍 Cache is checked
3. ⚡ If cache hit → return immediately
4. 🔨 If cache miss → build search query
5. 🔎 Execute search (with fallback providers if primary fails)
6. 💾 Save result to cache
7. 📦 Format and return response

---

## 🧬 Inheritance Examples

### Task Inheritance

```yaml
# base_validation.yml
tasks:
  - name: base_validator
    inputs:
      - name: data
        type: object
    logic: validate_schema

# specific_validation.yml
tasks:
  - name: search_validator
    inherits: base_validator
    inputs:
      - name: data
        type: object
      - name: query_type
        type: string
    logic: validate_search_schema  # overrides base logic
```

### Flow Inheritance

```yaml
# base_flow.yml
flows:
  - name: base_search
    tasks:
      - parse_params
      - search
      - response

# extended_flow.yml
flows:
  - name: cached_search
    inherits: base_search
    tasks:
      - parse_params
      - cache_check      # added
      - search
      - cache_save       # added
      - response
```

---

## 🛡️ Fallback Strategies

### 1. Abort

Stop execution immediately on error.

```yaml
fallback:
  strategy: abort
  message: "Critical authentication failed"
```

### 2. Continue

Continue with default values.

```yaml
fallback:
  strategy: continue
  defaults:
    result: []
    count: 0
```

### 3. Switch Task

Jump to alternative task.

```yaml
fallback:
  strategy: switch_task
  target_task: backup_search
```

### 4. Switch Provider

Try alternate providers in order.

```yaml
fallback:
  strategy: switch_provider
  providers: [search_engine, search_fallback, database]
```

---

## ⚡ Parallel Execution

Run independent tasks concurrently:

```yaml
flows:
  - name: parallel_operations
    tasks:
      - name: parse_input

      - parallel:
          - cache_lookup
          - metrics_increment
          - audit_log

      - name: process_data # waits for all parallel tasks
```

---

## 🔧 CLI Commands

```bash
# Start server
jigsaw serve --config ./configs --port 8080 --reload

# Validate all configurations
jigsaw validate --config ./configs

# Test a flow with sample input
jigsaw test flow --name search_flow --input input.json

# Test a single task
jigsaw test task --name cache_check --input '{"key": "test"}'

# List available flows
jigsaw list flows

# List available tasks
jigsaw list tasks

# Show flow details
jigsaw describe flow --name search_flow

# Show task details
jigsaw describe task --name cache_check

# Check provider connections
jigsaw check providers
```

---

## 📦 Using as a Package

### Import in Your Go Project

```go
package main

import (
    "context"
    "log"

    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/router"
)

func main() {
    // Load configuration
    cfg, err := config.Load("./configs")
    if err != nil {
        log.Fatal(err)
    }

    // Create engine
    eng := engine.New(cfg)

    // Execute a flow programmatically
    ctx := context.Background()
    result, err := eng.ExecuteFlow(ctx, "search_flow", map[string]interface{}{
        "query": "golang",
        "sub": 1,
    })

    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Result: %+v", result)
}
```

### Embed in HTTP Server

```go
package main

import (
    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/server"
)

func main() {
    cfg, _ := config.Load("./configs")

    // Create Jigsaw server
    srv := server.New(cfg, server.Options{
        Port: 8080,
        HotReload: true,
    })

    // Start server
    srv.Start()
}
```

---

## 🗂️ Project Structure

```
jigsaw/
├── cmd/jigsaw/              # CLI application
│   └── main.go
├── pkg/                     # Public package exports
│   ├── config/              # Configuration loading & hot-reload
│   ├── context/             # Execution context
│   ├── engine/              # Flow and task execution engine
│   ├── provider/            # Provider interfaces & registry
│   ├── router/              # Flow routing logic
│   ├── server/              # Gin HTTP server
│   ├── validator/           # Configuration validator
│   └── logger/              # Zerolog wrapper
├── internal/                # Private implementation
│   └── loader/              # (Reserved for future use)
├── configs/                 # Example configurations
│   ├── tasks/
│   ├── flows/
│   ├── providers/
│   └── endpoints/
├── examples/                # Usage examples
├── docs/                    # Documentation
│   ├── ARCHITECTURE.md
│   └── ERD.md
└── README.md
```

---

## 🛠️ Technology Stack

- **Go 1.24+** - Modern Go features
- **Gin** - High-performance HTTP server
- **Cobra** - Powerful CLI framework
- **Zerolog** - Fast, structured logging
- **YAML** - Human-friendly configuration
- **fsnotify** - File system watching for hot-reload

---

## 🎓 Learn More

- 📖 [Architecture Guide](docs/ARCHITECTURE.md) - Deep dive into system design
- 🗺️ [ERD Documentation](docs/ERD.md) - Entity relationships and data model
- 💡 [Examples](examples/) - Real-world usage examples
- 🔧 [API Reference](docs/API.md) - Go package documentation

---

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgments

Built with ❤️ using:

- [Gin Web Framework](https://github.com/gin-gonic/gin)
- [Cobra CLI](https://github.com/spf13/cobra)
- [Zerolog](https://github.com/rs/zerolog)

---

## 📞 Support

- 🐛 Issues: [GitHub Issues](https://github.com/amkarkhi/jigsaw/issues)

---

<p align="center">
  <strong>⭐ Star us on GitHub — it helps!</strong>
</p>

<p align="center">
  Made with ☕ and 🧩
</p>
