package context

import (
	"context"
	"fmt"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Fork creates a branch-local ExecutionContext from a parent. The branch may
// freely mutate its own Scope without affecting siblings. Reads in the branch
// first check the branch's local Scope, then fall back to the parent chain
// (parent.ScopeGet).
//
// After the branch completes, call Merge to publish every key the branch wrote
// into the parent's Scope under "<branchLabel>.<key>".
//
// `goCtx` is the goroutine-cancellation context for the branch.
func Fork(parent *types.ExecutionContext, branchLabel string, goCtx context.Context) *types.ExecutionContext {
	branchPath := append(append([]string{}, parent.BranchPath...), branchLabel)
	branchLogger := parent.Logger.With().Str("branch", branchLabel).Logger()

	child := &types.ExecutionContext{
		RequestID:   parent.RequestID,
		FlowName:    parent.FlowName,
		FlowVersion: parent.FlowVersion,
		Sub:         parent.Sub,
		Tag:         parent.Tag,
		Parameters:  parent.Parameters,
		Headers:     parent.Headers,
		Providers:   parent.Providers,
		Logger:      branchLogger,
		Context:     goCtx,
		Engine:      parent.Engine,
		Nested:      parent.Nested,
		BranchPath:  branchPath,
		Scope:       make(map[string]types.ScopedVar),
		Metadata:    cloneMap(parent.Metadata),
		Versions:    cloneStringMap(parent.Versions),
		TraceEnabled: parent.TraceEnabled,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	// Expose the parent for read fall-through. This field is unexported from
	// the types package so it must be set via SetParentScope.
	types.SetParentScope(child, parent)
	return child
}

// Merge folds the outputs produced inside `branch` back into `parent`. Every
// key the branch wrote to its local Scope is published to the parent under
// "<branchLabel>.<key>", where branchLabel is the last element of
// branch.BranchPath. Version entries are merged directly (no namespacing).
func Merge(parent, branch *types.ExecutionContext) {
	if len(branch.BranchPath) == 0 {
		return
	}
	branchLabel := branch.BranchPath[len(branch.BranchPath)-1]

	for k, v := range branch.Scope {
		parent.ScopePut(branchLabel+"."+k, v)
	}
	for k, v := range branch.Versions {
		parent.Versions[k] = v
	}
	parent.UpdatedAt = time.Now()
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// New creates a new root execution context. The Scope is seeded with request
// parameters so that the first task's Bind map can reference them directly.
func New(ctx context.Context, flowName string, sub int, params map[string]any, headers map[string]string) *types.ExecutionContext {
	execCtx := &types.ExecutionContext{
		RequestID:  generateRequestID(),
		FlowName:   flowName,
		Sub:        sub,
		Parameters: params,
		Headers:    headers,
		Scope:      make(map[string]types.ScopedVar),
		Metadata:   make(map[string]any),
		Versions:   make(map[string]string),
		Context:    ctx,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// Seed scope from request parameters.
	for k, v := range params {
		execCtx.ScopePut(k, types.ScopedVar{Value: v, Type: jsonTypeOf(v)})
	}

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

// WithLogger adds logger to context, enriching it with request_id and flow fields.
func WithLogger(execCtx *types.ExecutionContext, logger zerolog.Logger) *types.ExecutionContext {
	execCtx.Logger = logger.With().
		Str("request_id", execCtx.RequestID).
		Str("flow", execCtx.FlowName).
		Logger()
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
				ShouldOverride:  true,
				Action:          override.Action,
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

// jsonTypeOf returns a JSON-schema-style type string for a Go value.
func jsonTypeOf(v any) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "object"
	}
}
