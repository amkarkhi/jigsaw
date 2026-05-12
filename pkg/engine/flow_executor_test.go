package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/logger"
	"github.com/amkarkhi/jigsaw/pkg/types"
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

// sleepLogic returns a handler that sleeps for d (respecting ctx cancellation)
// and emits `{"value": value}`.
func sleepLogic(d time.Duration, value string) LogicHandler {
	return func(ctx *types.ExecutionContext, inputs map[string]any, _ types.ProviderInstance) (map[string]any, error) {
		select {
		case <-time.After(d):
			return map[string]any{"value": value}, nil
		case <-ctx.Context.Done():
			return nil, ctx.Context.Err()
		}
	}
}

func newEngine(t *testing.T, cfg *types.Config, register func(*Engine)) (*Engine, *types.ExecutionContext) {
	t.Helper()
	log := logger.New("error", false)
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
		e.MustRegisterLogic("sleep_left", sleepLogic(100*time.Millisecond, "L"))
		e.MustRegisterLogic("sleep_right", sleepLogic(100*time.Millisecond, "R"))
	})

	start := time.Now()
	_, err := eng.executor.Execute(execCtx, cfg.Flows["f"])
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("flow failed: %v", err)
	}
	// Generous upper bound: 100ms sleep each, parallel should finish in ~100ms.
	// Anything under 180ms proves the branches ran concurrently rather than
	// back-to-back (~200ms).
	if elapsed > 180*time.Millisecond {
		t.Fatalf("expected parallel branches to finish in ~100ms, took %v", elapsed)
	}
}

// TestLabelResolutionAcrossBranches verifies that a collector task placed
// after a parallel block can read each branch's labeled output via
// `from: <branch>.<label>`.
func TestLabelResolutionAcrossBranches(t *testing.T) {
	collected := make(chan map[string]any, 1)

	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"prod": {
				Name:    "prod",
				Label:   "data",
				Logic:   "produce",
				Outputs: []types.FieldDef{{Name: "value", Type: "string"}},
			},
			"collect": {
				Name: "collect",
				Inputs: []types.FieldDef{
					{Name: "left_value", From: "L.data", Field: "value", Type: "string"},
					{Name: "right_value", From: "R.data", Field: "value", Type: "string"},
				},
				Logic: "collect",
			},
		},
		Flows: map[string]*types.Flow{
			"f": {
				Name: "f",
				Tasks: []types.TaskRef{
					{Parallel: &types.ParallelBlock{
						Branches: []types.Branch{
							{Label: "L", Tasks: []types.TaskRef{{Name: "prod"}}},
							{Label: "R", Tasks: []types.TaskRef{{Name: "prod"}}},
						},
					}},
					{Name: "collect"},
				},
			},
		},
	}

	// The "prod" task produces a different value depending on which branch
	// runs it. We thread that through the execution context's CurrentTask /
	// branch path so the test handler can distinguish them.
	eng, execCtx := newEngine(t, cfg, func(e *Engine) {
		e.MustRegisterLogic("produce", func(ctx *types.ExecutionContext, _ map[string]any, _ types.ProviderInstance) (map[string]any, error) {
			tag := "main"
			if len(ctx.BranchPath) > 0 {
				tag = ctx.BranchPath[len(ctx.BranchPath)-1]
			}
			return map[string]any{"value": "from-" + tag}, nil
		})
		e.MustRegisterLogic("collect", func(_ *types.ExecutionContext, inputs map[string]any, _ types.ProviderInstance) (map[string]any, error) {
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
			"boom":     {Name: "boom", Logic: "boom"},
			"sleeper":  {Name: "sleeper", Logic: "sleeper"},
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
		e.MustRegisterLogic("boom", func(*types.ExecutionContext, map[string]any, types.ProviderInstance) (map[string]any, error) {
			return nil, fmt.Errorf("boom")
		})
		e.MustRegisterLogic("sleeper", sleepLogic(2*time.Second, "S"))
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
