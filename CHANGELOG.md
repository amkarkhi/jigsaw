# Changelog

All notable changes to the Jigsaw project.

## [Unreleased]

### Added
- **Generic Task Wrappers** - Define reusable wrappers at the task level for cross-cutting concerns (caching, metrics, logging, rate limiting). See [docs/reference/WRAPPER_PATTERN.md](docs/reference/WRAPPER_PATTERN.md)
  - New `wrapper` field on Task configuration
  - Wrappers inherit task's I/O schema (transparent I/O)
  - Wrapper receives `ctx.Nested` pointing to wrapped task
  - Web UI support for viewing and editing wrapper configuration

### Changed — logic-handler refactor (BREAKING)
- `LogicHandler` is now an interface (`Meta() LogicMeta`, `InputSchema()`, `OutputSchema()`, `ParamsSchema()`, `Execute`). The old function-typed handler is gone.
- Registration: use `eng.Register(MyLogic{})` (reflection path) or `engine.RegisterTyped[I,O,P](eng, name, fn)` (generic closure path). `MustRegisterLogic(name, fn)` is removed.
- Task YAML: `inputs:` and `outputs:` fields are removed. Schemas are derived from the handler's typed `Run` structs. Added `params:` for static per-task handler parameters.
- `TaskRef.Bind` is now a nested struct with `in:` and `out:` sub-maps (was a flat map with `bind:`/`as:`).
- Parallel branch outputs are namespaced as `<branch_label>.<key>` in the flat scope; downstream tasks read them via `bind.in`.
- Static validation: `eng.ValidateFlows()` must be called after all handlers are registered.
- `Fallback.TargetTask` field removed; `switch_task` strategy removed (was never implemented).
- `types.Validator` interface now only requires `ValidateConfig`; `ValidateInputs`/`ValidateOutputs` removed.

### Changed — previous entries
- **BREAKING**: Moved `validator` package from `internal/validator` to `pkg/validator`

### Added
- `docs/guides/EXTERNAL_USAGE.md` - Guide for using Jigsaw as an external package
- `pkg/symbols` — manifest format for CLI tooling (symbols.json, schema version 2)
- `pkg/lsp` — LSP server for editor diagnostics

## [0.1.0] - Initial Release

### Added
- Core task orchestration engine
- Configuration-driven workflow system
- YAML-based configuration for tasks, flows, providers, and endpoints
- Sub-based flow routing
- Tag-based task overrides
- Multiple fallback strategies (abort, continue, switch_provider)
- Task and flow inheritance
- Provider abstraction with lazy/eager/pooled initialization
- Hot-reload configuration support
- Gin HTTP server
- Cobra CLI with commands:
  - `serve` - Start HTTP server
  - `validate` - Validate configurations
  - `list` - List resources
  - `describe` - Describe resources
  - `test` - Test flow execution
- Zerolog structured logging
- Comprehensive documentation
- Example configurations and applications

### Package Structure
```
github.com/amkarkhi/jigsaw/
├── pkg/
│   ├── config/      - Configuration loading
│   ├── context/     - Execution context
│   ├── engine/      - Flow and task execution
│   ├── logger/      - Logging
│   ├── provider/    - Provider management
│   ├── router/      - Flow routing
│   ├── server/      - HTTP server
│   ├── types/       - Core types
│   └── validator/   - Configuration validation ⭐ NOW PUBLIC
└── internal/
    └── loader/      - Internal utilities
```

## Migration Guide

### From internal/validator to pkg/validator

**Before:**
```go
import "github.com/amkarkhi/jigsaw/internal/validator"  // ❌ Not accessible externally
```

**After:**
```go
import "github.com/amkarkhi/jigsaw/pkg/validator"  // ✅ Public and accessible
```

### Example Usage

```go
package main

import (
    "github.com/amkarkhi/jigsaw/pkg/config"
    "github.com/amkarkhi/jigsaw/pkg/engine"
    "github.com/amkarkhi/jigsaw/pkg/logger"
    "github.com/amkarkhi/jigsaw/pkg/validator"  // ✅ Now accessible
)

func main() {
    logger := logger.New("info", true)
    loader := config.NewLoader(logger)
    cfg, _ := loader.Load("./configs")
    
    // Validator is now accessible from external projects
    val := validator.New(logger)
    if err := val.ValidateConfig(cfg); err != nil {
        panic(err)
    }
    
    eng := engine.New(cfg, val, logger)
    // ... use engine
}
```

## Notes

- All packages under `pkg/` are public and can be imported by external projects
- The `internal/` directory contains private implementation details
- Configuration validation is essential before executing flows
- See `docs/guides/EXTERNAL_USAGE.md` for comprehensive usage examples
