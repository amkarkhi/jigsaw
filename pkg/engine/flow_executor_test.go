package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
)

// nullProviders satisfies types.ProviderRegistry for tests that don't use
// providers. None of the test tasks declare a provider, so Get must never be
// called by the executor.
type nullProviders struct{}

func (nullProviders) Get(string) (types.ProviderInstance, error) {
	return nil, errors.New("no providers registered")
}
func (nullProviders) Register(string, types.ProviderInstance) error { return nil }
func (nullProviders) Close() error                                  { return nil }

// ---- minimal Logic[I,O,P] wrappers for tests --------------------------------

// sleepIO / sleepLogic replace the old LogicHandler-based sleepHandler.
type sleepIO struct{}
type sleepOut struct {
	Value string `json:"value"`
}
type sleepP struct{}

type sleepLogic struct {
	name  string
	d     time.Duration
	value string
}

func (s sleepLogic) LogicMeta() LogicMeta { return LogicMeta{Name: s.name} }
func (s sleepLogic) Run(ctx *types.ExecutionContext, _ sleepIO, _ sleepP) (*sleepOut, error) {
	select {
	case <-time.After(s.d):
		return &sleepOut{Value: s.value}, nil
	case <-ctx.Context.Done():
		return nil, ctx.Context.Err()
	}
}

// funcIO / funcLogic replace the old LogicHandler-based funcHandler. The
// function receives raw maps so tests that need fine-grained control can
// bypass typed I/O by injecting scope values and reading raw outputs.
type funcRawIO struct {
	// Values passed to the function come from scope via bind; the struct
	// carries whatever keys the test scoped in. We use map[string]any
	// round-trips to pass arbitrary data through.
	Data map[string]any `json:"data,omitempty"`
}
type funcRawOut struct {
	Data map[string]any `json:"data,omitempty"`
}
type funcRawP struct{}

// rawFuncLogic wires an arbitrary function as a Logic implementation. The
// function receives/returns map[string]any; the typed wrappers handle
// JSON round-trips automatically, but since we need full control we implement
// logicHandler directly for these specific test helpers.
type rawFuncHandler struct {
	name string
	fn   func(ctx *types.ExecutionContext, inputs, params map[string]any, p types.ProviderInstance) (map[string]any, error)
}

func (h *rawFuncHandler) Meta() LogicMeta                  { return LogicMeta{Name: h.name} }
func (h *rawFuncHandler) InputSchema() *jsonschema.Schema  { return nil }
func (h *rawFuncHandler) OutputSchema() *jsonschema.Schema { return nil }
func (h *rawFuncHandler) ParamsSchema() *jsonschema.Schema { return nil }
func (h *rawFuncHandler) Execute(ctx *types.ExecutionContext, inputs, params map[string]any, p types.ProviderInstance) (map[string]any, error) {
	return h.fn(ctx, inputs, params, p)
}

// registerRawFunc is an internal helper that registers a rawFuncHandler
// directly into the engine's registry for tests that need arbitrary map I/O.
func registerRawFunc(e *Engine, name string, fn func(*types.ExecutionContext, map[string]any, map[string]any, types.ProviderInstance) (map[string]any, error)) {
	if err := e.logicRegistry.register(&rawFuncHandler{name: name, fn: fn}); err != nil {
		panic(err)
	}
}

func newEngine(t *testing.T, cfg *types.Config, register func(*Engine)) (*Engine, *types.ExecutionContext) {
	t.Helper()
	log := zerolog.Nop()
	eng := New(cfg, nil, log)
	register(eng)
	execCtx := jigsawctx.New(context.Background(), "test", 0, nil, nil)
	execCtx = jigsawctx.WithProviders(execCtx, nullProviders{})
	execCtx = jigsawctx.WithLogger(execCtx, log)
	return eng, execCtx
}

