package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// ---- shared test types -------------------------------------------------------

type regTestInputs struct {
	Name string `json:"name"`
}

type regTestOutputs struct {
	Greeting string `json:"greeting"`
}

type regTestParams struct {
	Prefix string `json:"prefix"`
}

// ---- Logic[I,O,P] implementation -------------------------------------------

type greetLogic struct{}

func (greetLogic) LogicMeta() LogicMeta {
	return LogicMeta{
		Name:        "greet",
		Description: "Says hello",
		Version:     "1.0.0",
	}
}

func (greetLogic) Run(_ *types.ExecutionContext, in regTestInputs, p regTestParams) (*regTestOutputs, error) {
	return &regTestOutputs{Greeting: p.Prefix + in.Name}, nil
}

// ---- ProviderLogic[I,O,P] implementation ------------------------------------

type greetWithProviderLogic struct{}

func (greetWithProviderLogic) LogicMeta() LogicMeta {
	return LogicMeta{Name: "greet_with_provider", Version: "1.0.0"}
}

func (greetWithProviderLogic) Run(_ *types.ExecutionContext, in regTestInputs, p regTestParams, _ types.ProviderInstance) (*regTestOutputs, error) {
	return &regTestOutputs{Greeting: "prov:" + p.Prefix + in.Name}, nil
}

// ---- helpers ----------------------------------------------------------------

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := &types.Config{
		Tasks: map[string]*types.Task{},
		Flows: map[string]*types.Flow{},
	}
	return New(cfg, nil, zerolog.Nop())
}

func newTestExecCtx() *types.ExecutionContext {
	ctx := jigsawctx.New(context.Background(), "test", 0, nil, nil)
	ctx = jigsawctx.WithProviders(ctx, nullProviders{})
	ctx = jigsawctx.WithLogger(ctx, zerolog.Nop())
	return ctx
}

// ---- tests ------------------------------------------------------------------

// TestRegisterLogic registers a Logic[I,O,P] struct, executes it through the
// registry, and checks the output map and schema presence.
func TestRegisterLogic(t *testing.T) {
	eng := newTestEngine(t)
	if err := Register(eng, greetLogic{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	h, err := eng.logicRegistry.get("greet")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if h.Meta().Description != "Says hello" {
		t.Errorf("Description = %q, want %q", h.Meta().Description, "Says hello")
	}
	if h.Meta().Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", h.Meta().Version, "1.0.0")
	}
	if h.InputSchema() == nil {
		t.Error("InputSchema should not be nil")
	}
	if h.OutputSchema() == nil {
		t.Error("OutputSchema should not be nil")
	}
	if h.ParamsSchema() == nil {
		t.Error("ParamsSchema should not be nil")
	}

	ctx := newTestExecCtx()
	out, err := h.Execute(ctx,
		map[string]any{"name": "World"},
		map[string]any{"prefix": "Hello, "},
		nil,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["greeting"] != "Hello, World" {
		t.Errorf("greeting = %v, want %q", out["greeting"], "Hello, World")
	}
}

// TestRegisterProviderLogic registers a ProviderLogic[I,O,P] struct and
// verifies it round-trips through Execute with a nil provider.
func TestRegisterProviderLogic(t *testing.T) {
	eng := newTestEngine(t)
	if err := RegisterWithProvider(eng, greetWithProviderLogic{}); err != nil {
		t.Fatalf("RegisterWithProvider: %v", err)
	}

	h, err := eng.logicRegistry.get("greet_with_provider")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	ctx := newTestExecCtx()
	out, err := h.Execute(ctx,
		map[string]any{"name": "Alice"},
		map[string]any{"prefix": "Hi "},
		nil,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["greeting"] != "prov:Hi Alice" {
		t.Errorf("greeting = %v, want %q", out["greeting"], "prov:Hi Alice")
	}
}

// TestRegisterRejectsEmptyName verifies that a logic returning an empty
// LogicMeta.Name produces an error at registration time.
func TestRegisterRejectsEmptyName(t *testing.T) {
	type emptyNameLogic struct{}
	// emptyNameLogic cannot implement Logic[I,O,P] directly because the
	// interface requires a concrete Run method — we verify the guard inside
	// Register instead by constructing a minimal wrapper.
	eng := newTestEngine(t)
	err := Register(eng, emptyNameWrap{})
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
	if !strings.Contains(err.Error(), "Name must not be empty") {
		t.Errorf("error = %q, want substring %q", err.Error(), "Name must not be empty")
	}
}

// emptyNameWrap implements Logic[regTestInputs, regTestOutputs, regTestParams]
// but returns an empty name.
type emptyNameWrap struct{}

func (emptyNameWrap) LogicMeta() LogicMeta { return LogicMeta{Name: ""} }
func (emptyNameWrap) Run(_ *types.ExecutionContext, _ regTestInputs, _ regTestParams) (*regTestOutputs, error) {
	return &regTestOutputs{}, nil
}

// TestRegisterRejectsNilOutput verifies that a logic returning (nil, nil) from
// Run causes Execute to produce an explicit error.
func TestRegisterRejectsNilOutput(t *testing.T) {
	eng := newTestEngine(t)
	if err := Register(eng, nilOutputWrap{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	h, err := eng.logicRegistry.get("nil_output_wrap")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	ctx := newTestExecCtx()
	_, err = h.Execute(ctx, map[string]any{}, map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error for nil output without error, got nil")
	}
	if !strings.Contains(err.Error(), "nil output without error") {
		t.Errorf("error = %q, want substring %q", err.Error(), "nil output without error")
	}
}

type nilOutputWrap struct{}

func (nilOutputWrap) LogicMeta() LogicMeta { return LogicMeta{Name: "nil_output_wrap"} }
func (nilOutputWrap) Run(_ *types.ExecutionContext, _ regTestInputs, _ regTestParams) (*regTestOutputs, error) {
	return nil, nil
}

// TestRegisterLogicFlowRoundTrip wires greetLogic into a real flow and
// executes it end-to-end through the engine.
func TestRegisterLogicFlowRoundTrip(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"greet_task": {Name: "greet_task", Logic: "greet"},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{
				{Name: "greet_task", Bind: &types.Bind{In: map[string]string{"name": "name"}}},
			}},
		},
	}

	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		MustRegister(e, greetLogic{})
	})

	execCtx.ScopePut("name", types.ScopedVar{Value: "Bob", Type: "string"})

	if _, err := eng.executor.Execute(execCtx, cfg.Flows["f"]); err != nil {
		t.Fatalf("flow failed: %v", err)
	}

	sv, ok := execCtx.ScopeGet("greeting")
	if !ok {
		t.Fatal("greeting not in scope after flow")
	}
	if sv.Value != "Bob" {
		t.Errorf("greeting = %v, want %q", sv.Value, "Bob")
	}
}

