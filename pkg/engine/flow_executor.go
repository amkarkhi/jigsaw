package engine

import (
	"fmt"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
)

// FlowExecutor executes flows
type FlowExecutor struct {
	config       *types.Config
	taskExecutor *TaskExecutor
	logger       types.Logger
}

// NewFlowExecutor creates a new flow executor
func NewFlowExecutor(config *types.Config, logger types.Logger, logicRegistry *LogicRegistry) *FlowExecutor {
	return &FlowExecutor{
		config:       config,
		taskExecutor: NewTaskExecutor(config, logger, logicRegistry),
		logger:       logger,
	}
}

// Execute executes a flow
func (f *FlowExecutor) Execute(execCtx *types.ExecutionContext, flow *types.Flow) (*types.FlowExecution, error) {
	flowExec := &types.FlowExecution{
		Flow:      flow,
		Context:   execCtx,
		Status:    types.StatusRunning,
		StartedAt: time.Now(),
		Tasks:     make([]*types.TaskExecution, 0),
	}
	
	// Resolve inheritance if needed
	resolvedFlow, err := f.resolveFlowInheritance(flow)
	if err != nil {
		flowExec.Status = types.StatusFailed
		flowExec.Error = err
		return flowExec, err
	}
	
	// Capture flow version in execution context
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
	
	// Execute each task in sequence
	for idx, taskRef := range resolvedFlow.Tasks {
		flowExec.CurrentStep = idx
		execCtx.CurrentTask = taskRef.Name
		
		// Check for parallel execution
		if len(taskRef.Parallel) > 0 {
			if err := f.executeParallelTasks(execCtx, taskRef.Parallel, flowExec); err != nil {
				flowExec.Status = types.StatusFailed
				flowExec.Error = err
				return flowExec, err
			}
			continue
		}
		
		// Check for task override
		var actualTaskName string
		var shouldSkip bool
		
		if len(taskRef.Overrides) > 0 {
			override := jigsawctx.CheckOverride(execCtx, taskRef.Overrides)
			if override.ShouldOverride {
				switch override.Action {
				case "skip":
					f.logger.Info("Task skipped due to override", map[string]any{
						"task":      taskRef.Name,
						"condition": taskRef.Overrides,
					})
					shouldSkip = true
				case "replace":
					actualTaskName = override.ReplacementTask
					f.logger.Info("Task replaced due to override", map[string]any{
						"original_task":    taskRef.Name,
						"replacement_task": actualTaskName,
					})
				}
			}
		}
		
		if shouldSkip {
			continue
		}
		
		if actualTaskName == "" {
			actualTaskName = taskRef.Name
		}
		
		// Get task definition
		task, ok := f.config.Tasks[actualTaskName]
		if !ok {
			err := fmt.Errorf("task '%s' not found", actualTaskName)
			flowExec.Status = types.StatusFailed
			flowExec.Error = err
			return flowExec, err
		}
		
		// Execute task
		taskExec, err := f.taskExecutor.Execute(execCtx, task)
		flowExec.Tasks = append(flowExec.Tasks, taskExec)
		
		if err != nil {
			// Check if fallback allows continuation
			if task.Fallback != nil && task.Fallback.Strategy == "continue" {
				f.logger.Warn("Task failed but continuing due to fallback strategy", map[string]any{
					"task": task.Name,
					"error": err.Error(),
				})
				continue
			}
			
			flowExec.Status = types.StatusFailed
			flowExec.Error = err
			return flowExec, err
		}
	}
	
	// Flow completed successfully
	now := time.Now()
	flowExec.Status = types.StatusCompleted
	flowExec.CompletedAt = &now
	
	return flowExec, nil
}

// executeParallelTasks executes tasks in parallel
func (f *FlowExecutor) executeParallelTasks(execCtx *types.ExecutionContext, tasks []types.TaskRef, flowExec *types.FlowExecution) error {
	f.logger.Info("Executing parallel tasks", map[string]any{
		"task_count": len(tasks),
	})
	
	// For now, execute sequentially
	// TODO: Implement actual parallel execution with goroutines and sync
	for _, taskRef := range tasks {
		task, ok := f.config.Tasks[taskRef.Name]
		if !ok {
			return fmt.Errorf("task '%s' not found in parallel group", taskRef.Name)
		}
		
		taskExec, err := f.taskExecutor.Execute(execCtx, task)
		flowExec.Tasks = append(flowExec.Tasks, taskExec)
		
		if err != nil {
			return fmt.Errorf("parallel task '%s' failed: %w", taskRef.Name, err)
		}
	}
	
	return nil
}

// resolveFlowInheritance resolves flow inheritance
func (f *FlowExecutor) resolveFlowInheritance(flow *types.Flow) (*types.Flow, error) {
	if flow.Inherits == "" {
		return flow, nil
	}
	
	parentFlow, ok := f.config.Flows[flow.Inherits]
	if !ok {
		return nil, fmt.Errorf("parent flow '%s' not found for flow '%s'", flow.Inherits, flow.Name)
	}
	
	// Recursively resolve parent inheritance
	resolvedParent, err := f.resolveFlowInheritance(parentFlow)
	if err != nil {
		return nil, err
	}
	
	// Merge parent and child
	resolved := &types.Flow{
		Name:        flow.Name,
		Description: flow.Description,
		Tasks:       flow.Tasks,
		Metadata:    make(map[string]any),
	}
	
	// If child has no tasks, use parent's tasks
	if len(flow.Tasks) == 0 {
		resolved.Tasks = resolvedParent.Tasks
	}
	
	// Merge metadata
	for k, v := range resolvedParent.Metadata {
		resolved.Metadata[k] = v
	}
	for k, v := range flow.Metadata {
		resolved.Metadata[k] = v
	}
	
	f.logger.Debug("Flow inheritance resolved", map[string]any{
		"flow":   flow.Name,
		"parent": flow.Inherits,
	})
	
	return resolved, nil
}
