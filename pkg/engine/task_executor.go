package engine

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
)

// TaskExecutor executes individual tasks.
type TaskExecutor struct {
	config        *types.Config
	logger        zerolog.Logger
	logicRegistry *logicRegistry
}

// NewTaskExecutor creates a new task executor.
func NewTaskExecutor(config *types.Config, logger zerolog.Logger, reg *logicRegistry) *TaskExecutor {
	return &TaskExecutor{
		config:        config,
		logger:        logger,
		logicRegistry: reg,
	}
}

// Execute executes a single task. The TaskRef carries bind/as rename maps that
// tell the executor how to source inputs from and publish outputs to the scope.
func (t *TaskExecutor) Execute(execCtx *types.ExecutionContext, task *types.Task, ref types.TaskRef) (*types.TaskExecution, error) {
	taskExec := &types.TaskExecution{
		Task:      task,
		Status:    types.StatusRunning,
		StartedAt: time.Now(),
		Inputs:    make(map[string]any),
		Outputs:   make(map[string]any),
	}

	resolvedTask, err := t.resolveTaskInheritance(task)
	if err != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = err
		return taskExec, err
	}
	taskExec.ActualTask = resolvedTask

	taskExec.TaskVersion = resolvedTask.Version
	if taskExec.TaskVersion != "" {
		execCtx.Versions[fmt.Sprintf("task:%s", task.Name)] = taskExec.TaskVersion
	}

	// Check if this task has a wrapper. If so, we execute the wrapper instead,
	// with ctx.Nested pointing to this task so the wrapper can invoke it.
	if resolvedTask.Wrapper != nil && resolvedTask.Wrapper.Task != "" {
		return t.executeWithWrapper(execCtx, resolvedTask, ref, taskExec)
	}

	t.logger.Debug().Str("task", task.Name).Str("version", resolvedTask.Version).Str("provider", resolvedTask.Provider).Str("logic", resolvedTask.Logic).Msg("Executing task")

	// Gather inputs from scope using handler's InputSchema and Bind.In rename map.
	inputs, err := t.gatherInputs(execCtx, resolvedTask, ref.Bind)
	if err != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = err
		return taskExec, t.handleFallback(execCtx, taskExec, ref, err)
	}
	taskExec.Inputs = inputs

	// Params start from the task definition; the flow's TaskRef may override
	// individual keys (shallow merge, ref wins).
	params := make(map[string]any, len(resolvedTask.Params)+len(ref.Params))
	maps.Copy(params, resolvedTask.Params)
	maps.Copy(params, ref.Params)
	taskExec.Params = params

	var outputs map[string]any
	var execErr error

	if resolvedTask.Provider != "" {
		outputs, execErr = t.executeWithProvider(execCtx, resolvedTask, inputs, params)
		if execErr == nil {
			if provider, err := execCtx.Providers.Get(resolvedTask.Provider); err == nil {
				providerConfig := provider.GetProvider()
				if providerConfig.Version != "" {
					taskExec.ProviderVersion = providerConfig.Version
					execCtx.Versions[fmt.Sprintf("provider:%s", resolvedTask.Provider)] = providerConfig.Version
				}
			}
		}
	} else {
		outputs, execErr = t.executeLogic(execCtx, resolvedTask, inputs, params)
	}

	if execErr != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = execErr

		if resolvedTask.Fallback != nil {
			return taskExec, t.handleFallback(execCtx, taskExec, ref, execErr)
		}
		return taskExec, execErr
	}

	taskExec.Outputs = outputs
	t.publishOutputs(execCtx, resolvedTask, outputs, ref.Bind)

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

// gatherInputs builds the input map for a task by reading from scope.
//
// When the handler has an InputSchema, only properties declared in the schema
// are gathered; bind.In controls which scope key maps to each property
// (default: the property name itself). Required-but-missing properties are
// errors; optional-and-missing properties are silently omitted.
//
// When the handler has no InputSchema (nil or no Properties), every entry in
// bind.In is resolved from scope. This allows handlers registered without
// typed structs to still receive data via bind.
func (t *TaskExecutor) gatherInputs(execCtx *types.ExecutionContext, task *types.Task, bind *types.Bind) (map[string]any, error) {
	handler, err := t.logicRegistry.get(task.Logic)
	if err != nil {
		// No handler registered — resolve bind.In keys only.
		return t.gatherFromBind(execCtx, bind.InMap()), nil
	}

	schema := handler.InputSchema()
	if schema == nil || schema.Properties == nil {
		// No schema — resolve bind.In keys only.
		return t.gatherFromBind(execCtx, bind.InMap()), nil
	}

	skipped := bind.SkipSet()
	inputs := make(map[string]any, schema.Properties.Len())
	for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		fieldName := pair.Key

		// Explicit skip: omit from the input map (logic sees Go zero value)
		// and treat as required-satisfied for this task ref.
		if _, isSkipped := skipped[fieldName]; isSkipped {
			continue
		}

		// Determine the scope key via bind.In rename (default: same name).
		scopeKey := bind.ResolveIn(fieldName)

		sv, found := execCtx.ScopeGet(scopeKey)
		if !found {
			// Check schema default.
			propSchema := pair.Value
			if propSchema.Default != nil {
				inputs[fieldName] = propSchema.Default
				continue
			}
			// If the field is not required, skip it silently.
			if !isRequired(schema, fieldName) {
				continue
			}
			return nil, fmt.Errorf("task %q: required input %q not found in scope (bind.in key %q)", task.Name, fieldName, scopeKey)
		}
		inputs[fieldName] = sv.Value
	}
	return inputs, nil
}

