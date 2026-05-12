package engine

import (
	stdctx "context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/types"
)

// FlowExecutor executes flows.
type FlowExecutor struct {
	config       *types.Config
	taskExecutor *TaskExecutor
	logger       types.Logger
}

// NewFlowExecutor creates a new flow executor.
func NewFlowExecutor(config *types.Config, logger types.Logger, logicRegistry *LogicRegistry) *FlowExecutor {
	return &FlowExecutor{
		config:       config,
		taskExecutor: NewTaskExecutor(config, logger, logicRegistry),
		logger:       logger,
	}
}

// Execute runs a flow end-to-end against the given execution context.
func (f *FlowExecutor) Execute(execCtx *types.ExecutionContext, flow *types.Flow) (*types.FlowExecution, error) {
	flowExec := &types.FlowExecution{
		Flow:      flow,
		Context:   execCtx,
		Status:    types.StatusRunning,
		StartedAt: time.Now(),
		Tasks:     make([]*types.TaskExecution, 0),
	}

	resolvedFlow, err := f.resolveFlowInheritance(flow)
	if err != nil {
		flowExec.Status = types.StatusFailed
		flowExec.Error = err
		return flowExec, err
	}

	execCtx.FlowVersion = resolvedFlow.Version
	if resolvedFlow.Version != "" {
		execCtx.Versions["flow"] = resolvedFlow.Version
	}

	logFields := map[string]any{
		"flow":       flow.Name,
		"task_count": len(resolvedFlow.Tasks),
	}
	if resolvedFlow.Version != "" {
		logFields["version"] = resolvedFlow.Version
	}
	f.logger.Debug("Executing flow tasks", logFields)

	if err := f.executeTaskList(execCtx, resolvedFlow.Tasks, flowExec, true); err != nil {
		flowExec.Status = types.StatusFailed
		flowExec.Error = err
		return flowExec, err
	}

	now := time.Now()
	flowExec.Status = types.StatusCompleted
	flowExec.CompletedAt = &now
	return flowExec, nil
}

// executeTaskList walks an ordered list of task refs sequentially. Parallel
// task refs delegate to executeParallel. Used at the top level of a flow and
// recursively inside each parallel branch.
//
// When updateCurrentStep is true, flowExec.CurrentStep is advanced as we
// traverse — only the top-level call passes true; nested branch calls leave
// the parent's step counter alone.
func (f *FlowExecutor) executeTaskList(
	execCtx *types.ExecutionContext,
	tasks []types.TaskRef,
	flowExec *types.FlowExecution,
	updateCurrentStep bool,
) error {
	for idx, taskRef := range tasks {
		if updateCurrentStep {
			flowExec.CurrentStep = idx
		}

		if taskRef.Parallel != nil {
			if err := f.executeParallel(execCtx, taskRef.Parallel, flowExec); err != nil {
				return err
			}
			execCtx.LastOutput = nil
			continue
		}

		if err := f.executeSingleTask(execCtx, taskRef, flowExec); err != nil {
			return err
		}
	}
	return nil
}

// executeSingleTask handles one TaskRef (overrides, lookup, execution,
// fallback-continue policy). Mirrors the original sequential logic.
func (f *FlowExecutor) executeSingleTask(
	execCtx *types.ExecutionContext,
	taskRef types.TaskRef,
	flowExec *types.FlowExecution,
) error {
	execCtx.CurrentTask = taskRef.Name

	actualTaskName := taskRef.Name
	if len(taskRef.Overrides) > 0 {
		override := jigsawctx.CheckOverride(execCtx, taskRef.Overrides)
		if override.ShouldOverride {
			switch override.Action {
			case "skip":
				f.logger.Info("Task skipped due to override", map[string]any{
					"task":      taskRef.Name,
					"condition": taskRef.Overrides,
				})
				return nil
			case "replace":
				actualTaskName = override.ReplacementTask
				f.logger.Info("Task replaced due to override", map[string]any{
					"original_task":    taskRef.Name,
					"replacement_task": actualTaskName,
				})
			}
		}
	}

	task, ok := f.config.Tasks[actualTaskName]
	if !ok {
		return fmt.Errorf("task '%s' not found", actualTaskName)
	}

	taskExec, err := f.taskExecutor.Execute(execCtx, task)
	flowExec.Tasks = append(flowExec.Tasks, taskExec)

	if err != nil {
		if task.Fallback != nil && task.Fallback.Strategy == "continue" {
			f.logger.Warn("Task failed but continuing due to fallback strategy", map[string]any{
				"task":  task.Name,
				"error": err.Error(),
			})
			return nil
		}
		return err
	}
	return nil
}

