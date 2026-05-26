package dashboard

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/configlang"
	"github.com/amkarkhi/jigsaw/pkg/symbols"
	"github.com/amkarkhi/jigsaw/pkg/types"
)

// routes wires the API + static SPA handlers onto the dashboard's mux.
func (d *Dashboard) routes() {
	d.mux.HandleFunc("/api/overview", d.handleOverview)
	d.mux.HandleFunc("/api/flows", d.handleFlows)
	d.mux.HandleFunc("/api/tasks", d.handleTasks)
	d.mux.HandleFunc("/api/providers", d.handleProviders)
	d.mux.HandleFunc("/api/endpoints", d.handleEndpoints)
	d.mux.HandleFunc("/api/logic", d.handleLogic)
	d.mux.HandleFunc("/api/diagnostics", d.handleDiagnostics)
	d.mux.HandleFunc("/api/info", d.handleInfo)
	// Phase 6+: editing.
	d.mux.HandleFunc("/api/tree", d.handleTree)
	d.mux.HandleFunc("/api/file", d.handleFile)
	d.mux.HandleFunc("/api/files", d.handleSave)
	d.mux.HandleFunc("/api/bundle", d.handleBundle)
	d.mux.HandleFunc("/api/flow-location", d.handleFlowLocation)
	d.mux.HandleFunc("/api/task-location", d.handleTaskLocation)
	d.mux.HandleFunc("/api/endpoint-location", d.handleEndpointLocation)
	d.mux.HandleFunc("/api/task-usage", d.handleTaskUsage)
	d.mux.HandleFunc("/api/layout", d.handleLayout)
	// Auth endpoints — these need to bypass the auth middleware so the user
	// can log in. See withAuth for the bypass logic.
	d.mux.HandleFunc("/api/login", d.handleLogin)
	d.mux.HandleFunc("/api/logout", d.handleLogout)
	d.mux.HandleFunc("/api/me", d.handleMe)
	d.mux.HandleFunc("/api/auth-info", d.handleAuthInfo)
	d.mux.HandleFunc("/auth/gitlab/login", d.handleGitLabLogin)
	d.mux.HandleFunc("/auth/gitlab/callback", d.handleGitLabCallback)
	d.mux.HandleFunc("/api/git/settings", d.handleGitSettings)
	d.mux.HandleFunc("/api/git/push", d.handleGitPush)
	d.mux.HandleFunc("/api/users", d.handleUsers)
	d.mux.HandleFunc("/api/users/", d.handleUser)
	d.mux.HandleFunc("/api/playground/run", d.handlePlaygroundRun)
	d.mux.HandleFunc("/api/playground/task", d.handlePlaygroundTask)
	d.mux.HandleFunc("/api/playground/logic", d.handlePlaygroundLogic)

	// Everything else falls through to the static SPA. Routing inside the SPA
	// is handled client-side.
	d.mux.Handle("/", staticHandler())
}

// loadConfig is called fresh on every read. The config tree is small enough
// that re-loading on each API hit is cheap, and it sidesteps cache-coherence
// concerns when files change outside the dashboard.
func (d *Dashboard) loadConfig() (*types.Config, error) {
	loader := config.NewLoader(d.opts.Logger)
	return loader.Load(d.opts.ConfigPath)
}

func (d *Dashboard) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"mode":         d.opts.Mode.String(),
		"edit":         d.opts.Edit,
		"config_path":  d.opts.ConfigPath,
		"server_name":  "jigsaw-dashboard",
		"service_name": d.opts.ServiceName,
		"playground":   d.opts.Playground,
	})
}

func (d *Dashboard) handleOverview(w http.ResponseWriter, r *http.Request) {
	cfg, err := d.loadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	specs := d.loadLogicSpecs()
	unimplemented := 0
	known := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		known[s.Name] = struct{}{}
	}
	for _, t := range cfg.Tasks {
		if t.Logic == "" {
			continue
		}
		if _, ok := known[t.Logic]; !ok {
			unimplemented++
		}
	}
	writeJSON(w, map[string]any{
		"flows":               len(cfg.Flows),
		"tasks":               len(cfg.Tasks),
		"providers":           len(cfg.Providers),
		"endpoints":           len(cfg.Endpoints),
		"logic_handlers":      len(specs),
		"unimplemented_logic": unimplemented,
		"manifest_loaded":     specs != nil,
	})
}

func (d *Dashboard) handleFlows(w http.ResponseWriter, r *http.Request) {
	cfg, err := d.loadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	if name := r.URL.Query().Get("name"); name != "" {
		flow, ok := cfg.Flows[name]
		if !ok {
			http.Error(w, "flow not found", http.StatusNotFound)
			return
		}
		writeJSON(w, flow)
		return
	}
	out := make([]map[string]any, 0, len(cfg.Flows))
	for name, flow := range cfg.Flows {
		out = append(out, map[string]any{
			"name":        name,
			"description": flow.Description,
			"version":     flow.Version,
			"inherits":    flow.Inherits,
			"task_count":  len(flow.Tasks),
		})
	}
	writeJSON(w, out)
}

