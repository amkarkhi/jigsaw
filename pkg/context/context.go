package context

import (
	"context"
	"fmt"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/google/uuid"
)

// Fork creates a branch-local ExecutionContext from a parent. The branch may
// freely mutate its own maps and scalar fields without racing siblings. The
// parent's TaskOutputs / Labels / Versions are snapshotted (shallow copy) so
// the branch starts with the same visibility the parent had at fork time.
//
// `goCtx` should be the goroutine-cancellation context for the branch (e.g.
// the gctx from context.WithCancel used by the parallel executor).
func Fork(parent *types.ExecutionContext, branchLabel string, goCtx context.Context) *types.ExecutionContext {
	branchPath := append(append([]string{}, parent.BranchPath...), branchLabel)

	branchLogger := parent.Logger
	if branchLogger != nil {
		branchLogger = branchLogger.With(map[string]any{"branch": branchLabel})
	}

	return &types.ExecutionContext{
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
		BranchPath:  branchPath,
		TaskOutputs: cloneMap(parent.TaskOutputs),
		Metadata:    cloneMap(parent.Metadata),
		Versions:    cloneStringMap(parent.Versions),
		Labels:      cloneLabels(parent.Labels),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

// Merge folds the new outputs / labels / versions produced inside `branch`
// back into `parent`. Sibling branches cannot collide on task outputs because
// SetTaskOutput qualifies the key with the branch path. Labels appended in
// the branch are appended to the parent's index.
func Merge(parent, branch *types.ExecutionContext) {
	for k, v := range branch.TaskOutputs {
		parent.TaskOutputs[k] = v
	}
	for k, v := range branch.Versions {
		parent.Versions[k] = v
	}
	for label, branchProducers := range branch.Labels {
		parentProducers := parent.Labels[label]
		// Only carry over entries that the branch added (not the snapshotted
		// parent entries). Identity by branch path + task name.
		existing := make(map[string]struct{}, len(parentProducers))
		for _, p := range parentProducers {
			existing[producerKey(p)] = struct{}{}
		}
		for _, p := range branchProducers {
			if _, seen := existing[producerKey(p)]; seen {
				continue
			}
			parentProducers = append(parentProducers, p)
		}
		if parent.Labels == nil {
			parent.Labels = make(types.LabelIndex)
		}
		parent.Labels[label] = parentProducers
	}
	parent.UpdatedAt = time.Now()
}

func producerKey(p types.LabeledProducer) string {
	out := p.TaskName
	for _, b := range p.BranchPath {
		out = b + "/" + out
	}
	return out
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

func cloneLabels(in types.LabelIndex) types.LabelIndex {
	out := make(types.LabelIndex, len(in))
	for k, v := range in {
		dup := make([]types.LabeledProducer, len(v))
		copy(dup, v)
		out[k] = dup
	}
	return out
}

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
		Labels:      make(types.LabelIndex),
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
