package engine

import (
	"context"
	"fmt"
	"testing"

	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// TestTaskWrapperExecution tests that a task with a wrapper field executes the wrapper
func TestTaskWrapperExecution(t *testing.T) {
	// Track which tasks were executed
	executed := make(map[string]bool)
	
	// Build config with wrapper setup
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"inner": {
				Name:  "inner",
				Logic: "inner",
			},
			"wrapper": {
				Name:  "wrapper",
				Logic: "wrapper",
			},
			"wrapped_task": {
				Name:  "wrapped_task",
				Logic: "inner",
				Wrapper: &types.WrapperRef{
					Task: "wrapper",
				},
			},
		},
		Flows: map[string]*types.Flow{
			"test_flow": {
				Name: "test_flow",
				Tasks: []types.TaskRef{
					{
						Name: "wrapped_task",
						Bind: &types.Bind{
							Out: map[string]string{"result": "output"},
						},
					},
				},
			},
		},
	}
	
	log := zerolog.Nop()
	eng := New(cfg, nil, log)
	
	// Register inner logic
	registerRawFunc(eng, "inner", func(ctx *types.ExecutionContext, in, params map[string]any, _ types.ProviderInstance) (map[string]any, error) {
		executed["inner"] = true
		return map[string]any{"result": "from_inner"}, nil
	})
	
	// Register wrapper logic that invokes ctx.Nested
	registerRawFunc(eng, "wrapper", func(ctx *types.ExecutionContext, in, params map[string]any, _ types.ProviderInstance) (map[string]any, error) {
		executed["wrapper"] = true
		
		if ctx.Nested == nil || ctx.Nested.Task == "" {
			return nil, fmt.Errorf("wrapper: ctx.Nested is nil or empty")
		}
		
		// Invoke the nested task
		result, err := ctx.Engine.InvokeTask(ctx, ctx.Nested.Task, in, nil)
		if err != nil {
			return nil, err
		}
		
		// Wrapper modifies the result
		result["wrapped"] = true
		return result, nil
	})
	
	// Create execution context using helper
	execCtx := jigsawctx.New(context.Background(), "test_flow", 0, nil, nil)
	execCtx = jigsawctx.WithProviders(execCtx, nullProviders{})
	execCtx = jigsawctx.WithLogger(execCtx, log)
	execCtx.Engine = eng
	execCtx.Scope["input"] = types.ScopedVar{Value: "test_value", Type: "string"}
	
	// Execute the flow
	_, err := eng.executor.Execute(execCtx, cfg.Flows["test_flow"])
	if err != nil {
		t.Fatalf("ExecuteFlow failed: %v", err)
	}
	
	// Verify both wrapper and inner were executed
	if !executed["wrapper"] {
		t.Error("wrapper was not executed")
	}
	if !executed["inner"] {
		t.Error("inner task was not executed")
	}
	
	// Verify the result was published to scope
	resultVar, ok := execCtx.Scope["output"]
	if !ok {
		t.Fatal("output not found in scope")
	}
	
	// The binding extracts just the "result" field, so we get a string
	if resultVal, ok := resultVar.Value.(string); ok {
		if resultVal != "from_inner" {
			t.Errorf("expected 'from_inner', got %v", resultVal)
		}
	} else {
		t.Errorf("expected string result, got %T: %v", resultVar.Value, resultVar.Value)
	}
}

// TestTaskWithoutWrapper tests that tasks without wrappers execute normally
func TestTaskWithoutWrapper(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"normal_task": {
				Name:  "normal_task",
				Logic: "normal",
				// No wrapper field
			},
		},
		Flows: map[string]*types.Flow{
			"normal_flow": {
				Name: "normal_flow",
				Tasks: []types.TaskRef{
					{
						Name: "normal_task",
						Bind: &types.Bind{
							Out: map[string]string{"result": "output"},
						},
					},
				},
			},
		},
	}
	
	log := zerolog.Nop()
	eng := New(cfg, nil, log)
	
	registerRawFunc(eng, "normal", func(ctx *types.ExecutionContext, in, params map[string]any, _ types.ProviderInstance) (map[string]any, error) {
		return map[string]any{"result": "normal_execution"}, nil
	})
	
	execCtx := jigsawctx.New(context.Background(), "normal_flow", 0, nil, nil)
	execCtx = jigsawctx.WithProviders(execCtx, nullProviders{})
	execCtx = jigsawctx.WithLogger(execCtx, log)
	execCtx.Engine = eng
	
	_, err := eng.executor.Execute(execCtx, cfg.Flows["normal_flow"])
	if err != nil {
		t.Fatalf("ExecuteFlow failed: %v", err)
	}
	
	resultVar, ok := execCtx.Scope["output"]
	if !ok {
		t.Fatal("output not found in scope")
	}
	
	if resultVar.Value != "normal_execution" {
		t.Errorf("expected 'normal_execution', got %v", resultVar.Value)
	}
}