func (d *Dashboard) handleTasks(w http.ResponseWriter, r *http.Request) {
	cfg, err := d.loadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	specs := d.loadLogicSpecs()
	known := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		known[s.Name] = struct{}{}
	}
	if name := r.URL.Query().Get("name"); name != "" {
		task, ok := cfg.Tasks[name]
		if !ok {
			http.Error(w, "task not found", http.StatusNotFound)
			return
		}
		_, implemented := known[task.Logic]
		writeJSON(w, map[string]any{
			"task":              task,
			"logic_implemented": implemented,
		})
		return
	}
	out := make([]map[string]any, 0, len(cfg.Tasks))
	for name, task := range cfg.Tasks {
		_, implemented := known[task.Logic]
		entry := map[string]any{
			"name":              name,
			"description":       task.Description,
			"logic":             task.Logic,
			"logic_implemented": implemented || task.Logic == "",
			"provider":          task.Provider,
			"params":            len(task.Params),
			"inherits":          task.Inherits,
		}
		// Task-level wrapper: surface the wrapper task name so the UI can
		// render this task as wrapped by another (dashed container with the
		// wrapper's name on top, the wrapped task underneath).
		if task.Wrapper != nil && task.Wrapper.Task != "" {
			entry["wrapped_by"] = task.Wrapper.Task
		}
		out = append(out, entry)
	}
	writeJSON(w, out)
}

func (d *Dashboard) handleProviders(w http.ResponseWriter, r *http.Request) {
	cfg, err := d.loadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	if name := r.URL.Query().Get("name"); name != "" {
		prov, ok := cfg.Providers[name]
		if !ok {
			http.Error(w, "provider not found", http.StatusNotFound)
			return
		}
		// Find tasks that reference this provider.
		users := make([]string, 0)
		for taskName, t := range cfg.Tasks {
			if t.Provider == name {
				users = append(users, taskName)
			}
		}
		writeJSON(w, map[string]any{
			"provider": prov,
			"used_by":  users,
		})
		return
	}
	out := make([]map[string]any, 0, len(cfg.Providers))
	for name, prov := range cfg.Providers {
		// task-count summary for the index view.
		users := 0
		for _, t := range cfg.Tasks {
			if t.Provider == name {
				users++
			}
		}
		out = append(out, map[string]any{
			"name":       name,
			"type":       prov.Type,
			"version":    prov.Version,
			"init_mode":  prov.InitMode,
			"pool_size":  prov.PoolSize,
			"user_count": users,
		})
	}
	writeJSON(w, out)
}

func (d *Dashboard) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	cfg, err := d.loadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(cfg.Endpoints))
	for name, ep := range cfg.Endpoints {
		flows := make([]map[string]any, 0, len(ep.Flows))
		for _, m := range ep.Flows {
			flows = append(flows, map[string]any{"sub": m.Sub, "flow": m.FlowName})
		}
		out = append(out, map[string]any{
			"name":           name,
			"path":           ep.Path,
			"method":         ep.Method,
			"description":    ep.Description,
			"flows":          flows,
			"request_params": ep.RequestParams,
		})
	}
	writeJSON(w, out)
}

func (d *Dashboard) handleLogic(w http.ResponseWriter, r *http.Request) {
	specs := d.loadLogicSpecs()
	if specs == nil {
		writeJSON(w, map[string]any{
			"manifest_loaded": false,
			"handlers":        []any{},
		})
		return
	}
	cfg, err := d.loadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	usage := make(map[string][]string)
	for taskName, t := range cfg.Tasks {
		if t.Logic == "" {
			continue
		}
		usage[t.Logic] = append(usage[t.Logic], taskName)
	}
	out := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		out = append(out, map[string]any{
			"name":             s.Name,
			"description":      s.Description,
			"version":          s.Version,
			"input_schema":     s.InputSchema,
			"output_schema":    s.OutputSchema,
			"params_schema":    s.ParamsSchema,
			"skippable_inputs": s.SkippableInputs,
			"used_by":          usage[s.Name],
		})
	}
	writeJSON(w, map[string]any{
		"manifest_loaded": true,
		"handlers":        out,
	})
}

// handleTaskUsage returns the list of flows that reference a given task name.
// Walks every flow's task list and parallel branches recursively.
func (d *Dashboard) handleTaskUsage(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing ?name=", http.StatusBadRequest)
		return
	}
	cfg, err := d.loadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	hits := make([]string, 0)
	for flowName, f := range cfg.Flows {
		if flowReferencesTask(f.Tasks, name) {
			hits = append(hits, flowName)
		}
	}
	writeJSON(w, hits)
}

func flowReferencesTask(refs []types.TaskRef, target string) bool {
	for _, r := range refs {
		if r.Name == target {
			return true
		}
		if r.Parallel != nil {
			for _, b := range r.Parallel.Branches {
				if flowReferencesTask(b.Tasks, target) {
					return true
				}
			}
		}
	}
	return false
}

func (d *Dashboard) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	cfg, err := d.loadConfig()
	if err != nil {
		writeJSON(w, []configlang.Diagnostic{{
			Severity: configlang.SeverityError,
			Message:  err.Error(),
		}})
		return
	}
	opts := configlang.CheckOptions{}
	if specs := d.loadLogicSpecs(); specs != nil {
		opts.LogicRegistry = specs
		opts.RegistryProvided = true
	}
	diags := configlang.Check(cfg, opts)
	if diags == nil {
		diags = []configlang.Diagnostic{}
	}
	writeJSON(w, diags)
}

// loadLogicSpecs returns the parsed manifest's specs, or nil if no manifest.
// nil signals "no signal" — UI distinguishes this from "empty but authoritative."
func (d *Dashboard) loadLogicSpecs() []configlang.LogicSpec {
	m, err := symbols.Read(filepath.Join(d.opts.ConfigPath, symbols.DefaultManifestPath))
	if err != nil || m == nil {
		return nil
	}
	specs := make([]configlang.LogicSpec, len(m.Logic))
	for i, l := range m.Logic {
		specs[i] = configlang.LogicSpec{
			Name:            l.Name,
			Description:     l.Description,
			Version:         l.Version,
			InputSchema:     l.InputSchema,
			OutputSchema:    l.OutputSchema,
			ParamsSchema:    l.ParamsSchema,
			SkippableInputs: l.SkippableInputs,
		}
	}
	return specs
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
