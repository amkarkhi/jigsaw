# Changelog

All notable changes to the Jigsaw project.

## [Unreleased]

### Changed
- **BREAKING**: Moved `validator` package from `internal/validator` to `pkg/validator`
  - **Reason**: The validator package needs to be accessible to external projects using Jigsaw as a library
  - **Migration**: Update imports from `github.com/amkarkhi/jigsaw/internal/validator` to `github.com/amkarkhi/jigsaw/pkg/validator`
  - **Impact**: External projects can now properly import and use the validator

### Added
- `docs/EXTERNAL_USAGE.md` - Comprehensive guide for using Jigsaw as an external package
- Examples showing how to use Jigsaw in other Go projects

## [0.1.0] - Initial Release

### Added
- Core task orchestration engine
- Configuration-driven workflow system
- YAML-based configuration for tasks, flows, providers, and endpoints
- Sub-based flow routing
- Tag-based task overrides
- Multiple fallback strategies (abort, continue, switch_task, switch_provider)
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
- See `docs/EXTERNAL_USAGE.md` for comprehensive usage examples
