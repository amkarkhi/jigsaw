# Using Jigsaw as a Package

## 🎯 Core Concept

**Jigsaw is an orchestration framework, NOT a template.**

- ✅ Jigsaw handles: routing, flow control, configuration, error handling
- 🔧 YOU implement: business logic, provider connections, actual work

Think of Jigsaw like **Gin** or **Echo** for HTTP routing, but for workflow orchestration.

## Architecture

```
┌───────────────────────────────────────────────────────┐
│              YOUR APPLICATION                          │
│                                                        │
│  ┌──────────────────────────────────────────────┐   │
│  │  YOUR Business Logic                         │   │
│  │  • Cache operations                          │   │
│  │  • Database queries                          │   │
│  │  • API calls                                 │   │
│  │  • Data transformations                      │   │
│  └──────────────────────────────────────────────┘   │
│                      ▲                                │
│                      │ registers with                │
│                      ▼                                │
│  ┌──────────────────────────────────────────────┐   │
│  │  Jigsaw Package (import)                     │   │
│  │  • Flow routing                              │   │
│  │  • Task orchestration                        │   │
│  │  • Configuration management                  │   │
│  │  • Error handling                            │   │
│  └──────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────┘
```

## Quick Example

### 1. Install Package

```bash
go get github.com/amkarkhi/jigsaw
```

### 2. Define Configuration (YAML)

```yaml
# config/flows/my_flow.yml
flows:
  - name: process_order
    tasks:
      - name: validate_order
      - name: check_inventory
      - name: process_payment
      - name: send_confirmation
```

### 3. Implement YOUR Logic (Go)

```go
package main

import (
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/types"
)

func main() {
    // Setup Jigsaw
    eng := engine.New(cfg, val, logger)
    
    // Register YOUR business logic
    eng.MustRegisterLogic("validate_order", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
        // YOUR validation code
        order := inputs["order"].(map[string]any)
        
        if order["total"].(float64) < 0 {
            return nil, errors.New("invalid order total")
        }
        
        return map[string]any{
            "validated": true,
            "order_id": order["id"],
        }, nil
    })
    
    eng.MustRegisterLogic("check_inventory", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
        // YOUR inventory check code
        db := provider.GetConnection().(*sql.DB)
        
        // Query YOUR database
        var available bool
        err := db.QueryRow("SELECT available FROM inventory WHERE id = ?", inputs["product_id"]).Scan(&available)
        
        return map[string]any{
            "available": available,
        }, err
    })
    
    // Jigsaw orchestrates, YOUR code executes
    result, err := eng.ExecuteFlow(ctx, "process_order", 1, params, headers, providerReg)
}
```

## Key Concepts

### 1. Logic Handlers

Logic handlers are YOUR functions that do the actual work:

```go
type LogicHandler func(
    ctx *types.ExecutionContext,  // Jigsaw context
    inputs map[string]any,          // Task inputs
    provider types.ProviderInstance, // Optional provider
) (map[string]any, error)           // Task outputs
```

**Example:**

```go
eng.MustRegisterLogic("my_logic", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
    // YOUR code here
    result := doSomething(inputs)
    
    return map[string]any{
        "result": result,
    }, nil
})
```

### 2. Provider Implementations

Providers are YOUR connections to external services:

```go
// YOUR cache provider
type MyCacheProvider struct {
    client any // Your cache client
}

func (p *MyCacheProvider) Connect(ctx context.Context) error {
    // Initialize your cache client
    // p.client = cache.NewClient(...)
    return nil
}

func (p *MyCacheProvider) GetConnection() any {
    return p.client
}

// Register it
providerReg.Register("cache", myCacheProvider)
```

### 3. Configuration

Jigsaw loads configuration, YOU define what tasks do:

```yaml
tasks:
  - name: my_task
    logic: my_logic_handler  # YOUR registered handler
    inputs:
      - name: data
        type: object
    outputs:
      - name: result
        type: object
```

## Complete Example: Search Service

### Your Project Structure

```
my-search-service/
├── main.go                    # YOUR application
├── logic/
│   ├── cache.go              # YOUR cache logic
│   ├── search.go             # YOUR search logic
│   └── query.go              # YOUR query logic
├── providers/
│   ├── cache.go              # YOUR cache implementation
│   └── search.go             # YOUR search engine implementation
├── configs/                   # Jigsaw configurations
│   ├── tasks/
│   ├── flows/
│   └── endpoints/
└── go.mod
```

### main.go

```go
package main

import (
    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/server"
    "github.com/amkarkhi/jigsaw/pkg/validator"
    
    "my-search-service/logic"
    "my-search-service/providers"
)

func main() {
    // 1. Setup Jigsaw
    jigsawLogger := logger.New("info", false)
    loader := config.NewLoader(jigsawLogger)
    cfg, _ := loader.Load("./configs")
    val := validator.New(jigsawLogger)
    eng := engine.New(cfg, val, jigsawLogger)
    
    // 2. Register YOUR logic handlers
    logic.RegisterHandlers(eng)
    
    // 3. Create YOUR provider registry
    providerReg := providers.NewRegistry(jigsawLogger)
    
    // 4. Start Jigsaw server (or use engine directly)
    srv := server.New(cfg, jigsawLogger, server.Options{
        Port: 8080,
        HotReload: true,
    })
    
    srv.Start(8080, "./configs")
}
```

