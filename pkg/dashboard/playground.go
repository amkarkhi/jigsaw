package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/config"
	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	"gopkg.in/yaml.v3"
)

// Playground — runs a flow (or single task, or single logic) against
// user-supplied inputs so the editor can inspect per-task data and pinpoint
// failures.
//
// Two execution modes, chosen via Options.PlaygroundMode:
//   - "real":    dispatch against the host engine's registered logic
//                handlers (Options.Engine must be set). What runs here is
//                what the live service runs. Wrapper logics work — the
//                playground sets execCtx.Engine so ctx.Engine.InvokeTask
//                resolves.
//   - "dry_run": empty logic registry; the engine's "_logic_not_implemented"
//                fallback echoes inputs as outputs. Useful for shape checks
//                in a binary that doesn't carry handlers (the standalone
//                dashboard).
//
// Provider lookups default to a stub registry that performs no I/O, so
// flows can be exercised offline even when real providers are configured.
// Hosts that want real I/O can pass Options.PlaygroundProviders.

// POST /api/playground/run — body: {flow: string, inputs: map[string]any}
// Returns: {ok, status, error?, tasks: [TaskTrace], result: any, request_id}
func (d *Dashboard) handlePlaygroundRun(w http.ResponseWriter, r *http.Request) {
	if !d.opts.Playground {
		http.Error(w, "playground is disabled on this server", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.opts.Mode == ModeServer && d.opts.Auth != nil {
		if _, err := d.opts.Auth.Authenticate(r); err != nil {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
	}

	var body struct {
		Flow     string            `json:"flow"`
		FlowYAML string            `json:"flow_yaml"` // optional ad-hoc flow (custom playground)
		Inputs   map[string]any    `json:"inputs"`
		Sub      int               `json:"sub"`
		Headers  map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Flow == "" && body.FlowYAML == "" {
		http.Error(w, "either flow or flow_yaml is required", http.StatusBadRequest)
		return
	}

	cfg, err := d.playgroundConfig()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// Resolve the flow to run: either a saved one (by name) or an ad-hoc
	// one parsed from the request body (custom playground flows).
	var flow *types.Flow
	if body.FlowYAML != "" {
		f, err := parsePlaygroundFlowYAML(body.FlowYAML)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "flow_yaml: " + err.Error()})
			return
		}
		flow = f
	} else {
		var ok bool
		flow, ok = cfg.Flows[body.Flow]
		if !ok {
			writeJSON(w, map[string]any{"ok": false, "error": fmt.Sprintf("flow %q not found", body.Flow)})
			return
		}
	}

	flowExec, traces, runErr := d.runPlaygroundFlow(r.Context(), cfg, flow, body.Inputs, body.Headers, body.Sub)
	writePlaygroundResp(w, flowExec, traces, runErr, body.Flow)
}

// playgroundConfig returns the config the playground should use. In real
// mode we use the host engine's config directly so what runs in the
// playground is exactly what the live service is serving. In dry-run mode
// we reload from disk so the user sees the current on-disk state (which
// may include unsaved edits not yet picked up by any live engine).
func (d *Dashboard) playgroundConfig() (*types.Config, error) {
	if d.opts.PlaygroundModeReal() {
		return d.opts.Engine.Config(), nil
	}
	loader := config.NewLoader(d.opts.Logger)
	cfg, err := loader.Load(d.opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}
	if err := validator.New(d.opts.Logger).ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("config invalid: %w", err)
	}
	return cfg, nil
}

// parsePlaygroundFlowYAML turns a `{flows: [{...}]}` YAML doc into a single
// *types.Flow. We don't validate the flow against the task registry here —
// any unresolved task will surface as a clear runtime error in the trace,
// which is more useful for the user than a precondition rejection.
func parsePlaygroundFlowYAML(raw string) (*types.Flow, error) {
	var doc struct {
		Flows []*types.Flow `yaml:"flows"`
	}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	if len(doc.Flows) == 0 {
		return nil, fmt.Errorf("no flow entries in yaml")
	}
	f := doc.Flows[0]
	if f.Name == "" {
		f.Name = "playground"
	}
	return f, nil
}

// runPlaygroundFlow executes a flow in the playground sandbox and returns
// per-task traces. In real mode (host engine wired in via Options.Engine)
// the engine's registered handlers run; ctx.Engine is set so wrapper logics
// can use InvokeTask. In dry-run mode, the empty registry triggers the
// engine's "echo inputs as outputs" fallback — useful for shape checks.
//
// Providers default to a stub registry so flows can be exercised offline
// even when real providers are configured. Hosts that want real I/O can
// inject their own registry via Options.PlaygroundProviders.
func (d *Dashboard) runPlaygroundFlow(
	ctx context.Context,
	cfg *types.Config,
	flow *types.Flow,
	inputs map[string]any,
	headers map[string]string,
	sub int,
) (*types.FlowExecution, []taskTrace, error) {
	execCtx := jigsawctx.New(ctx, flow.Name, sub, inputs, headers)

	providers := d.opts.PlaygroundProviders
	if providers == nil {
		providers = newStubProviderRegistry(cfg)
	}
	execCtx = jigsawctx.WithProviders(execCtx, providers)
	execCtx = jigsawctx.WithLogger(execCtx, d.opts.Logger)

	var exec *engine.FlowExecutor
	if d.opts.PlaygroundModeReal() {
		exec = d.opts.Engine.FlowExecutorFor(cfg)
		// Wrapper logics call ctx.Engine.InvokeTask to dispatch the wrapped
		// task. Without this, every wrapped task in the playground would
		// fail with a nil-engine panic.
		execCtx.Engine = d.opts.Engine
	} else {
		exec = engine.NewDryRunFlowExecutor(cfg, d.opts.Logger)
	}

	flowExec, err := exec.Execute(execCtx, flow)
	traces := toTaskTraces(flowExec)
	return flowExec, traces, err
}

