# Logic Handler Validation

This guide explains how to validate that all required logic handlers are properly registered in your Jigsaw application.

## Problem

When you configure tasks in YAML, they reference logic handlers by name:

```yaml
tasks:
  - name: parse_params
    logic: parse_and_validate_params  # ← This needs to be implemented!
```

However, these logic handlers are registered at runtime in your Go code:

```go
eng.MustRegisterLogic("parse_and_validate_params", func(...) {...})
```

**The Issue:** If you forget to register a logic handler, or misspell the name, your application will fail at runtime when that task is executed. The UI can't validate this because it doesn't know which handlers are registered.

## Solution

Jigsaw now provides comprehensive validation APIs to check logic handler registration before runtime failures occur.

## Validation Methods

### 1. Programmatic Validation

Check logic handlers in your application code:

```go
package main

import (
    "fmt"
    "os"
    
    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/validator"
)

func main() {
    log := logger.New("info", true)
    
    // Load config
    loader := config.NewLoader(log)
    cfg, err := loader.Load("./configs")
    if err != nil {
        log.Error("Failed to load config", err, nil)
        os.Exit(1)
    }
    
    // Create engine
    val := validator.New(log)
    eng := engine.New(cfg, val, log)
    
    // Register your logic handlers
    registerLogicHandlers(eng, log)
    
    // ✅ VALIDATE: Check all logic handlers are registered
    errors := eng.ValidateLogicHandlers()
    if len(errors) > 0 {
        log.Error("Logic validation failed", nil, map[string]any{
            "errors": errors,
        })
        
        fmt.Println("\n❌ Missing Logic Handlers:")
        for _, err := range errors {
            fmt.Printf("   • %s (required by task: %s)\n", err.Logic, err.Task)
        }
        fmt.Println("\n💡 Register missing handlers before starting server\n")
        os.Exit(1)
    }
    
    log.Info("✅ All logic handlers validated successfully", nil)
    
    // Continue with server startup...
}

func registerLogicHandlers(eng *engine.Engine, log types.Logger) {
    eng.MustRegisterLogic("parse_and_validate_params", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
        // Implementation...
        return outputs, nil
    })
    
    eng.MustRegisterLogic("check_cache", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
        // Implementation...
        return outputs, nil
    })
    
    // Register all other handlers...
}
```

### 2. HTTP API Validation

Query the validation endpoint when your server is running:

```bash
# Check all logic handlers
curl http://localhost:8080/api/_validate/logic

# Response if all handlers are registered:
{
  "valid": true,
  "total_handlers": 3,
  "message": "All logic handlers are properly registered"
}

# Response if handlers are missing:
{
  "valid": false,
  "total_handlers": 1,
  "errors": [
    {
      "type": "missing_logic_handler",
      "logic": "parse_and_validate_params",
      "task": "parse_params",
      "message": "Logic handler 'parse_and_validate_params' required by task 'parse_params' is not registered"
    }
  ]
}
```

### 3. List All Logic Handlers

Get information about registered handlers:

```bash
# List all registered logic handlers with metadata
curl http://localhost:8080/api/_logic

# Response:
{
  "handlers": [
    {
      "name": "parse_and_validate_params",
      "description": "Parses and validates input parameters",
      "version": "2.1.0",
      "registered_at": "2025-10-25T12:00:00Z",
      "used_by": ["parse_params", "validate_input"]
    },
    {
      "name": "check_cache",
      "version": "1.5.2",
      "registered_at": "2025-10-25T12:00:00Z",
      "used_by": ["use_cache"]
    }
  ],
  "total": 2
}
```

### 4. Get Specific Handler Info

```bash
curl http://localhost:8080/api/_validate/logic/parse_and_validate_params

# Response:
{
  "name": "parse_and_validate_params",
  "description": "Parses and validates input parameters",
  "version": "2.1.0",
  "registered_at": "2025-10-25T12:00:00Z",
  "used_by": ["parse_params"]
}
```

## Advanced: Register Logic with Metadata

You can register logic handlers with additional metadata for better documentation:

```go
import "github.com/amkarkhi/jigsaw/pkg/engine"

// Register with metadata
info := &engine.LogicHandlerInfo{
    Description: "Parses and validates input parameters",
    Version:     "2.1.0",
}

err := eng.RegisterWithMetadata("parse_and_validate_params", myHandler, info)
if err != nil {
    log.Error("Failed to register logic", err, nil)
}
```

Or use the convenience method:

```go
eng.MustRegisterLogic("parse_and_validate_params", myHandler)
// Metadata is automatically created with basic info
```

## Integration with CI/CD

### Pre-Deployment Validation

Add validation to your CI/CD pipeline:

```bash
#!/bin/bash
# validate-logic.sh

# Start your application in validation mode
go run main.go --validate-only

# Or use a dedicated validation command
go run main.go validate

# Exit with error if validation fails
if [ $? -ne 0 ]; then
    echo "❌ Logic validation failed"
    exit 1
fi

echo "✅ Logic validation passed"
```

### Example Validation Command

Add a validation command to your application:

