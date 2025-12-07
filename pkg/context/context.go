package context

import (
	"context"
	"fmt"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/google/uuid"
)

// New creates a new execution context
func New(ctx context.Context, flowName string, sub int, params map[string]any, headers map[string]string) *types.ExecutionContext {
	execCtx := &types.ExecutionContext{
		RequestID:   generateRequestID(),
		FlowName:    flowName,
		Sub:         sub,
		Parameters:  params,
		Headers:     headers,
		TaskOutputs: make(map[string]any),
		Metadata:    make(map[string]any),
		Versions:    make(map[string]string),
		Context:     ctx,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	
	// Extract tag from parameters if present
	if tag, ok := params["tag"].(string); ok {
		execCtx.Tag = tag
	}
	
	return execCtx
}

// WithProviders adds provider registry to context
func WithProviders(execCtx *types.ExecutionContext, registry types.ProviderRegistry) *types.ExecutionContext {
	execCtx.Providers = registry
	return execCtx
}

// WithLogger adds logger to context
func WithLogger(execCtx *types.ExecutionContext, logger types.Logger) *types.ExecutionContext {
	execCtx.Logger = logger.With(map[string]any{
		"request_id": execCtx.RequestID,
		"flow":       execCtx.FlowName,
	})
	return execCtx
}

// generateRequestID creates a unique request identifier
func generateRequestID() string {
	return fmt.Sprintf("req_%s", uuid.New().String()[:8])
}

// CheckOverride checks if task should be overridden based on context
func CheckOverride(execCtx *types.ExecutionContext, overrides []types.TaskOverride) *OverrideResult {
	if len(overrides) == 0 {
		return &OverrideResult{ShouldOverride: false}
	}
	
	for _, override := range overrides {
		if matchCondition(execCtx, override.Condition) {
			return &OverrideResult{
				ShouldOverride: true,
				Action:         override.Action,
				ReplacementTask: override.Task,
			}
		}
	}
	
	return &OverrideResult{ShouldOverride: false}
}

// OverrideResult contains override decision
type OverrideResult struct {
	ShouldOverride  bool
	Action          string // skip, replace
	ReplacementTask string
}

// matchCondition checks if context matches override condition
func matchCondition(execCtx *types.ExecutionContext, condition map[string]any) bool {
	for key, expectedValue := range condition {
		var actualValue any
		
		switch key {
		case "tag":
			actualValue = execCtx.Tag
		case "sub":
			actualValue = execCtx.Sub
		default:
			// Check in headers
			if val, ok := execCtx.Headers[key]; ok {
				actualValue = val
			} else if val, ok := execCtx.Parameters[key]; ok {
				actualValue = val
			} else if val, ok := execCtx.Metadata[key]; ok {
				actualValue = val
			} else {
				return false
			}
		}
		
		if actualValue != expectedValue {
			return false
		}
	}
	
	return true
}
