# Using Jigsaw as an External Package

This guide shows how to use Jigsaw in your own Go projects.

## Installation

```bash
go get github.com/amkarkhi/jigsaw
```

## Basic Usage

### Example 1: Simple Flow Execution

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
    
    // Validate configuration
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
    
    // Execute a flow
    result, err := eng.ExecuteFlow(
        context.Background(),
        "my_flow",           // flow name
        1,                   // sub parameter
        map[string]any{      // parameters
            "query": "test",
            "limit": 10,
        },
        map[string]string{}, // headers
        providerReg,
    )
    
    if err != nil {
        log.Fatal(err)
    }
    
    log.Printf("Result: %+v", result)
}
```

### Example 2: Embedding HTTP Server

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/server"
    "github.com/amkarkhi/jigsaw/pkg/validator"
)

func main() {
    // Create logger
    logger := logger.New("info", false)
    
    // Load configuration
    loader := config.NewLoader(logger)
    cfg, err := loader.Load("./configs")
    if err != nil {
        log.Fatal(err)
    }
    
    // Validate configuration
    val := validator.New(logger)
    if err := val.ValidateConfig(cfg); err != nil {
        log.Fatal(err)
    }
    
    // Create server
    srv := server.New(cfg, logger, server.Options{
        Port:      8080,
        HotReload: true,
        LogLevel:  "info",
        Pretty:    false,
    })
    
    // Setup graceful shutdown
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    
    // Start server in goroutine
    errChan := make(chan error, 1)
    go func() {
        if err := srv.Start(8080, "./configs"); err != nil {
            errChan <- err
        }
    }()
    
    // Wait for shutdown signal
    select {
    case <-sigChan:
        log.Println("Shutting down...")
        ctx, cancel := context.WithTimeout(context.Background(), 30*1000000000)
        defer cancel()
        if err := srv.Stop(ctx); err != nil {
            log.Fatal(err)
        }
    case err := <-errChan:
        log.Fatal(err)
    }
}
```

### Example 3: Custom Task Logic

```go
package main

import (
    "context"
    "fmt"

    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/provider"
    "github.com/amkarkhi/jigsaw/pkg/types"
    "github.com/amkarkhi/jigsaw/pkg/validator"
)

// CustomTaskExecutor wraps the engine with custom logic
type CustomTaskExecutor struct {
    engine *engine.Engine
}

func (c *CustomTaskExecutor) ExecuteWithCustomLogic(
    ctx context.Context,
    flowName string,
    sub int,
    params map[string]any,
) (*types.ExecutionResult, error) {
    // Add custom pre-processing
    params["custom_field"] = "custom_value"
    
    // Execute flow
    result, err := c.engine.ExecuteFlow(
        ctx,
        flowName,
        sub,
        params,
        make(map[string]string),
        nil, // provider registry
    )
    
    // Add custom post-processing
    if result != nil {
        result.Metadata["custom_metadata"] = "processed"
    }
    
    return result, err
}

func main() {
    logger := logger.New("info", true)
    loader := config.NewLoader(logger)
    cfg, _ := loader.Load("./configs")
    val := validator.New(logger)
    
    eng := engine.New(cfg, val, logger)
    
    customExec := &CustomTaskExecutor{engine: eng}
    
    result, err := customExec.ExecuteWithCustomLogic(
        context.Background(),
        "my_flow",
        1,
        map[string]any{"query": "test"},
    )
    
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    
    fmt.Printf("Result: %+v\n", result)
}
```

### Example 4: Dynamic Configuration

```go
package main

import (
    "context"
    "log"

    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/types"
    "github.com/amkarkhi/jigsaw/pkg/validator"
    "github.com/rs/zerolog"
)

// Logic handler — struct exposes LogicMeta() + Run(...).
type myLogic struct{}

type myIn struct {
    Input1 string `json:"input1"`
}
type myOut struct {
    Output1 string `json:"output1"`
}
type myParams struct{}

func (myLogic) LogicMeta() engine.LogicMeta {
    return engine.LogicMeta{Name: "custom_logic", Version: "1.0.0"}
}
func (myLogic) Run(_ *types.ExecutionContext, in myIn, _ myParams) (myOut, error) {
    return myOut{Output1: "processed: " + in.Input1}, nil
}

func main() {
    log := zerolog.Nop()

    cfg := &types.Config{
        Tasks: map[string]*types.Task{
            "my_task": {Name: "my_task", Logic: "custom_logic", Timeout: 5000},
        },
        Flows: map[string]*types.Flow{
            "my_flow": {
                Name:  "my_flow",
                Tasks: []types.TaskRef{{Name: "my_task"}},
            },
        },
        Providers: make(map[string]*types.Provider),
        Endpoints: make(map[string]*types.Endpoint),
    }

    val := validator.New(log)
    if err := val.ValidateConfig(cfg); err != nil {
        log.Fatal(err)
    }

    eng := engine.New(cfg, val, log)
    engine.MustRegister(eng, myLogic{})

    result, err := eng.ExecuteFlow(
        context.Background(), "my_flow", 1,
        map[string]any{"input1": "test"}, nil, nil,
    )
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Result: %+v", result)
}
```