// executeParallel runs all branches concurrently. Each branch gets its own
// forked ExecutionContext; results merge back into the parent serially.
//
// Failure policy is governed by block.OnBranchFailure:
//   - "continue" (default): all branches run to completion; their errors are
//     joined via errors.Join and returned only if every branch failed.
//   - "cancel": first hard failure cancels sibling goroutines via gctx.
func (f *FlowExecutor) executeParallel(
	execCtx *types.ExecutionContext,
	block *types.ParallelBlock,
	flowExec *types.FlowExecution,
) error {
	if len(block.Branches) == 0 {
		return nil
	}

	mode := block.OnBranchFailure
	if mode == "" {
		mode = "continue"
	}

	f.logger.Info("Executing parallel block", map[string]any{
		"branches":          len(block.Branches),
		"on_branch_failure": mode,
	})

	gctx, cancel := stdctx.WithCancel(execCtx.Context)
	defer cancel()

	branchCtxs := make([]*types.ExecutionContext, len(block.Branches))
	branchTasks := make([][]*types.TaskExecution, len(block.Branches))
	branchErrs := make([]error, len(block.Branches))

	var wg sync.WaitGroup
	for i, branch := range block.Branches {
		childCtx := jigsawctx.Fork(execCtx, branch.Label, gctx)
		branchCtxs[i] = childCtx

		// Each branch carries a local FlowExecution to keep TaskExecution
		// appends out of the parent's slice while goroutines are live.
		localFlowExec := &types.FlowExecution{Tasks: make([]*types.TaskExecution, 0)}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					branchErrs[i] = fmt.Errorf("branch %q panicked: %v", branch.Label, r)
					if mode == "cancel" {
						cancel()
					}
				}
			}()

			err := f.executeTaskList(childCtx, branch.Tasks, localFlowExec, false)
			branchTasks[i] = localFlowExec.Tasks
			if err != nil {
				branchErrs[i] = fmt.Errorf("branch %q: %w", branch.Label, err)
				if mode == "cancel" {
					cancel()
				}
			}
		}()
	}
	wg.Wait()

	// Merge in branch-declaration order — deterministic regardless of finish
	// order.
	for i := range block.Branches {
		flowExec.Tasks = append(flowExec.Tasks, branchTasks[i]...)
		if branchCtxs[i] != nil {
			jigsawctx.Merge(execCtx, branchCtxs[i])
		}
	}

	return classifyParallelErrors(mode, branchErrs, len(block.Branches))
}

// classifyParallelErrors decides whether the parallel block as a whole failed.
//   - cancel mode: any error fails the block.
//   - continue mode: only fails if every branch errored (otherwise downstream
//     tasks decide via their input requirements).
func classifyParallelErrors(mode string, errs []error, total int) error {
	failed := 0
	for _, e := range errs {
		if e != nil {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}
	joined := errors.Join(errs...)
	if mode == "cancel" {
		return joined
	}
	if failed == total {
		return fmt.Errorf("all %d parallel branches failed: %w", total, joined)
	}
	return nil
}

func (f *FlowExecutor) resolveFlowInheritance(flow *types.Flow) (*types.Flow, error) {
	if flow.Inherits == "" {
		return flow, nil
	}

	parentFlow, ok := f.config.Flows[flow.Inherits]
	if !ok {
		return nil, fmt.Errorf("parent flow '%s' not found for flow '%s'", flow.Inherits, flow.Name)
	}

	resolvedParent, err := f.resolveFlowInheritance(parentFlow)
	if err != nil {
		return nil, err
	}

	resolved := &types.Flow{
		Name:        flow.Name,
		Description: flow.Description,
		Tasks:       flow.Tasks,
		Metadata:    make(map[string]any),
	}
	if len(flow.Tasks) == 0 {
		resolved.Tasks = resolvedParent.Tasks
	}
	maps.Copy(resolved.Metadata, resolvedParent.Metadata)
	maps.Copy(resolved.Metadata, flow.Metadata)

	f.logger.Debug("Flow inheritance resolved", map[string]any{
		"flow":   flow.Name,
		"parent": flow.Inherits,
	})
	return resolved, nil
}