### logic/search.go (YOUR CODE)

```go
package logic

import (
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/types"
)

func RegisterHandlers(eng *engine.Engine) {
    // YOUR search logic
    eng.MustRegisterLogic("execute_search", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
        // Get YOUR search engine client
        searchClient := provider.GetConnection() // Your search client type
        
        // Build YOUR query
        query := inputs["query"].(map[string]any)
        
        // Execute YOUR search
        results, err := executeSearch(searchClient, query, ctx.Context)
        
        if err != nil {
            return nil, err
        }
        
        return map[string]any{
            "results": results,
            "total": len(results),
        }, nil
    })
    
    // YOUR cache logic
    eng.MustRegisterLogic("get_from_cache", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
        // YOUR cache code
        cacheClient := provider.GetConnection() // Your cache client type
        key := inputs["key"].(string)
        
        val, err := getFromCache(cacheClient, key, ctx.Context)
        // ... YOUR implementation
        return map[string]any{"value": val, "found": err == nil}, err
    })
}
```

### providers/search.go (YOUR CODE)

```go
package providers

import (
    "context"
    "github.com/amkarkhi/jigsaw/pkg/types"
)

type SearchProvider struct {
    client any // Your search engine client
    config map[string]any
}

func NewSearchProvider(config map[string]any) *SearchProvider {
    return &SearchProvider{config: config}
}

func (p *SearchProvider) Connect(ctx context.Context) error {
    // Initialize your search engine client
    // client, err := yourSearchClient.New(config)
    // p.client = client
    return nil
}

func (p *SearchProvider) GetConnection() any {
    return p.client
}

func (p *SearchProvider) IsConnected() bool {
    return p.client != nil
}

// Implement other required methods...
```

## Benefits

### ✅ Clean Separation
- Jigsaw: Orchestration framework
- Your Code: Business logic

### ✅ Configuration-Driven
- Change flows without recompiling
- Add/remove tasks via YAML
- Override behavior with tags

### ✅ Testable
```go
// Test YOUR logic independently
func TestSearchLogic(t *testing.T) {
    handler := mySearchLogic
    
    result, err := handler(ctx, inputs, mockProvider)
    
    assert.NoError(t, err)
    assert.Equal(t, expected, result)
}
```

### ✅ Reusable
- Use same Jigsaw in multiple projects
- Different logic per project
- Share flow patterns

## Comparison

### ❌ NOT Like This (Template)
```
Clone jigsaw → Modify code → Deploy
```

### ✅ Like This (Package)
```
Import jigsaw → Register YOUR logic → Deploy
```

## Real-World Scenarios

### Scenario 1: E-commerce Order Processing

```go
// YOUR order service
eng.MustRegisterLogic("validate_order", yourValidateOrderFunc)
eng.MustRegisterLogic("check_inventory", yourCheckInventoryFunc)
eng.MustRegisterLogic("process_payment", yourProcessPaymentFunc)
eng.MustRegisterLogic("send_email", yourSendEmailFunc)

// Jigsaw orchestrates the flow
result, _ := eng.ExecuteFlow(ctx, "order_flow", 1, orderData, headers, providers)
```

### Scenario 2: Data Pipeline

```go
// YOUR data pipeline
eng.MustRegisterLogic("extract_data", yourExtractFunc)
eng.MustRegisterLogic("transform_data", yourTransformFunc)
eng.MustRegisterLogic("validate_data", yourValidateFunc)
eng.MustRegisterLogic("load_data", yourLoadFunc)

// Jigsaw orchestrates ETL
result, _ := eng.ExecuteFlow(ctx, "etl_flow", 1, config, headers, providers)
```

### Scenario 3: API Gateway

```go
// YOUR API logic
eng.MustRegisterLogic("auth_check", yourAuthFunc)
eng.MustRegisterLogic("rate_limit", yourRateLimitFunc)
eng.MustRegisterLogic("proxy_request", yourProxyFunc)
eng.MustRegisterLogic("log_request", yourLogFunc)

// Jigsaw orchestrates API flow
result, _ := eng.ExecuteFlow(ctx, "api_flow", 1, request, headers, providers)
```

## Summary

| Aspect | Jigsaw Provides | You Implement |
|--------|----------------|---------------|
| Flow Control | ✅ | - |
| Configuration | ✅ | - |
| Routing | ✅ | - |
| Error Handling | ✅ | - |
| Business Logic | - | ✅ |
| Providers | - | ✅ |
| Data Processing | - | ✅ |
| External Calls | - | ✅ |

**Jigsaw = Orchestration Framework**  
**Your Code = Business Logic**

Use Jigsaw like you use Gin for HTTP or GORM for databases - as a tool, not a template! 🎯
