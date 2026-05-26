package engine

import (
	"fmt"
	"testing"

	"github.com/amkarkhi/jigsaw/pkg/types"
)

// innerInputs / innerOutputs / innerParams stand in for any registered logic
// the wrapper might dispatch to via Engine.Invoke.
type innerInputs struct {
	Name string `json:"name"`
}

type innerOutputs struct {
	Greeting string `json:"greeting"`
}

type innerParams struct {
	Prefix string `json:"prefix"`
}

type innerLogic struct{}

func (innerLogic) LogicMeta() LogicMeta { return LogicMeta{Name: "inner_greet"} }

func (innerLogic) Run(_ *types.ExecutionContext, in innerInputs, p innerParams) (*innerOutputs, error) {
	return &innerOutputs{Greeting: p.Prefix + in.Name}, nil
}

// wrapperLogic verifies that a handler can reach the engine via
// ctx.Engine.Invoke and dispatch another registered logic by name.
type wrapperInputs struct {
	Name string `json:"name"`
}
type wrapperOutputs struct {
	Forwarded map[string]any `json:"forwarded"`
}
type wrapperParams struct {
	Inner string `json:"inner"`
}

type wrapperLogic struct{}

func (wrapperLogic) LogicMeta() LogicMeta { return LogicMeta{Name: "wrapper"} }

func (wrapperLogic) Run(ctx *types.ExecutionContext, in wrapperInputs, p wrapperParams) (*wrapperOutputs, error) {
	if ctx.Engine == nil {
		return nil, fmt.Errorf("ctx.Engine is nil")
	}
	out, err := ctx.Engine.Invoke(ctx, p.Inner,
		map[string]any{"name": in.Name},
		map[string]any{"prefix": "hi-"},
		nil,
	)
	if err != nil {
		return nil, err
	}
	return &wrapperOutputs{Forwarded: out}, nil
}

func TestEngineInvokeDispatchesByName(t *testing.T) {
	eng := newTestEngine(t)
	MustRegister(eng, innerLogic{})

	ctx := newTestExecCtx()
	ctx.Engine = eng

	out, err := eng.Invoke(ctx, "inner_greet",
		map[string]any{"name": "World"},
		map[string]any{"prefix": "Hello, "},
		nil,
	)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out["greeting"] != "Hello, World" {
		t.Errorf("greeting = %v, want %q", out["greeting"], "Hello, World")
	}
}

func TestEngineInvokeUnknownLogic(t *testing.T) {
	eng := newTestEngine(t)
	ctx := newTestExecCtx()
	ctx.Engine = eng

	_, err := eng.Invoke(ctx, "no_such_logic", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for unregistered logic, got nil")
	}
}

func TestHandlerCanInvokeAnotherLogic(t *testing.T) {
	eng := newTestEngine(t)
	MustRegister(eng, innerLogic{})
	MustRegister(eng, wrapperLogic{})

	ctx := newTestExecCtx()
	ctx.Engine = eng

	h, err := eng.logicRegistry.get("wrapper")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	out, err := h.Execute(ctx,
		map[string]any{"name": "Ada"},
		map[string]any{"inner": "inner_greet"},
		nil,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	fwd, ok := out["forwarded"].(map[string]any)
	if !ok {
		t.Fatalf("forwarded not a map: %#v", out["forwarded"])
	}
	if fwd["greeting"] != "hi-Ada" {
		t.Errorf("greeting = %v, want %q", fwd["greeting"], "hi-Ada")
	}
}