// gatherFromBind resolves scope values for each entry in the bind map, plus
// any scope entry whose key appears as a bind target. Used for handlers without
// an InputSchema so they still receive explicitly wired inputs.
func (t *TaskExecutor) gatherFromBind(execCtx *types.ExecutionContext, bind map[string]string) map[string]any {
	if len(bind) == 0 {
		return make(map[string]any)
	}
	inputs := make(map[string]any, len(bind))
	for fieldName, scopeKey := range bind {
		if sv, ok := execCtx.ScopeGet(scopeKey); ok {
			inputs[fieldName] = sv.Value
		}
	}
	return inputs
}

// publishOutputs writes each output to the execution scope. bind.Out controls
// renaming (default: the property name from OutputSchema).
func (t *TaskExecutor) publishOutputs(execCtx *types.ExecutionContext, task *types.Task, outputs map[string]any, bind *types.Bind) {
	handler, err := t.logicRegistry.get(task.Logic)
	if err != nil {
		// Publish all outputs verbatim if no handler metadata.
		for k, v := range outputs {
			execCtx.ScopePut(bind.ResolveOut(k), types.ScopedVar{Value: v, Type: jsonTypeOfValue(v)})
		}
		return
	}

	schema := handler.OutputSchema()
	if schema == nil || schema.Properties == nil {
		for k, v := range outputs {
			execCtx.ScopePut(bind.ResolveOut(k), types.ScopedVar{Value: v, Type: jsonTypeOfValue(v)})
		}
		return
	}

	for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		fieldName := pair.Key
		v, ok := outputs[fieldName]
		if !ok {
			continue
		}
		schemaType := schemaTypeString(pair.Value)
		if schemaType == "" {
			schemaType = jsonTypeOfValue(v)
		}
		execCtx.ScopePut(bind.ResolveOut(fieldName), types.ScopedVar{Value: v, Type: schemaType})
	}
}

// executeWithProvider executes task logic with a provider.
func (t *TaskExecutor) executeWithProvider(execCtx *types.ExecutionContext, task *types.Task, inputs, params map[string]any) (map[string]any, error) {
	provider, err := execCtx.Providers.Get(task.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider %q: %w", task.Provider, err)
	}

	if !provider.IsConnected() {
		if err := provider.Connect(execCtx.Context); err != nil {
			return nil, fmt.Errorf("failed to connect provider %q: %w", task.Provider, err)
		}
	}

	t.logger.Debug().Str("task", task.Name).Str("provider", task.Provider).Str("logic", task.Logic).Msg("Executing task with provider")

	return t.executeTaskLogic(execCtx, task.Logic, inputs, params, provider)
}

// executeLogic executes task logic without a provider.
func (t *TaskExecutor) executeLogic(execCtx *types.ExecutionContext, task *types.Task, inputs, params map[string]any) (map[string]any, error) {
	t.logger.Debug().Str("task", task.Name).Str("logic", task.Logic).Msg("Executing task logic")
	return t.executeTaskLogic(execCtx, task.Logic, inputs, params, nil)
}

// executeTaskLogic dispatches to the registered handler.
func (t *TaskExecutor) executeTaskLogic(execCtx *types.ExecutionContext, logic string, inputs, params map[string]any, provider types.ProviderInstance) (map[string]any, error) {
	handler, err := t.logicRegistry.get(logic)
	if err != nil {
		t.logger.Warn().Str("logic", logic).Err(err).Msg("Logic handler not found, returning inputs as outputs")
		outputs := make(map[string]any, len(inputs))
		maps.Copy(outputs, inputs)
		outputs["_logic_not_implemented"] = logic
		return outputs, nil
	}

	t.logger.Debug().Str("logic", logic).Interface("inputs", inputs).Msg("Executing task logic")
	return handler.Execute(execCtx, inputs, params, provider)
}

