package engine

import (
	"fmt"
	"maps"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// TaskExecutor executes individual tasks
type TaskExecutor struct {
	config        *types.Config
	logger        zerolog.Logger
	logicRegistry *LogicRegistry
}

// NewTaskExecutor creates a new task executor
func NewTaskExecutor(config *types.Config, logger zerolog.Logger, logicRegistry *LogicRegistry) *TaskExecutor {
	return &TaskExecutor{
		config:        config,
		logger:        logger,
		logicRegistry: logicRegistry,
	}
}

// Execute executes a single task
func (t *TaskExecutor) Execute(execCtx *types.ExecutionContext, task *types.Task) (*types.TaskExecution, error) {
	taskExec := &types.TaskExecution{
		Task:      task,
		Status:    types.StatusRunning,
		StartedAt: time.Now(),
		Inputs:    make(map[string]any),
		Outputs:   make(map[string]any),
	}

	// Resolve task inheritance
	resolvedTask, err := t.resolveTaskInheritance(task)
	if err != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = err
		return taskExec, err
	}
	taskExec.ActualTask = resolvedTask

	// Capture task version
	taskExec.TaskVersion = resolvedTask.Version
	if taskExec.TaskVersion != "" {
		execCtx.Versions[fmt.Sprintf("task:%s", task.Name)] = taskExec.TaskVersion
	}

	t.logger.Debug().Str("task", task.Name).Str("version", resolvedTask.Version).Str("provider", resolvedTask.Provider).Str("logic", resolvedTask.Logic).Msg("Executing task")

	// Get scoped inputs for the task
	scopedData := execCtx.GetScopedData(resolvedTask)

	// Validate inputs
	for _, inputDef := range resolvedTask.Inputs {
		val, ok := scopedData[inputDef.Name]
		if !ok && inputDef.Required {
			if inputDef.Default != nil {
				val = inputDef.Default
			} else {
				err := fmt.Errorf("required input '%s' not found for task '%s'", inputDef.Name, task.Name)
				taskExec.Status = types.StatusFailed
				taskExec.Error = err
				return taskExec, t.handleFallback(execCtx, taskExec, err)
			}
		}
		taskExec.Inputs[inputDef.Name] = val
	}

	// Execute task logic with provider if needed
	var outputs map[string]any
	var execErr error

	if resolvedTask.Provider != "" {
		outputs, execErr = t.executeWithProvider(execCtx, resolvedTask, taskExec.Inputs)
		// Capture provider version if used
		if execErr == nil && resolvedTask.Provider != "" {
			if provider, err := execCtx.Providers.Get(resolvedTask.Provider); err == nil {
				providerConfig := provider.GetProvider()
				if providerConfig.Version != "" {
					taskExec.ProviderVersion = providerConfig.Version
					execCtx.Versions[fmt.Sprintf("provider:%s", resolvedTask.Provider)] = providerConfig.Version
				}
			}
		}
	} else {
		outputs, execErr = t.executeLogic(execCtx, resolvedTask, taskExec.Inputs)
	}

	if execErr != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = execErr

		// Handle fallback
		if resolvedTask.Fallback != nil {
			return taskExec, t.handleFallback(execCtx, taskExec, execErr)
		}

		return taskExec, execErr
	}

	// Store outputs (qualified by branch path inside parallel scopes) and
	// publish the task's label into the index if one is declared.
	taskExec.Outputs = outputs
	execCtx.SetTaskOutput(task.Name, outputs)
	execCtx.PublishLabel(resolvedTask.Label, task.Name, outputs)

	// Mark as completed
	now := time.Now()
	taskExec.Status = types.StatusCompleted
	taskExec.CompletedAt = &now

	completedEvt := t.logger.Debug().Str("task", task.Name).Interface("outputs", outputs).Int64("duration", time.Since(taskExec.StartedAt).Milliseconds())
	if taskExec.TaskVersion != "" {
		completedEvt = completedEvt.Str("version", taskExec.TaskVersion)
	}
	if taskExec.ProviderVersion != "" {
		completedEvt = completedEvt.Str("provider_version", taskExec.ProviderVersion)
	}
	completedEvt.Msg("Task completed successfully")

	return taskExec, nil
}

// executeWithProvider executes task logic with a provider
func (t *TaskExecutor) executeWithProvider(execCtx *types.ExecutionContext, task *types.Task, inputs map[string]any) (map[string]any, error) {
	// Get provider instance
	provider, err := execCtx.Providers.Get(task.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider '%s': %w", task.Provider, err)
	}

	// Ensure provider is connected
	if !provider.IsConnected() {
		if err := provider.Connect(execCtx.Context); err != nil {
			return nil, fmt.Errorf("failed to connect provider '%s': %w", task.Provider, err)
		}
	}

	t.logger.Debug().Str("task", task.Name).Str("provider", task.Provider).Str("logic", task.Logic).Msg("Executing task with provider")

	// Execute logic with provider
	return t.executeTaskLogic(execCtx, task.Logic, inputs, provider)
}

// executeLogic executes task logic without a provider
func (t *TaskExecutor) executeLogic(execCtx *types.ExecutionContext, task *types.Task, inputs map[string]any) (map[string]any, error) {
	t.logger.Debug().Str("task", task.Name).Str("logic", task.Logic).Msg("Executing task logic")

	// Execute logic without provider
	return t.executeTaskLogic(execCtx, task.Logic, inputs, nil)
}