// TestParallelBranchesRunConcurrently asserts that two branches each sleeping
// 100ms complete in well under their summed duration.
func TestParallelBranchesRunConcurrently(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"left":  {Name: "left", Logic: "sleep_left"},
			"right": {Name: "right", Logic: "sleep_right"},
		},
		Flows: map[string]*types.Flow{
			"f": {
				Name: "f",
				Tasks: []types.TaskRef{
					{Parallel: &types.ParallelBlock{
						Branches: []types.Branch{
							{Label: "L", Tasks: []types.TaskRef{{Name: "left"}}},
							{Label: "R", Tasks: []types.TaskRef{{Name: "right"}}},
						},
					}},
				},
			},
		},
	}

	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		MustRegister(e, sleepLogic{name: "sleep_left", d: 100 * time.Millisecond, value: "L"})
		MustRegister(e, sleepLogic{name: "sleep_right", d: 100 * time.Millisecond, value: "R"})
	})

	start := time.Now()
	_, err := eng.executor.Execute(execCtx, cfg.Flows["f"])
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("flow failed: %v", err)
	}
	// Generous upper bound: 100ms sleep each, parallel should finish in ~100ms.
	if elapsed > 180*time.Millisecond {
		t.Fatalf("expected parallel branches to finish in ~100ms, took %v", elapsed)
	}
}

// TestParallelBranchScopeNamespacing verifies that after a parallel block each
// branch's outputs are accessible in the parent scope under
// "<branch_label>.<key>" and not directly.
func TestParallelBranchScopeNamespacing(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"producer":  {Name: "producer", Logic: "produce"},
			"collector": {Name: "collector", Logic: "collect"},
		},
		Flows: map[string]*types.Flow{
			"f": {
				Name: "f",
				Tasks: []types.TaskRef{
					{Parallel: &types.ParallelBlock{
						Branches: []types.Branch{
							{Label: "L", Tasks: []types.TaskRef{{Name: "producer"}}},
							{Label: "R", Tasks: []types.TaskRef{{Name: "producer"}}},
						},
					}},
					// collector uses bind.in to pull branch-namespaced outputs
					{Name: "collector",
						Bind: &types.Bind{In: map[string]string{"left_value": "L.value", "right_value": "R.value"}}},
				},
			},
		},
	}

	collected := make(chan map[string]any, 1)

	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		registerRawFunc(e, "produce", func(ctx *types.ExecutionContext, _, _ map[string]any, _ types.ProviderInstance) (map[string]any, error) {
			tag := "main"
			if len(ctx.BranchPath) > 0 {
				tag = ctx.BranchPath[len(ctx.BranchPath)-1]
			}
			return map[string]any{"value": "from-" + tag}, nil
		})
		registerRawFunc(e, "collect", func(_ *types.ExecutionContext, inputs, _ map[string]any, _ types.ProviderInstance) (map[string]any, error) {
			collected <- inputs
			return inputs, nil
		})
	})

	if _, err := eng.executor.Execute(execCtx, cfg.Flows["f"]); err != nil {
		t.Fatalf("flow failed: %v", err)
	}

	select {
	case got := <-collected:
		if got["left_value"] != "from-L" {
			t.Errorf("left_value = %v, want from-L", got["left_value"])
		}
		if got["right_value"] != "from-R" {
			t.Errorf("right_value = %v, want from-R", got["right_value"])
		}
	case <-time.After(time.Second):
		t.Fatal("collector task never ran")
	}
}

// TestParallelCancelOnFailure verifies that on_branch_failure: cancel aborts
// in-flight sibling branches via context cancellation.
func TestParallelCancelOnFailure(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"boom":    {Name: "boom", Logic: "boom"},
			"sleeper": {Name: "sleeper", Logic: "sleeper"},
		},
		Flows: map[string]*types.Flow{
			"f": {
				Name: "f",
				Tasks: []types.TaskRef{
					{Parallel: &types.ParallelBlock{
						OnBranchFailure: "cancel",
						Branches: []types.Branch{
							{Label: "F", Tasks: []types.TaskRef{{Name: "boom"}}},
							{Label: "S", Tasks: []types.TaskRef{{Name: "sleeper"}}},
						},
					}},
				},
			},
		},
	}

	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		registerRawFunc(e, "boom", func(*types.ExecutionContext, map[string]any, map[string]any, types.ProviderInstance) (map[string]any, error) {
			return nil, fmt.Errorf("boom")
		})
		MustRegister(e, sleepLogic{name: "sleeper", d: 2 * time.Second, value: "S"})
	})

	start := time.Now()
	_, err := eng.executor.Execute(execCtx, cfg.Flows["f"])
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected flow failure when cancel-on-failure is set")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("cancellation did not abort sleeper; flow took %v", elapsed)
	}
}

