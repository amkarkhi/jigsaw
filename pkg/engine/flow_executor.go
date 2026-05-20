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
	"github.com/rs/zerolog"
)

// FlowExecutor executes flows.
type FlowExecutor struct {
	config       *types.Config
	taskExecutor *TaskExecutor
	logger       zerolog.Logger
}

// NewFlowExecutor creates a new flow executor.
func NewFlowExecutor(config *types.Config, logger zerolog.Logger, reg *logicRegistry) *FlowExecutor {
	return &FlowExecutor{
		config:       config,
		taskExecutor: NewTaskExecutor(config, logger, reg),
		logger:       logger,
	}
}

// NewDryRunFlowExecutor creates a flow executor with an empty logic registry.
// Tasks whose logic is not registered echo their inputs back with a
// _logic_not_implemented marker, which is useful for sandbox / dry-run
// execution without touching real backends.
func NewDryRunFlowExecutor(config *types.Config, logger zerolog.Logger) *FlowExecutor {
	return NewFlowExecutor(config, logger, newLogicRegistry())
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

	debugEvt := f.logger.Debug().Str("flow", flow.Name).Int("task_count", len(resolvedFlow.Tasks))
	if resolvedFlow.Version != "" {
		debugEvt = debugEvt.Str("version", resolvedFlow.Version)
	}
	debugEvt.Msg("Executing flow tasks")

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
// task refs delegate to executeParallel.
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
			continue
		}

		if err := f.executeSingleTask(execCtx, taskRef, flowExec); err != nil {
			return err
		}
	}
	return nil
}

// executeSingleTask handles one TaskRef.
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
				f.logger.Info().Str("task", taskRef.Name).Interface("condition", taskRef.Overrides).Msg("Task skipped due to override")
				return nil
			case "replace":
				actualTaskName = override.ReplacementTask
				f.logger.Info().Str("original_task", taskRef.Name).Str("replacement_task", actualTaskName).Msg("Task replaced due to override")
			}
		}
	}

	task, ok := f.config.Tasks[actualTaskName]
	if !ok {
		return fmt.Errorf("task %q not found", actualTaskName)
	}

	taskExec, err := f.taskExecutor.Execute(execCtx, task, taskRef)
	flowExec.Tasks = append(flowExec.Tasks, taskExec)

	if err != nil {
		if task.Fallback != nil && task.Fallback.Strategy == "continue" {
			f.logger.Warn().Str("task", task.Name).Err(err).Msg("Task failed but continuing due to fallback strategy")
			return nil
		}
		return err
	}
	return nil
}

// executeParallel runs all branches concurrently. Each branch gets its own
// forked ExecutionContext; results merge back into the parent serially.
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

	total := len(block.Branches)
	minSuccess := block.MinSuccess
	if minSuccess < 0 {
		minSuccess = 0
	}
	if minSuccess > total {
		minSuccess = total
	}

	f.logger.Info().
		Int("branches", total).
		Str("on_branch_failure", mode).
		Int("min_success", minSuccess).
		Int("timeout_ms", block.Timeout).
		Msg("Executing parallel block")

	parentGoCtx := execCtx.Context
	if block.Timeout > 0 {
		var cancelBlock stdctx.CancelFunc
		parentGoCtx, cancelBlock = stdctx.WithTimeout(parentGoCtx, time.Duration(block.Timeout)*time.Millisecond)
		defer cancelBlock()
	}

	gctx, cancel := stdctx.WithCancel(parentGoCtx)
	defer cancel()

	branchCtxs := make([]*types.ExecutionContext, total)
	branchTasks := make([][]*types.TaskExecution, total)
	branchErrs := make([]error, total)

	done := make(chan int, total)
	var wg sync.WaitGroup
	for i, branch := range block.Branches {
		i, branch := i, branch

		branchGoCtx := gctx
		if branch.Timeout > 0 {
			var bcancel stdctx.CancelFunc
			branchGoCtx, bcancel = stdctx.WithTimeout(gctx, time.Duration(branch.Timeout)*time.Millisecond)
			defer bcancel()
		}

		childCtx := jigsawctx.Fork(execCtx, branch.Label, branchGoCtx)
		branchCtxs[i] = childCtx

		localFlowExec := &types.FlowExecution{Tasks: make([]*types.TaskExecution, 0)}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					branchErrs[i] = fmt.Errorf("branch %q panicked: %v", branch.Label, r)
				}
				done <- i
			}()

			err := f.executeTaskList(childCtx, branch.Tasks, localFlowExec, false)
			branchTasks[i] = localFlowExec.Tasks
			if err != nil {
				if cerr := branchGoCtx.Err(); cerr != nil && errors.Is(cerr, stdctx.DeadlineExceeded) {
					branchErrs[i] = fmt.Errorf("branch %q: timed out after %dms: %w", branch.Label, branch.Timeout, err)
				} else {
					branchErrs[i] = fmt.Errorf("branch %q: %w", branch.Label, err)
				}
			}
		}()
	}

	successCount := 0
	completed := 0
	earlyExit := false
	for completed < total {
		i := <-done
		completed++
		if branchErrs[i] == nil {
			successCount++
			if minSuccess > 0 && successCount >= minSuccess {
				cancel()
				earlyExit = true
				break
			}
		} else if mode == "cancel" {
			cancel()
		}
	}
	wg.Wait()

	for i := range block.Branches {
		flowExec.Tasks = append(flowExec.Tasks, branchTasks[i]...)
		if branchCtxs[i] != nil {
			jigsawctx.Merge(execCtx, branchCtxs[i])
		}
	}

	if earlyExit {
		return nil
	}
	return classifyParallelErrors(mode, branchErrs, total, minSuccess)
}

// classifyParallelErrors decides whether the parallel block as a whole failed.
func classifyParallelErrors(mode string, errs []error, total int, minSuccess int) error {
	failed := 0
	for _, e := range errs {
		if e != nil {
			failed++
		}
	}
	succeeded := total - failed
	joined := errors.Join(errs...)

	if minSuccess > 0 {
		if succeeded >= minSuccess {
			return nil
		}
		return fmt.Errorf("only %d/%d parallel branches succeeded (min_success=%d): %w", succeeded, total, minSuccess, joined)
	}
	if failed == 0 {
		return nil
	}
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
		return nil, fmt.Errorf("parent flow %q not found for flow %q", flow.Inherits, flow.Name)
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

	f.logger.Debug().Str("flow", flow.Name).Str("parent", flow.Inherits).Msg("Flow inheritance resolved")
	return resolved, nil
}