// executeTaskLogic executes registered task logic
func (t *TaskExecutor) executeTaskLogic(execCtx *types.ExecutionContext, logic string, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error) {
	// Look up logic handler
	handler, err := t.logicRegistry.Get(logic)
	if err != nil {
		t.logger.Warn().Str("logic", logic).Err(err).Msg("Logic handler not found, returning inputs as outputs")

		// Return inputs as outputs if no handler registered (for testing/development)
		outputs := make(map[string]any)
		for k, v := range inputs {
			outputs[k] = v
		}
		outputs["_logic_not_implemented"] = logic
		return outputs, nil
	}

	t.logger.Debug().Str("logic", logic).Interface("inputs", inputs).Msg("Executing task logic")

	// Execute the registered handler
	return handler(execCtx, inputs, provider)
}

// handleFallback handles task failure with fallback strategy
func (t *TaskExecutor) handleFallback(execCtx *types.ExecutionContext, taskExec *types.TaskExecution, err error) error {
	task := taskExec.Task

	if task.Fallback == nil {
		return err
	}

	t.logger.Warn().Str("task", task.Name).Str("strategy", task.Fallback.Strategy).Err(err).Msg("Task failed, applying fallback strategy")

	taskExec.FallbackUsed = true

	switch task.Fallback.Strategy {
	case "abort":
		// Stop flow execution
		return fmt.Errorf("task '%s' failed (abort): %w", task.Name, err)

	case "continue":
		// Continue with default values
		if task.Fallback.Defaults != nil {
			taskExec.Outputs = task.Fallback.Defaults
			execCtx.SetTaskOutput(task.Name, task.Fallback.Defaults)
			execCtx.PublishLabel(task.Label, task.Name, task.Fallback.Defaults)
		}

		now := time.Now()
		taskExec.Status = types.StatusCompleted
		taskExec.CompletedAt = &now

		t.logger.Info().Str("task", task.Name).Interface("defaults", task.Fallback.Defaults).Msg("Task continued with fallback defaults")

		return nil

	case "switch_task":
		// This would be handled at flow level
		return fmt.Errorf("task '%s' failed, switch_task fallback not implemented at task level", task.Name)

	case "switch_provider":
		// Try alternate providers
		if len(task.Fallback.Providers) > 0 {
			return t.tryAlternateProviders(execCtx, taskExec, task.Fallback.Providers)
		}
		return err

	default:
		return err
	}
}

// tryAlternateProviders attempts to execute task with alternate providers
func (t *TaskExecutor) tryAlternateProviders(execCtx *types.ExecutionContext, taskExec *types.TaskExecution, providers []string) error {
	task := taskExec.Task

	for _, providerName := range providers {
		t.logger.Info().Str("task", task.Name).Str("provider", providerName).Msg("Trying alternate provider")

		// Create a temporary task with alternate provider
		altTask := *task
		altTask.Provider = providerName

		outputs, err := t.executeWithProvider(execCtx, &altTask, taskExec.Inputs)
		if err == nil {
			taskExec.Outputs = outputs
			taskExec.ProviderUsed = providerName
			execCtx.SetTaskOutput(task.Name, outputs)
			execCtx.PublishLabel(task.Label, task.Name, outputs)

			now := time.Now()
			taskExec.Status = types.StatusCompleted
			taskExec.CompletedAt = &now

			t.logger.Info().Str("task", task.Name).Str("provider", providerName).Msg("Task succeeded with alternate provider")

			return nil
		}

		t.logger.Warn().Str("task", task.Name).Str("provider", providerName).Err(err).Msg("Alternate provider failed")
	}

	return fmt.Errorf("all provider fallbacks failed for task '%s'", task.Name)
}

// resolveTaskInheritance resolves task inheritance
func (t *TaskExecutor) resolveTaskInheritance(task *types.Task) (*types.Task, error) {
	if task.Inherits == "" {
		return task, nil
	}

	parentTask, ok := t.config.Tasks[task.Inherits]
	if !ok {
		return nil, fmt.Errorf("parent task '%s' not found for task '%s'", task.Inherits, task.Name)
	}

	// Recursively resolve parent inheritance
	resolvedParent, err := t.resolveTaskInheritance(parentTask)
	if err != nil {
		return nil, err
	}

	// Merge parent and child
	resolved := &types.Task{
		Name:        task.Name,
		Description: task.Description,
		Label:       task.Label,
		Inputs:      task.Inputs,
		Outputs:     task.Outputs,
		Provider:    task.Provider,
		Fallback:    task.Fallback,
		Logic:       task.Logic,
		Timeout:     task.Timeout,
		Retry:       task.Retry,
		Metadata:    make(map[string]any),
	}
	if resolved.Label == "" {
		resolved.Label = resolvedParent.Label
	}

	// Inherit from parent if not specified in child
	if len(resolved.Inputs) == 0 {
		resolved.Inputs = resolvedParent.Inputs
	}
	if len(resolved.Outputs) == 0 {
		resolved.Outputs = resolvedParent.Outputs
	}
	if resolved.Provider == "" {
		resolved.Provider = resolvedParent.Provider
	}
	if resolved.Fallback == nil {
		resolved.Fallback = resolvedParent.Fallback
	}
	if resolved.Logic == "" {
		resolved.Logic = resolvedParent.Logic
	}
	if resolved.Timeout == 0 {
		resolved.Timeout = resolvedParent.Timeout
	}
	if resolved.Retry == 0 {
		resolved.Retry = resolvedParent.Retry
	}

	// Merge metadata
	maps.Copy(resolved.Metadata, resolvedParent.Metadata)
	maps.Copy(resolved.Metadata, task.Metadata)

	t.logger.Debug().Str("task", task.Name).Str("parent", task.Inherits).Msg("Task inheritance resolved")

	return resolved, nil
}