// TestRegisterTypedRoundTrip tests that Register correctly reflects schemas
// and round-trips input/output via JSON.
func TestRegisterTypedRoundTrip(t *testing.T) {
	type inputs struct {
		Q string `json:"Q"`
	}
	type outputs struct {
		ParsedQuery string `json:"parsed_query"`
	}
	type params struct{}

	type parseLogic struct{}
	// parseLogic needs to be defined at function scope; we use an adapter
	// that satisfies Logic[inputs, outputs, params].
	type parseLogicImpl struct{}

	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"parse": {Name: "parse", Logic: "parse_q"},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{{Name: "parse", Bind: &types.Bind{In: map[string]string{"Q": "Q"}}}}},
		},
	}

	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		MustRegister(e, parseQueryLogic{})
	})

	// Seed scope with the request parameter.
	execCtx.ScopePut("Q", types.ScopedVar{Value: "hello", Type: "string"})

	_, err := eng.executor.Execute(execCtx, cfg.Flows["f"])
	if err != nil {
		t.Fatalf("flow failed: %v", err)
	}

	sv, ok := execCtx.ScopeGet("parsed_query")
	if !ok {
		t.Fatal("parsed_query not in scope after flow")
	}
	if sv.Value != "hello" {
		t.Errorf("parsed_query = %v, want hello", sv.Value)
	}

	info, err := eng.GetLogicHandlerInfo("parse_q")
	if err != nil {
		t.Fatalf("GetLogicHandlerInfo: %v", err)
	}
	if info.Description != "parse query" {
		t.Errorf("description = %q", info.Description)
	}
	if info.Version != "1.0.0" {
		t.Errorf("version = %q", info.Version)
	}
	if info.InputSchema == nil {
		t.Error("InputSchema should not be nil")
	}
	if info.OutputSchema == nil {
		t.Error("OutputSchema should not be nil")
	}
}

// parseQueryLogic is a package-level struct used by TestRegisterTypedRoundTrip.
type parseQueryInputs struct {
	Q string `json:"Q"`
}
type parseQueryOutputs struct {
	ParsedQuery string `json:"parsed_query"`
}
type parseQueryParams struct{}

type parseQueryLogic struct{}

func (parseQueryLogic) LogicMeta() LogicMeta {
	return LogicMeta{Name: "parse_q", Description: "parse query", Version: "1.0.0"}
}
func (parseQueryLogic) Run(_ *types.ExecutionContext, in parseQueryInputs, _ parseQueryParams) (*parseQueryOutputs, error) {
	if in.Q == "" {
		return nil, fmt.Errorf("Q required")
	}
	return &parseQueryOutputs{ParsedQuery: in.Q}, nil
}

// TestScopeBindAs tests that bind.in and bind.out correctly rename inputs and outputs.
func TestScopeBindAs(t *testing.T) {
	type myInputs struct {
		Query string `json:"query"`
	}
	type myOutputs struct {
		Result string `json:"result"`
	}
	type noParams struct{}

	type workerLogic struct{}

	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"worker": {Name: "worker", Logic: "worker_logic"},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{
				{
					Name: "worker",
					Bind: &types.Bind{
						In:  map[string]string{"query": "search_query"},
						Out: map[string]string{"result": "final_result"},
					},
				},
			}},
		},
	}

	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		MustRegister(e, workerLogicImpl{})
	})

	execCtx.ScopePut("search_query", types.ScopedVar{Value: "test", Type: "string"})

	_, err := eng.executor.Execute(execCtx, cfg.Flows["f"])
	if err != nil {
		t.Fatalf("flow failed: %v", err)
	}

	// "result" should NOT be in scope (it was renamed to "final_result").
	if _, ok := execCtx.ScopeGet("result"); ok {
		t.Error("original output name 'result' should not be in scope after bind.out rename")
	}

	sv, ok := execCtx.ScopeGet("final_result")
	if !ok {
		t.Fatal("final_result not in scope")
	}
	if sv.Value != "processed:test" {
		t.Errorf("final_result = %v, want processed:test", sv.Value)
	}
}

