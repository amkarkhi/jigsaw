package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/config"
	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// Playground — runs a flow against user-supplied inputs in a sandbox so the
// editor can inspect per-task data and pinpoint failures.
//
// IMPORTANT: this is a dry-run sandbox, not the real engine. The dashboard
// binary doesn't know about your application's logic handlers (those are
// registered by your own service binary). When a task's logic handler is
// missing, the engine's built-in fallback echoes the inputs back as outputs
// with a `_logic_not_implemented` marker — which is exactly what you want
// for tracing data shapes through a flow without touching real backends.
//
// Provider lookups are answered by a stub registry so flows referencing
// providers don't fail at lookup time; the stub doesn't perform any I/O.

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

	// Load and validate the current config tree. We reload on every run so
	// the playground always reflects what's on disk right now (including
	// unsaved edits the user has just persisted).
	loader := config.NewLoader(d.opts.Logger)
	cfg, err := loader.Load(d.opts.ConfigPath)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "config load: " + err.Error()})
		return
	}
	if err := validator.New(d.opts.Logger).ValidateConfig(cfg); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "config invalid: " + err.Error()})
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

	// Build a fresh flow executor. Logic registry stays empty: any task
	// whose logic isn't registered will hit the engine's "echo inputs as
	// outputs" fallback, which is the desired dry-run behaviour.
	flowExec, traces, err := runPlaygroundFlow(r.Context(), cfg, flow, body.Inputs, body.Headers, body.Sub, d.opts.Logger)

	resp := map[string]any{
		"ok":         err == nil,
		"flow":       body.Flow,
		"tasks":      traces,
		"request_id": flowExec.Context.RequestID,
		"status":     string(flowExec.Status),
	}
	if err != nil {
		resp["error"] = err.Error()
	} else if flowExec.Context != nil {
		// Collect all scope vars as the result.
		snapshot := make(map[string]any, len(flowExec.Context.Scope))
		for k, sv := range flowExec.Context.Scope {
			snapshot[k] = sv.Value
		}
		resp["result"] = snapshot
	}
	writeJSON(w, resp)
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

// runPlaygroundFlow wires up the engine in dry-run mode, executes the flow,
// and produces an ordered trace per task. The logic registry is intentionally
// empty — the engine's built-in "echo inputs" fallback gives us the dry-run
// behaviour we want.
func runPlaygroundFlow(
	ctx context.Context,
	cfg *types.Config,
	flow *types.Flow,
	inputs map[string]any,
	headers map[string]string,
	sub int,
	logger zerolog.Logger,
) (*types.FlowExecution, []taskTrace, error) {
	execCtx := jigsawctx.New(ctx, flow.Name, sub, inputs, headers)
	execCtx = jigsawctx.WithProviders(execCtx, newStubProviderRegistry(cfg))
	execCtx = jigsawctx.WithLogger(execCtx, logger)

	exec := engine.NewDryRunFlowExecutor(cfg, logger)
	flowExec, err := exec.Execute(execCtx, flow)
	traces := toTaskTraces(flowExec)
	return flowExec, traces, err
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
func (s *stubProviderRegistry) Close() error                                       { return nil }

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