// TestMustRegisterPanicsOnDuplicate verifies the Must-variant panics on
// duplicate registration.
func TestMustRegisterPanicsOnDuplicate(t *testing.T) {
	eng := newTestEngine(t)
	MustRegister(eng, greetLogic{})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration, got none")
		}
	}()
	MustRegister(eng, greetLogic{})
}

// ---- error message for emptyNameWrap with provider variant -----------------

func TestRegisterWithProviderRejectsEmptyName(t *testing.T) {
	eng := newTestEngine(t)
	err := RegisterWithProvider(eng, emptyProviderNameWrap{})
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
	if !strings.Contains(err.Error(), "Name must not be empty") {
		t.Errorf("error = %q, want substring %q", err.Error(), "Name must not be empty")
	}
}

type emptyProviderNameWrap struct{}

func (emptyProviderNameWrap) LogicMeta() LogicMeta { return LogicMeta{Name: ""} }
func (emptyProviderNameWrap) Run(_ *types.ExecutionContext, _ regTestInputs, _ regTestParams, _ types.ProviderInstance) (*regTestOutputs, error) {
	return &regTestOutputs{}, nil
}

// TestRegisterProviderLogicNilOutput verifies providerTypedHandler nil-output guard.
func TestRegisterProviderLogicNilOutput(t *testing.T) {
	eng := newTestEngine(t)
	if err := RegisterWithProvider(eng, nilProviderOutputWrap{}); err != nil {
		t.Fatalf("RegisterWithProvider: %v", err)
	}

	h, err := eng.logicRegistry.get("nil_prov_output")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	ctx := newTestExecCtx()
	_, err = h.Execute(ctx, map[string]any{}, map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error for nil output without error, got nil")
	}
	if !strings.Contains(err.Error(), "nil output without error") {
		t.Errorf("error = %q, want substring %q", err.Error(), "nil output without error")
	}
}

type nilProviderOutputWrap struct{}

func (nilProviderOutputWrap) LogicMeta() LogicMeta { return LogicMeta{Name: "nil_prov_output"} }
func (nilProviderOutputWrap) Run(_ *types.ExecutionContext, _ regTestInputs, _ regTestParams, _ types.ProviderInstance) (*regTestOutputs, error) {
	return nil, nil
}

// TestRegisterErrorPropagates verifies that errors returned from Run propagate
// through Execute unchanged.
func TestRegisterErrorPropagates(t *testing.T) {
	eng := newTestEngine(t)
	if err := Register(eng, errorLogic{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	h, _ := eng.logicRegistry.get("error_logic")
	ctx := newTestExecCtx()
	_, err := h.Execute(ctx, map[string]any{}, map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "intentional error") {
		t.Errorf("error = %q, want %q", err.Error(), "intentional error")
	}
}

type errorLogic struct{}

func (errorLogic) LogicMeta() LogicMeta { return LogicMeta{Name: "error_logic"} }
func (errorLogic) Run(_ *types.ExecutionContext, _ regTestInputs, _ regTestParams) (*regTestOutputs, error) {
	return nil, fmt.Errorf("intentional error")
}