// POST /api/playground/task — body: {task, inputs, headers, params, sub}
// Wraps the named task in a synthetic single-task flow and runs it through
// the dry-run executor. Useful for testing one task's input/output shape
// without typing out a flow YAML. `params` are forwarded as task-level
// overrides via the synthetic ref.
func (d *Dashboard) handlePlaygroundTask(w http.ResponseWriter, r *http.Request) {
	if !d.opts.Playground {
		http.Error(w, "playground is disabled on this server", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.opts.Mode == ModeServer && d.opts.Auth != nil {
		if _, err := d.opts.Auth.Authenticate(r); err != nil {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
	}

	var body struct {
		Task    string            `json:"task"`
		Inputs  map[string]any    `json:"inputs"`
		Headers map[string]string `json:"headers"`
		Params  map[string]any    `json:"params"`
		Sub     int               `json:"sub"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Task == "" {
		http.Error(w, "task is required", http.StatusBadRequest)
		return
	}

	cfg, err := d.playgroundConfig()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if _, ok := cfg.Tasks[body.Task]; !ok {
		writeJSON(w, map[string]any{"ok": false, "error": fmt.Sprintf("task %q not found", body.Task)})
		return
	}

	flow := &types.Flow{
		Name:        "__playground_task__",
		Description: "ad-hoc single-task playground",
		Tasks: []types.TaskRef{
			{Name: body.Task, Params: body.Params},
		},
	}

	flowExec, traces, runErr := d.runPlaygroundFlow(r.Context(), cfg, flow, body.Inputs, body.Headers, body.Sub)
	writePlaygroundResp(w, flowExec, traces, runErr, "")
}

// POST /api/playground/logic — body: {logic, inputs, headers, params, sub}
// Builds a synthetic task that wraps the named logic and runs it through the
// dry-run executor. If the dashboard binary doesn't have this logic
// registered (the common case), the engine's "echo inputs as outputs"
// fallback fires — useful for shape-checking the logic's contract.
func (d *Dashboard) handlePlaygroundLogic(w http.ResponseWriter, r *http.Request) {
	if !d.opts.Playground {
		http.Error(w, "playground is disabled on this server", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.opts.Mode == ModeServer && d.opts.Auth != nil {
		if _, err := d.opts.Auth.Authenticate(r); err != nil {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
	}

	var body struct {
		Logic   string            `json:"logic"`
		Inputs  map[string]any    `json:"inputs"`
		Headers map[string]string `json:"headers"`
		Params  map[string]any    `json:"params"`
		Sub     int               `json:"sub"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Logic == "" {
		http.Error(w, "logic is required", http.StatusBadRequest)
		return
	}

	base, err := d.playgroundConfig()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// Clone the config so injecting our synthetic task doesn't mutate the
	// host engine's live config (which is shared with running flow
	// executions and must not change underneath them). Only the Tasks map
	// needs a fresh copy — every other field is read-only here.
	cfg := *base
	cfg.Tasks = make(map[string]*types.Task, len(base.Tasks)+1)
	maps.Copy(cfg.Tasks, base.Tasks)
	const synthName = "__playground_logic__"
	cfg.Tasks[synthName] = &types.Task{
		Name:        synthName,
		Description: "synthetic playground task for logic " + body.Logic,
		Logic:       body.Logic,
		Params:      body.Params,
	}

	flow := &types.Flow{
		Name:        "__playground_logic__",
		Description: "ad-hoc single-logic playground",
		Tasks:       []types.TaskRef{{Name: synthName}},
	}

	flowExec, traces, runErr := d.runPlaygroundFlow(r.Context(), &cfg, flow, body.Inputs, body.Headers, body.Sub)
	writePlaygroundResp(w, flowExec, traces, runErr, "")
}

// writePlaygroundResp emits the common response shape for any of the
// playground endpoints.
func writePlaygroundResp(w http.ResponseWriter, flowExec *types.FlowExecution, traces []taskTrace, runErr error, flowName string) {
	resp := map[string]any{
		"ok":    runErr == nil,
		"flow":  flowName,
		"tasks": traces,
	}
	if flowExec != nil {
		resp["request_id"] = flowExec.Context.RequestID
		resp["status"] = string(flowExec.Status)
	}
	if runErr != nil {
		resp["error"] = runErr.Error()
	} else if flowExec != nil && flowExec.Context != nil {
		resp["result"] = playgroundResultData(flowExec)
	}
	writeJSON(w, resp)
}

// playgroundResultData mirrors engine.ExecuteFlow's narrowing: when the
// flow ends in a plain task, the response is just that task's outputs
// (matching the HTTP body curl receives). Otherwise we fall back to the
// full scope snapshot so the user still sees something.
func playgroundResultData(flowExec *types.FlowExecution) map[string]any {
	if flowExec == nil || flowExec.Context == nil {
		return nil
	}
	if flow := flowExec.Flow; flow != nil {
		flowExec.Context.Logger.Debug().Str("flowName", flow.Name).Msg("playgroundResultData: flowExec has flow")
		for i := len(flow.Tasks) - 1; i >= 0; i-- {
			name := flow.Tasks[i].Name
			flowExec.Context.Logger.Trace().Str("taskName", name).Msg("playgroundResultData: checking task ref")
			if name == "" {
				continue
			}
			for _, te := range flowExec.Tasks {
				tn := ""
				if te.Task != nil {
					tn = te.Task.Name
				}
				flowExec.Context.Logger.Trace().Str("taskName", tn).Bool("outputsNil", te.Outputs == nil).Msg("playgroundResultData: checking exec task")
				if te.Task != nil && te.Task.Name == name && te.Outputs != nil {
					return te.Outputs
				}
			}
			break
		}
	} else {
		flowExec.Context.Logger.Debug().Msg("playgroundResultData: flowExec has nil flow")
	}
	snapshot := make(map[string]any, len(flowExec.Context.Scope))
	for k, sv := range flowExec.Context.Scope {
		snapshot[k] = sv.Value
	}
	return snapshot
}

// ---- traces ---------------------------------------------------------------

type taskTrace struct {
	Name        string         `json:"name"`
	Label       string         `json:"label,omitempty"`
	Status      string         `json:"status"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	DurationMs  int64          `json:"duration_ms"`
	Inputs      map[string]any `json:"inputs"`
	Params      map[string]any `json:"params,omitempty"`
	Outputs     map[string]any `json:"outputs"`
	Error       string         `json:"error,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	Logic       string         `json:"logic,omitempty"`
	Fallback    bool           `json:"fallback_used,omitempty"`
	Skipped     bool           `json:"skipped,omitempty"`
}

func toTaskTraces(flowExec *types.FlowExecution) []taskTrace {
	if flowExec == nil || len(flowExec.Tasks) == 0 {
		return []taskTrace{}
	}
	out := make([]taskTrace, 0, len(flowExec.Tasks))
	for _, te := range flowExec.Tasks {
		t := taskTrace{
			Name:        te.Task.Name,
			Status:      string(te.Status),
			StartedAt:   te.StartedAt,
			CompletedAt: te.CompletedAt,
			Inputs:      te.Inputs,
			Params:      te.Params,
			Outputs:     te.Outputs,
			Fallback:    te.FallbackUsed,
			Skipped:     te.Skipped,
		}
		if te.ActualTask != nil {
			t.Provider = te.ActualTask.Provider
			t.Logic = te.ActualTask.Logic
		}
		if te.Error != nil {
			t.Error = te.Error.Error()
		}
		if te.CompletedAt != nil {
			t.DurationMs = te.CompletedAt.Sub(te.StartedAt).Milliseconds()
		}
		out = append(out, t)
	}
	// Stable order: by StartedAt so the UI shows tasks in execution order
	// even when the underlying slice was appended from parallel branches.
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// ---- stub providers -------------------------------------------------------

// stubProviderRegistry satisfies types.ProviderRegistry. Get returns a
// stubProvider for any name the running flow references; no network or
// disk I/O happens. Useful for the playground; not for production traffic.
type stubProviderRegistry struct {
	cfg *types.Config
}

func newStubProviderRegistry(cfg *types.Config) *stubProviderRegistry {
	return &stubProviderRegistry{cfg: cfg}
}

func (s *stubProviderRegistry) Get(name string) (types.ProviderInstance, error) {
	p, ok := s.cfg.Providers[name]
	if !ok {
		p = &types.Provider{Name: name, Type: "stub"}
	}
	return &stubProvider{provider: p, connected: true}, nil
}

func (s *stubProviderRegistry) Register(_ string, _ types.ProviderInstance) error { return nil }
func (s *stubProviderRegistry) Close() error                                      { return nil }

// stubProvider implements types.ProviderInstance with no-op everything.
type stubProvider struct {
	provider  *types.Provider
	connected bool
}

func (s *stubProvider) Connect(_ context.Context) error    { s.connected = true; return nil }
func (s *stubProvider) Disconnect(_ context.Context) error { s.connected = false; return nil }
func (s *stubProvider) IsConnected() bool                  { return s.connected }
func (s *stubProvider) GetConnection() any                 { return nil }
func (s *stubProvider) GetProvider() *types.Provider       { return s.provider }
