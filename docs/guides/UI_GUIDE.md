# Jigsaw UI Guide

Jigsaw provides two user interfaces to help you manage and configure your workflows:

1. **TUI (Terminal User Interface)** - Interactive terminal-based UI
2. **Web UI** - Browser-based graphical interface

## 🖥️ Terminal UI (TUI)

### Features

- ✅ Interactive terminal interface
- ✅ Browse flows, tasks, providers, and endpoints
- ✅ View logic registry status
- ✅ Real-time configuration overview
- ✅ Keyboard navigation
- ✅ No browser required

### Launch TUI

```bash
jigsaw ui tui --config ./configs
```

### Navigation

- **Tab / Shift+Tab**: Switch between tabs
- **↑ / ↓ or k / j**: Navigate items
- **q or Ctrl+C**: Quit

### Tabs

1. **📋 Flows** - View all configured flows
2. **⚙️ Tasks** - Browse tasks and their configurations
3. **🔌 Providers** - See provider configurations
4. **🌐 Endpoints** - View HTTP endpoints and mappings
5. **🔧 Logic Registry** - Check registered logic handlers
6. **📊 Overview** - Configuration summary and status

### Screenshots

```
┌─────────────────────────────────────────────────────┐
│  🧩 Jigsaw Configuration Manager                    │
├─────────────────────────────────────────────────────┤
│  [Flows] [Tasks] [Providers] [Endpoints] [Logic]   │
├─────────────────────────────────────────────────────┤
│                                                     │
│  📋 Flows (4 total)                                 │
│                                                     │
│  ▶ basic_search                                     │
│    Description: Basic search flow without caching   │
│    Tasks: 4                                         │
│                                                     │
│    cached_search                                    │
│    advanced_search                                  │
│    parallel_search                                  │
│                                                     │
└─────────────────────────────────────────────────────┘
```

## 🌐 Web UI

### Features

- ✅ Modern browser-based interface
- ✅ Beautiful, responsive design
- ✅ Auto-refresh every 5 seconds
- ✅ Detailed configuration views
- ✅ Status indicators
- ✅ Easy to share with team

### Launch Web UI

```bash
jigsaw ui web --config ./configs --port 3000
```

Then open your browser to: **http://localhost:3000**

### Tabs

1. **📊 Overview** - Dashboard with statistics and status
2. **📋 Flows** - List all flows with descriptions
3. **⚙️ Tasks** - View tasks with implementation status
4. **🔌 Providers** - Provider configurations
5. **🌐 Endpoints** - HTTP endpoints and flow mappings
6. **🔧 Logic Registry** - Registered logic handlers

### Features

#### Overview Dashboard
- Configuration statistics (flows, tasks, providers, endpoints)
- Status indicators (configured, implemented, warnings)
- Quick health check

#### Flow View
- Flow name and description
- Number of tasks
- Inheritance information
- Task list

#### Task View
- Task name and description
- Logic handler name
- Implementation status (✓ implemented / ⚠️ not implemented)
- Provider information
- Input/output counts
- Inheritance details

#### Provider View
- Provider name and type
- Initialization mode (lazy, eager, pooled)
- Pool size (if applicable)

#### Endpoint View
- HTTP method and path
- Description
- Flow mappings (sub → flow)

#### Logic Registry
- All registered logic handlers
- Implementation status
- Which tasks use each handler
- Warnings for unimplemented handlers

### API Endpoints

The Web UI exposes a REST API:

```bash
# Overview
GET /api/overview

# Flows
GET /api/flows
GET /api/flows/:name

# Tasks
GET /api/tasks
GET /api/tasks/:name

# Providers
GET /api/providers

# Endpoints
GET /api/endpoints

# Logic Registry
GET /api/logic

# Health Check
GET /api/health
```

## Use Cases

### For Developers

**Use TUI when:**
- Working in terminal-only environments
- Quick configuration checks
- SSH sessions
- Lightweight browsing

**Use Web UI when:**
- Detailed configuration review
- Sharing with team members
- Documentation/screenshots
- Monitoring configuration status

### For Clients/Users

**Web UI is ideal for:**
- Non-technical users
- Configuration review
- Understanding workflow structure
- Verifying implementation status

## Configuration Status Indicators

Both UIs show configuration status:

### ✓ Green (OK)
- Flows configured
- Tasks configured
- Logic handlers registered
- All logic implemented

### ⚠️ Yellow (Warning)
- No flows configured
- No tasks configured
- Logic handlers not registered
- Some logic not implemented

### ❌ Red (Error)
- Configuration validation failed
- Critical issues

## Examples

### Check Configuration Status

```bash
# Quick TUI check
jigsaw ui tui --config ./configs

# Web UI for detailed view
jigsaw ui web --config ./configs --port 3000
```

### Share Configuration with Team

```bash
# Start Web UI on accessible port
jigsaw ui web --config ./configs --port 8080

# Share URL with team
# http://your-server:8080
```

### Monitor Implementation Progress

```bash
# Launch Web UI
jigsaw ui web --config ./configs

# Navigate to "Logic Registry" tab
# See which logic handlers are implemented
# Auto-refreshes every 5 seconds
```

## Integration with Development

### During Development

```go
// In your application
eng := engine.New(cfg, val, logger)

// Register logic handlers
eng.MustRegisterLogic("my_logic", myHandler)

// Check in UI
// jigsaw ui web --config ./configs
// Navigate to "Logic Registry" to see registered handlers
```

### Before Deployment

1. Run Web UI: `jigsaw ui web --config ./configs`
2. Check Overview tab for warnings
3. Verify all logic handlers are implemented
4. Review flow configurations
5. Validate endpoint mappings

## Tips

### TUI Tips

- Use `Tab` to quickly switch between sections
- Press `j/k` for vim-style navigation
- Selected items show detailed information
- Quit anytime with `q`

### Web UI Tips

- Auto-refreshes show real-time updates
- Use browser dev tools to inspect API responses
- Bookmark the URL for quick access
- Share URL with team for collaboration

### Both UIs

- Run alongside your application
- Use for configuration validation
- Check implementation status
- Monitor logic registry
- Verify flow mappings

## Troubleshooting

### TUI Issues

**Terminal too small:**
- Resize terminal window
- Some content may be truncated

**Colors not showing:**
- Check terminal supports colors
- Try different terminal emulator

### Web UI Issues

**Port already in use:**
```bash
# Use different port
jigsaw ui web --port 3001
```

**Can't access from other machines:**
- Web UI binds to localhost by default
- For remote access, consider SSH tunneling

**Auto-refresh not working:**
- Check browser console for errors
- Refresh page manually

## Command Reference

```bash
# TUI
jigsaw ui tui [flags]
  --config string   Path to configuration directory (default "./configs")
  --log-level string   Log level (default "info")

# Web UI
jigsaw ui web [flags]
  --config string   Path to configuration directory (default "./configs")
  --port int        Web UI port (default 3000)
  --log-level string   Log level (default "info")
  --pretty          Pretty print logs
```

## Next Steps

- Launch TUI: `jigsaw ui tui`
- Launch Web UI: `jigsaw ui web`
- Check configuration status
- Verify logic implementation
- Share with your team

---

**Both UIs are read-only** - they help you visualize and understand your configuration, but don't modify files. Edit YAML files directly for configuration changes.