## Available Packages

All packages under `pkg/` are public and can be imported:

```go
import (
    "github.com/amkarkhi/jigsaw/pkg/config"     // Configuration loading
    "github.com/amkarkhi/jigsaw/pkg/context"    // Execution context
    "github.com/amkarkhi/jigsaw/pkg/engine"     // Flow execution engine
    "github.com/amkarkhi/jigsaw/pkg/logger"     // Logging
    "github.com/amkarkhi/jigsaw/pkg/provider"   // Provider management
    "github.com/amkarkhi/jigsaw/pkg/router"     // Flow routing
    "github.com/amkarkhi/jigsaw/pkg/server"     // HTTP server
    "github.com/amkarkhi/jigsaw/pkg/types"      // Core types
    "github.com/amkarkhi/jigsaw/pkg/validator"  // Configuration validation
)
```

## Key Interfaces

### Engine
```go
type Engine interface {
    ExecuteFlow(ctx context.Context, flowName string, sub int, 
                params map[string]any, headers map[string]string, 
                providers types.ProviderRegistry) (*types.ExecutionResult, error)
    GetFlow(name string) (*types.Flow, error)
    GetTask(name string) (*types.Task, error)
    ListFlows() []string
    ListTasks() []string
}
```

### Validator
```go
type Validator interface {
    ValidateConfig(config *types.Config) error
}
```

### ConfigLoader
```go
type ConfigLoader interface {
    Load(configPath string) (*types.Config, error)
    Watch(configPath string, onChange func(*types.Config)) error
    StopWatch() error
}
```

## Configuration Structure

Your project needs a `configs/` directory with this structure:

```
your-project/
├── main.go
├── configs/
│   ├── tasks/
│   │   └── my_tasks.yml
│   ├── flows/
│   │   └── my_flows.yml
│   ├── providers/
│   │   └── my_providers.yml
│   └── endpoints/
│       └── my_endpoints.yml
└── go.mod
```

## go.mod Example

```go
module myproject

go 1.24

require github.com/amkarkhi/jigsaw v0.1.0
```

## Tips

1. **Always validate configuration** before creating the engine
2. **Use structured logging** - pass logger to all components
3. **Handle errors gracefully** - check all return values
4. **Close providers** - call `providerReg.Close()` on shutdown
5. **Use context** - pass context for cancellation support

## Common Patterns

### Pattern 1: Web Service with Jigsaw
```go
// Use Jigsaw as your workflow engine in a web service
func main() {
    // Setup Jigsaw
    logger := logger.New("info", false)
    loader := config.NewLoader(logger)
    cfg, _ := loader.Load("./configs")
    val := validator.New(logger)
    eng := engine.New(cfg, val, logger)
    
    // Setup your web framework
    http.HandleFunc("/api/process", func(w http.ResponseWriter, r *http.Request) {
        // Use Jigsaw to process requests
        result, err := eng.ExecuteFlow(ctx, "process_flow", 1, params, headers, providerReg)
        // ... handle result
    })
    
    http.ListenAndServe(":8080", nil)
}
```

### Pattern 2: Background Job Processor
```go
// Use Jigsaw for background job processing
func processJob(job Job) error {
    result, err := engine.ExecuteFlow(
        context.Background(),
        job.FlowName,
        job.Sub,
        job.Parameters,
        make(map[string]string),
        providerRegistry,
    )
    return err
}
```

### Pattern 3: Parallel Branches
```go
// Build a flow with two concurrent branches, then a downstream task that
// reads each branch's outputs via bind.in using "<branch_label>.<key>".
// See docs/parallel-execution.md for the full design.
cfg.Tasks["produce"] = &types.Task{Name: "produce", Logic: "produce"}
cfg.Tasks["collect"] = &types.Task{Name: "collect", Logic: "collect"}

cfg.Flows["fanout"] = &types.Flow{
    Name: "fanout",
    Tasks: []types.TaskRef{
        {Parallel: &types.ParallelBlock{
            OnBranchFailure: "continue", // "" | "continue" | "cancel"
            Branches: []types.Branch{
                {Label: "L", Tasks: []types.TaskRef{{Name: "produce"}}},
                {Label: "R", Tasks: []types.TaskRef{{Name: "produce"}}},
            },
        }},
        {Name: "collect", Bind: &types.Bind{
            In: map[string]string{
                "left_value":  "L.value", // branch "L", scope key "value"
                "right_value": "R.value", // branch "R", scope key "value"
            },
        }},
    },
}
```

### Pattern 4: CLI Tool with Workflows
```go
// Use Jigsaw to power a CLI tool
func main() {
    cmd := &cobra.Command{
        Use: "mytool",
        Run: func(cmd *cobra.Command, args []string) {
            // Execute Jigsaw flow
            result, _ := engine.ExecuteFlow(...)
            fmt.Println(result)
        },
    }
    cmd.Execute()
}
```

## Need Help?

- Check `examples/` directory for working examples
- Read `docs/GETTING_STARTED.md` for full tutorial
- See `docs/ARCHITECTURE.md` for system design
- Review `docs/QUICK_REFERENCE.md` for API reference