// handleFallback handles task failure with fallback strategy.
func (t *TaskExecutor) handleFallback(execCtx *types.ExecutionContext, taskExec *types.TaskExecution, ref types.TaskRef, err error) error {
	task := taskExec.Task

	if task.Fallback == nil {
		return err
	}

	t.logger.Warn().Str("task", task.Name).Str("strategy", task.Fallback.Strategy).Err(err).Msg("Task failed, applying fallback strategy")

	taskExec.FallbackUsed = true

	switch task.Fallback.Strategy {
	case "abort":
		return fmt.Errorf("task %q failed (abort): %w", task.Name, err)

	case "continue":
		if task.Fallback.Defaults != nil {
			taskExec.Outputs = task.Fallback.Defaults
			t.publishOutputs(execCtx, task, task.Fallback.Defaults, ref.Bind)
		}
		now := time.Now()
		taskExec.Status = types.StatusCompleted
		taskExec.CompletedAt = &now
		t.logger.Info().Str("task", task.Name).Interface("defaults", task.Fallback.Defaults).Msg("Task continued with fallback defaults")
		return nil

	case "switch_provider":
		if len(task.Fallback.Providers) > 0 {
			return t.tryAlternateProviders(execCtx, taskExec, ref, task.Fallback.Providers)
		}
		return err

	default:
		return err
	}
}

// tryAlternateProviders attempts to execute task with alternate providers.
func (t *TaskExecutor) tryAlternateProviders(execCtx *types.ExecutionContext, taskExec *types.TaskExecution, ref types.TaskRef, providers []string) error {
	task := taskExec.Task

	for _, providerName := range providers {
		t.logger.Info().Str("task", task.Name).Str("provider", providerName).Msg("Trying alternate provider")

		altTask := *task
		altTask.Provider = providerName

		altParams := make(map[string]any, len(altTask.Params)+len(ref.Params))
		maps.Copy(altParams, altTask.Params)
		maps.Copy(altParams, ref.Params)

		outputs, err := t.executeWithProvider(execCtx, &altTask, taskExec.Inputs, altParams)
		if err == nil {
			taskExec.Outputs = outputs
			taskExec.ProviderUsed = providerName
			t.publishOutputs(execCtx, task, outputs, ref.Bind)

			now := time.Now()
			taskExec.Status = types.StatusCompleted
			taskExec.CompletedAt = &now

			t.logger.Info().Str("task", task.Name).Str("provider", providerName).Msg("Task succeeded with alternate provider")
			return nil
		}

		t.logger.Warn().Str("task", task.Name).Str("provider", providerName).Err(err).Msg("Alternate provider failed")
	}

	return fmt.Errorf("all provider fallbacks failed for task %q", task.Name)
}