```go
// cmd/validate.go
func validateCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "validate",
        Short: "Validate configuration and logic handlers",
        RunE: func(cmd *cobra.Command, args []string) error {
            log := logger.New("info", true)
            
            // Load config
            loader := config.NewLoader(log)
            cfg, err := loader.Load(configPath)
            if err != nil {
                return fmt.Errorf("failed to load config: %w", err)
            }
            
            // Validate config structure
            val := validator.New(log)
            if err := val.ValidateConfig(cfg); err != nil {
                return fmt.Errorf("invalid configuration: %w", err)
            }
            
            // Create engine and register handlers
            eng := engine.New(cfg, val, log)
            registerLogicHandlers(eng, log)
            
            // Validate logic handlers
            errors := eng.ValidateLogicHandlers()
            if len(errors) > 0 {
                fmt.Println("\n❌ Validation Failed:")
                for _, err := range errors {
                    fmt.Printf("   • %s\n", err.Message)
                }
                return fmt.Errorf("logic validation failed")
            }
            
            fmt.Println("\n✅ All validations passed!")
            fmt.Printf("   • Tasks: %d\n", len(cfg.Tasks))
            fmt.Printf("   • Flows: %d\n", len(cfg.Flows))
            fmt.Printf("   • Logic Handlers: %d\n", len(eng.ListLogicHandlers()))
            
            return nil
        },
    }
}
```

## Best Practices

### 1. Validate Early

Always validate logic handlers during application startup, before starting the HTTP server:

```go
func main() {
    // ... load config, create engine, register handlers ...
    
    // Validate BEFORE starting server
    if errors := eng.ValidateLogicHandlers(); len(errors) > 0 {
        log.Error("Logic validation failed", nil, map[string]any{"errors": errors})
        os.Exit(1)
    }
    
    // Now safe to start server
    srv.Start(8080, "./configs")
}
```

### 2. Use Consistent Naming

Keep logic handler names consistent between YAML and Go:

```yaml
# tasks/search.yml
tasks:
  - name: search_products
    logic: search_products_v2  # Clear, versioned name
```

```go
// main.go
eng.MustRegisterLogic("search_products_v2", searchProductsV2Handler)
```

### 3. Document Your Handlers

Add metadata to make handlers discoverable:

```go
eng.RegisterWithMetadata("search_products_v2", handler, &engine.LogicHandlerInfo{
    Description: "Searches products using Elasticsearch with ML ranking",
    Version:     "2.0.0",
})
```

### 4. Version Your Logic

When updating logic, version it:

```go
// Old version (deprecated)
eng.MustRegisterLogic("search_products_v1", oldHandler)

// New version
eng.MustRegisterLogic("search_products_v2", newHandler)
```

Then use task overrides to gradually migrate:

```yaml
flows:
  - name: product_search
    tasks:
      - name: search
        logic: search_products_v1
        overrides:
          - condition:
              tag: "beta"
            action: replace
            task: search_v2
```

### 5. Test Logic Handlers

Write unit tests for your logic handlers:

```go
func TestParseParams(t *testing.T) {
    // Create test context
    ctx := &types.ExecutionContext{
        Parameters: map[string]any{
            "query": "test",
        },
        Logger: logger.New("debug", false),
    }
    
    // Call handler
    outputs, err := parseAndValidateParams(ctx, ctx.Parameters, nil)
    
    // Assert
    assert.NoError(t, err)
    assert.Equal(t, "test", outputs["parsed_query"])
}
```

## Troubleshooting

### Issue: "Logic handler not found" error

**Symptom:** Task execution fails with error like:
```
Logic handler 'my_logic' not found
```

**Solution:**
1. Check the logic name in your task YAML matches exactly (case-sensitive)
2. Verify you registered the handler before starting the server
3. Run validation to see all missing handlers:
   ```go
   errors := eng.ValidateLogicHandlers()
   ```

### Issue: UI shows "Not implemented"

**Symptom:** The Web UI shows logic handlers as "Not implemented" even though they're registered.

**Explanation:** The standalone UI command (`jigsaw ui web`) creates a fresh engine without your custom logic handlers. It can only see the configuration, not the runtime registration.

**Solutions:**

**Option 1:** Use the validation API from your running server:
```bash
# Your server is running on port 8080
curl http://localhost:8080/api/_validate/logic
```

**Option 2:** Add validation to your application startup (recommended):
```go
func main() {
    // ... setup ...
    
    // Validate before starting
    errors := eng.ValidateLogicHandlers()
    if len(errors) > 0 {
        // Print errors and exit
        os.Exit(1)
    }
    
    // Start server
    srv.Start(8080, "./configs")
}
```

**Option 3:** Create a validation script:
```bash
#!/bin/bash
# validate.sh
go run main.go validate
```

### Issue: Validation passes but execution fails

**Symptom:** Validation shows all handlers registered, but task execution fails.

**Possible Causes:**
1. Handler is registered but has bugs
2. Handler expects different input format than task provides
3. Provider connection issues

**Solution:** Add logging inside your handlers:
```go
eng.MustRegisterLogic("my_logic", func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
    ctx.Logger.Debug("Handler called", map[string]any{
        "inputs": inputs,
    })
    
    // ... implementation ...
    
    ctx.Logger.Debug("Handler completed", map[string]any{
        "outputs": outputs,
    })
    
    return outputs, nil
})
```

## Example: Complete Validated Application

See `examples/jig-test/main.go` for a complete example with validation.

## Summary

- ✅ Always validate logic handlers during startup
- ✅ Use the HTTP API to check handler status
- ✅ Add metadata to handlers for documentation
- ✅ Version your logic handlers
- ✅ Test handlers independently
- ✅ Integrate validation into CI/CD

For more information:
- [Getting Started](./GETTING_STARTED.md)
- [Architecture](./ARCHITECTURE.md)
- [Versioning](./VERSIONING.md)
