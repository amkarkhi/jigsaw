# Jigsaw Examples

This directory contains complete, working examples showing how to use Jigsaw in your projects.

## 📁 Examples

### 1. Simple Example (`simple/`)
**Basic programmatic usage of Jigsaw**

Shows how to:
- Load configuration
- Create engine
- Register logic handlers
- Execute flows programmatically

```bash
cd examples/simple
go run main.go
```

### 2. Server Example (`server/`) ⭐ **RECOMMENDED**
**Complete HTTP server with all logic handlers registered**

Shows how to:
- Set up HTTP server
- Register ALL logic handlers
- Create provider registry
- Handle graceful shutdown
- Use with real HTTP requests

```bash
cd examples/server
go run main.go
```

Then test with:
```bash
# Test search endpoint
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{"sub":1,"query":"golang","limit":10}'

# Health check
curl http://localhost:8080/health
```

### 3. Search Service Example (`search-service/`)
**Template for building a search service**

Shows how to:
- Structure a real application
- Organize logic handlers
- Implement providers
- Use Jigsaw as a package

```bash
cd examples/search-service
go run main.go
```

## 🚀 Quick Start

### Run the Server Example (Easiest)

```bash
# From jigsaw root directory
go run ./examples/server/main.go
```

You'll see:
```
🚀 Jigsaw Server Started!
   URL: http://localhost:8080

📡 Try these commands:
   curl -X POST http://localhost:8080/api/search \
     -H 'Content-Type: application/json' \
     -d '{"sub":1,"query":"golang","limit":10}'

   curl http://localhost:8080/health

🎨 Open Web UI:
   jigsaw ui web --config ../../configs --port 3000

   Press Ctrl+C to stop
```

### Test the API

```bash
# Basic search (sub=1)
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{"sub":1,"query":"golang frameworks","limit":10}'

# Cached search (sub=2)
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{"sub":2,"query":"golang","limit":5}'

# Advanced search (sub=3)
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{"sub":3,"query":"golang"}'

# With tag override (premium cache)
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{"sub":2,"tag":"premium","query":"golang"}'

# With tag override (no cache)
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{"sub":2,"tag":"no-cache","query":"golang"}'
```

## 📚 What Each Example Teaches

### Simple Example
**Best for**: Understanding the basics

- ✅ Minimal setup
- ✅ Direct flow execution
- ✅ No HTTP server
- ✅ Good for testing

### Server Example ⭐
**Best for**: Production use

- ✅ Complete HTTP server
- ✅ All logic handlers registered
- ✅ Provider registry setup
- ✅ Graceful shutdown
- ✅ Ready to customize

### Search Service Example
**Best for**: Real applications

- ✅ Structured project layout
- ✅ Organized code
- ✅ Provider implementations
- ✅ Production patterns

## 🔧 Customizing the Examples

### Add Your Own Logic

In `jig-test/main.go`, find `registerLogicHandlers()`:

```go
// Define a struct with LogicMeta() and Run(...) methods.
type MyLogic struct{}

func (MyLogic) LogicMeta() engine.LogicMeta {
    return engine.LogicMeta{Name: "my_custom_logic", Version: "1.0.0"}
}

type myIn struct{ Query string `json:"query"` }
type myOut struct{ Result string `json:"result"` }
type myParams struct{}

func (MyLogic) Run(_ *types.ExecutionContext, in myIn, _ myParams) (myOut, error) {
    return myOut{Result: "processed: " + in.Query}, nil
}

func registerLogicHandlers(eng *engine.Engine, log zerolog.Logger) {
    engine.MustRegister(eng, MyLogic{})
}
```

### Add Your Own Provider

In `server/main.go`, modify `createProviderRegistry()`:

```go
func createProviderRegistry(cfg *types.Config, log types.Logger) *provider.Registry {
    providerReg := provider.NewRegistry(log)
    
    // Register configured providers
    for _, prov := range cfg.Providers {
        providerReg.RegisterConfig(prov)
    }
    
    // Add YOUR custom provider
    // myProvider := &MyCustomProvider{...}
    // providerReg.Register("my_provider", myProvider)
    
    return providerReg
}
```

## 🎯 Common Use Cases

### Use Case 1: Test Flow Execution

```bash
# Run simple example
cd examples/simple
go run main.go
```

### Use Case 2: Run HTTP Server

```bash
# Run server example
cd examples/server
go run main.go

# In another terminal, test it
curl -X POST http://localhost:8080/api/search \
  -d '{"sub":1,"query":"test"}'
```

### Use Case 3: Check Configuration

```bash
# From jigsaw root
jigsaw ui web --config ./configs --port 3000

# Open http://localhost:3000
# See all flows, tasks, and implementation status
```

### Use Case 4: Validate Before Running

```bash
# Validate configuration
jigsaw validate --config ./configs

# List flows
jigsaw list flows --config ./configs

# Then run server
go run ./examples/server/main.go
```

## 🐛 Troubleshooting

### "Logic handler not found"

**Problem**: Task logic not registered

**Solution**: Add logic handler in `registerLogicHandlers()`:
```go
engine.MustRegister(eng, YourLogicStruct{})
```

### "Provider not found"

**Problem**: Provider not registered

**Solution**: Check `createProviderRegistry()` and ensure provider is configured in YAML

### "Required input not found"

**Problem**: Task needs input that wasn't provided

**Solution**: Check flow configuration and ensure previous tasks provide required outputs

### "Flow not found"

**Problem**: Flow name doesn't match configuration

**Solution**: Check `configs/flows/` for correct flow name

## 📖 Next Steps

1. **Run server example**: `go run ./examples/server/main.go`
2. **Test with curl**: Use the curl commands above
3. **Check Web UI**: `jigsaw ui web --config ./configs`
4. **Customize logic**: Edit `registerLogicHandlers()`
5. **Add providers**: Implement YOUR cache, database, search engines, etc.
6. **Create flows**: Add YOUR workflows in `configs/flows/`

## 💡 Tips

### Development
- Use `jigsaw ui tui` for quick config checks
- Use `jigsaw validate` before running
- Check logs for detailed execution info
- Use `--pretty` flag for readable logs

### Production
- Implement actual provider connections
- Add error handling in logic handlers
- Use connection pooling for providers
- Monitor with structured logs
- Use hot-reload for config updates

### Testing
- Test flows with `jigsaw test flow`
- Use simple example for unit testing
- Mock providers for testing
- Validate configs in CI/CD

## 🔗 Related Documentation

- [Package Usage Guide](../docs/PACKAGE_USAGE.md)
- [UI Guide](../docs/UI_GUIDE.md)
- [Getting Started](../docs/GETTING_STARTED.md)
- [Architecture](../docs/ARCHITECTURE.md)

## ✅ Checklist

Before deploying:
- [ ] All logic handlers registered
- [ ] Provider connections implemented
- [ ] Configuration validated
- [ ] Flows tested
- [ ] Error handling added
- [ ] Logging configured
- [ ] Graceful shutdown tested

---

**The server example is production-ready** - just add YOUR actual provider implementations and logic! 🚀