// executeWithWrapper executes a task through its wrapper. The wrapper task
// receives ctx.Nested pointing to the original task, allowing it to invoke
// the original task via ctx.Engine.InvokeTask. The wrapper inherits the
// original task's I/O bindings so data flows through transparently.
func (t *TaskExecutor) executeWithWrapper(
	execCtx *types.ExecutionContext,
	originalTask *types.Task,
	ref types.TaskRef,
	taskExec *types.TaskExecution,
) (*types.TaskExecution, error) {
	wrapper := originalTask.Wrapper
	if wrapper == nil || wrapper.Task == "" {
		return nil, fmt.Errorf("executeWithWrapper called but wrapper is nil or empty")
	}

	// Resolve the wrapper task
	wrapperTask, ok := t.config.Tasks[wrapper.Task]
	if !ok {
		taskExec.Status = types.StatusFailed
		err := fmt.Errorf("wrapper task %q not found for task %q", wrapper.Task, originalTask.Name)
		taskExec.Error = err
		return taskExec, err
	}

	resolvedWrapper, err := t.resolveTaskInheritance(wrapperTask)
	if err != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = err
		return taskExec, err
	}

	t.logger.Debug().
		Str("task", originalTask.Name).
		Str("wrapper", wrapper.Task).
		Str("wrapper_logic", resolvedWrapper.Logic).
		Msg("Executing task with wrapper")

	// Gather inputs using the ORIGINAL task's I/O schema and bindings.
	// The wrapper is transparent to the flow's I/O.
	inputs, err := t.gatherInputs(execCtx, originalTask, ref.Bind)
	if err != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = err
		return taskExec, t.handleFallback(execCtx, taskExec, ref, err)
	}
	taskExec.Inputs = inputs

	// Build params: start with wrapper's params, merge original task's params,
	// then merge flow-level overrides.
	params := make(map[string]any)
	maps.Copy(params, resolvedWrapper.Params)
	maps.Copy(params, originalTask.Params)
	if wrapper.Params != nil {
		maps.Copy(params, wrapper.Params)
	}
	maps.Copy(params, ref.Params)
	taskExec.Params = params

	// Set ctx.Nested to point to the original task so the wrapper logic
	// knows which task to invoke. Params are the inner task's resolved
	// params — defaults from the task definition with flow-level overrides
	// applied — so wrappers (e.g. caches) can vary their behaviour on the
	// same overrides the inner task will run with.
	innerParams := make(map[string]any, len(originalTask.Params)+len(ref.Params))
	maps.Copy(innerParams, originalTask.Params)
	maps.Copy(innerParams, ref.Params)
	prevNested := execCtx.Nested
	execCtx.Nested = &types.WrapperRef{
		Task:   originalTask.Name,
		Params: innerParams,
	}
	defer func() { execCtx.Nested = prevNested }()

	// Execute the wrapper task
	var outputs map[string]any
	var execErr error

	if resolvedWrapper.Provider != "" {
		outputs, execErr = t.executeWithProvider(execCtx, resolvedWrapper, inputs, params)
		if execErr == nil {
			if provider, err := execCtx.Providers.Get(resolvedWrapper.Provider); err == nil {
				providerConfig := provider.GetProvider()
				if providerConfig.Version != "" {
					taskExec.ProviderVersion = providerConfig.Version
					execCtx.Versions[fmt.Sprintf("provider:%s", resolvedWrapper.Provider)] = providerConfig.Version
				}
			}
		}
	} else {
		outputs, execErr = t.executeLogic(execCtx, resolvedWrapper, inputs, params)
	}

	if execErr != nil {
		taskExec.Status = types.StatusFailed
		taskExec.Error = execErr

		if resolvedWrapper.Fallback != nil {
			return taskExec, t.handleFallback(execCtx, taskExec, ref, execErr)
		}
		return taskExec, execErr
	}

	// Publish outputs using the ORIGINAL task's bindings
	taskExec.Outputs = outputs
	t.publishOutputs(execCtx, originalTask, outputs, ref.Bind)

	now := time.Now()
	taskExec.Status = types.StatusCompleted
	taskExec.CompletedAt = &now

	t.logger.Debug().
		Str("task", originalTask.Name).
		Str("wrapper", wrapper.Task).
		Interface("outputs", outputs).
		Int64("duration", time.Since(taskExec.StartedAt).Milliseconds()).
		Msg("Task with wrapper completed successfully")

	return taskExec, nil
}

// resolveTaskInheritance resolves task inheritance.
func (t *TaskExecutor) resolveTaskInheritance(task *types.Task) (*types.Task, error) {
	if task.Inherits == "" {
		return task, nil
	}

	parentTask, ok := t.config.Tasks[task.Inherits]
	if !ok {
		return nil, fmt.Errorf("parent task %q not found for task %q", task.Inherits, task.Name)
	}

	resolvedParent, err := t.resolveTaskInheritance(parentTask)
	if err != nil {
		return nil, err
	}

	resolved := &types.Task{
		Name:        task.Name,
		Description: task.Description,
		Params:      task.Params,
		Provider:    task.Provider,
		Fallback:    task.Fallback,
		Logic:       task.Logic,
		Timeout:     task.Timeout,
		Retry:       task.Retry,
		Metadata:    make(map[string]any),
		Wrapper:     task.Wrapper,
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
	if resolved.Params == nil && resolvedParent.Params != nil {
		resolved.Params = resolvedParent.Params
	}
	if resolved.Wrapper == nil {
		resolved.Wrapper = resolvedParent.Wrapper
	}

	maps.Copy(resolved.Metadata, resolvedParent.Metadata)
	maps.Copy(resolved.Metadata, task.Metadata)

	t.logger.Debug().Str("task", task.Name).Str("parent", task.Inherits).Msg("Task inheritance resolved")

	return resolved, nil
}

// isRequired reports whether fieldName is listed in the schema's Required slice.
func isRequired(schema *jsonschema.Schema, fieldName string) bool {
	return slices.Contains(schema.Required, fieldName)
}

// schemaTypeString extracts the type string from a property schema.
func schemaTypeString(s *jsonschema.Schema) string {
	if s == nil {
		return ""
	}
	if s.Type != "" {
		return s.Type
	}
	return ""
}

// jsonTypeOfValue returns a JSON-schema-style type string for a Go runtime value.
func jsonTypeOfValue(v any) string {
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
	default:
		return "object"
	}
}