type workerInputs struct {
	Query string `json:"query"`
}
type workerOutputs struct {
	Result string `json:"result"`
}
type workerNoParams struct{}

type workerLogicImpl struct{}

func (workerLogicImpl) LogicMeta() LogicMeta { return LogicMeta{Name: "worker_logic"} }
func (workerLogicImpl) Run(_ *types.ExecutionContext, in workerInputs, _ workerNoParams) (*workerOutputs, error) {
	return &workerOutputs{Result: "processed:" + in.Query}, nil
}

// TestValidatorCatchesMissingBindSource verifies that ValidateFlows errors when
// a bind source key is not in the simulated scope at that point in the flow.
func TestValidatorCatchesMissingBindSource(t *testing.T) {
	type myIn struct {
		X string `json:"x"`
	}
	type myOut struct{}
	type noP struct{}

	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"t": {Name: "t", Logic: "logic_t"},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{
				{Name: "t", Bind: &types.Bind{In: map[string]string{"x": "nonexistent_source"}}},
			}},
		},
	}

	log := zerolog.Nop()
	eng := New(cfg, nil, log)
	MustRegister(eng, validatorTestLogic{})

	if err := eng.ValidateFlows(); err == nil {
		t.Error("expected error for missing bind source, got nil")
	}
}

type validatorIn struct {
	X string `json:"x"`
}
type validatorOut struct{}
type validatorP struct{}

type validatorTestLogic struct{}

func (validatorTestLogic) LogicMeta() LogicMeta { return LogicMeta{Name: "logic_t"} }
func (validatorTestLogic) Run(_ *types.ExecutionContext, _ validatorIn, _ validatorP) (*validatorOut, error) {
	return &validatorOut{}, nil
}

// TestBindNestedInOutParsesAndExecutes round-trips a TaskRef with both
// bind.in and bind.out through the config loader then executes it.
func TestBindNestedInOutParsesAndExecutes(t *testing.T) {
	const flowYAML = `
flows:
  - name: f
    tasks:
      - name: worker
        bind:
          in:
            query: raw_query
          out:
            result: final
`
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "flows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flows", "f.yml"), []byte(flowYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	log := zerolog.Nop()
	loader := config.NewLoader(log)
	cfg, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}

	flow, ok := cfg.Flows["f"]
	if !ok {
		t.Fatal("flow 'f' not found after load")
	}
	if len(flow.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(flow.Tasks))
	}
	ref := flow.Tasks[0]
	if ref.Bind == nil {
		t.Fatal("Bind is nil after load")
	}
	if ref.Bind.In["query"] != "raw_query" {
		t.Errorf("bind.in[query] = %q, want raw_query", ref.Bind.In["query"])
	}
	if ref.Bind.Out["result"] != "final" {
		t.Errorf("bind.out[result] = %q, want final", ref.Bind.Out["result"])
	}

	// Inject the task definition so the engine can find it.
	cfg.Tasks = map[string]*types.Task{
		"worker": {Name: "worker", Logic: "wl"},
	}

	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		MustRegister(e, bindTestLogic{})
	})

	execCtx.ScopePut("raw_query", types.ScopedVar{Value: "hello", Type: "string"})

	if _, err := eng.executor.Execute(execCtx, flow); err != nil {
		t.Fatalf("flow failed: %v", err)
	}

	if _, inScope := execCtx.ScopeGet("result"); inScope {
		t.Error("raw output key 'result' should not be in scope; bind.out should rename it")
	}
	sv, ok := execCtx.ScopeGet("final")
	if !ok {
		t.Fatal("'final' not in scope after flow")
	}
	if sv.Value != "got:hello" {
		t.Errorf("final = %v, want got:hello", sv.Value)
	}
}

type bindTestIn struct {
	Query string `json:"query"`
}
type bindTestOut struct {
	Result string `json:"result"`
}
type bindTestP struct{}

type bindTestLogic struct{}

func (bindTestLogic) LogicMeta() LogicMeta { return LogicMeta{Name: "wl"} }
func (bindTestLogic) Run(_ *types.ExecutionContext, in bindTestIn, _ bindTestP) (*bindTestOut, error) {
	return &bindTestOut{Result: "got:" + in.Query}, nil
}
