package engine

import (
	"context"
	"fmt"
	"maps"
	"time"

	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// Engine is the main execution engine for flows and tasks.
type Engine struct {
	config        *types.Config
	executor      *FlowExecutor
	validator     types.Validator
	logger        zerolog.Logger
	logicRegistry *logicRegistry
}

// New creates a new engine instance.
func New(config *types.Config, validator types.Validator, logger zerolog.Logger) *Engine {
	reg := newLogicRegistry()
	executor := NewFlowExecutor(config, logger, reg)

	return &Engine{
		config:        config,
		executor:      executor,
		validator:     validator,
		logger:        logger,
		logicRegistry: reg,
	}
}

// Config returns the engine's config. Read-only — mutating the returned
// value will corrupt running flow executions.
func (e *Engine) Config() *types.Config {
	return e.config
}

// UpdateConfig swaps the engine's config and rebuilds the FlowExecutor
// against it. The logic-handler registry is preserved — handlers
// registered before reload remain available, so logic handlers do NOT
// need to be re-registered after a hot reload. In-flight executions hold
// their own reference to the previous executor and complete safely;
// requests that arrive after the swap use the new config.
func (e *Engine) UpdateConfig(cfg *types.Config) {
	newExec := NewFlowExecutor(cfg, e.logger, e.logicRegistry)
	// Aligned-pointer writes are atomic on Go's supported 64-bit targets;
	// readers either see the old pair or the new pair, never a torn mix.
	e.executor = newExec
	e.config = cfg
}

// FlowExecutor returns the engine's own flow executor (the one used by
// ExecuteFlow). It is bound to the engine's config and logic registry.
func (e *Engine) FlowExecutor() *FlowExecutor {
	return e.executor
}

// FlowExecutorFor returns a FlowExecutor that uses the engine's logic
// registry but resolves tasks against the supplied config. Use this when
// callers (e.g. the playground) need to run a synthetic config — say a
// one-task flow with an injected task definition — through the real
// handlers without mutating the engine's own config.
func (e *Engine) FlowExecutorFor(cfg *types.Config) *FlowExecutor {
	return NewFlowExecutor(cfg, e.logger, e.logicRegistry)
}

// ListLogicHandlers returns all registered logic handler names.
func (e *Engine) ListLogicHandlers() []string {
	return e.logicRegistry.list()
}

// ListLogicHandlersWithInfo returns all registered logic handlers with metadata.
func (e *Engine) ListLogicHandlersWithInfo() []*LogicHandlerInfo {
	return e.logicRegistry.listWithInfo()
}

// GetLogicHandlerInfo returns metadata about a specific logic handler.
func (e *Engine) GetLogicHandlerInfo(name string) (*LogicHandlerInfo, error) {
	return e.logicRegistry.getInfo(name)
}

// ValidateLogicHandlers validates that all required logic handlers are registered.
func (e *Engine) ValidateLogicHandlers() []ValidationError {
	return e.logicRegistry.validateConfig(e.config)
}

// HasLogicHandler checks if a logic handler is registered.
func (e *Engine) HasLogicHandler(name string) bool {
	return e.logicRegistry.has(name)
}

// ValidateFlows performs a post-registration flow-level validation pass: it
// walks every flow's task list and verifies that each task's bind.in sources
// exist in the simulated scope and that bind.out renames do not produce type
// conflicts. This must be called after all handlers are registered.
func (e *Engine) ValidateFlows() error {
	return validateFlows(e.config, e.logicRegistry)
}

// ExecuteFlow executes a flow by name.
func (e *Engine) ExecuteFlow(ctx context.Context, flowName string, sub int, params map[string]any, headers map[string]string, providers types.ProviderRegistry) (*types.ExecutionResult, error) {
	startTime := time.Now()

	flow, ok := e.config.Flows[flowName]
	if !ok {
		return nil, fmt.Errorf("flow %q not found", flowName)
	}

	execCtx := jigsawctx.New(ctx, flowName, sub, params, headers)
	execCtx = jigsawctx.WithProviders(execCtx, providers)
	execCtx = jigsawctx.WithLogger(execCtx, e.logger)
	execCtx.Engine = e

	e.logger.Info().Str("flow", flowName).Str("request_id", execCtx.RequestID).Int("sub", sub).Str("tag", execCtx.Tag).Msg("Starting flow execution")

	flowExec, err := e.executor.Execute(execCtx, flow)

	executionTime := time.Since(startTime).Milliseconds()

	// Collect all scope vars as the final result data.
	scopeSnapshot := make(map[string]any, len(execCtx.Scope))
	for k, sv := range execCtx.Scope {
		scopeSnapshot[k] = sv.Value
	}

	result := &types.ExecutionResult{
		RequestID:     execCtx.RequestID,
		FlowName:      flowName,
		FlowVersion:   execCtx.FlowVersion,
		ExecutionTime: executionTime,
		Versions:      execCtx.Versions,
		Metadata: map[string]any{
			"tasks_executed": len(flowExec.Tasks),
		},
	}

	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()

		failedEvt := e.logger.Error().Err(err).Str("flow", flowName).Str("request_id", execCtx.RequestID).Int64("execution_time", executionTime)
		if execCtx.FlowVersion != "" {
			failedEvt = failedEvt.Str("flow_version", execCtx.FlowVersion)
		}
		failedEvt.Msg("Flow execution failed")

		return result, err
	}

	result.Status = "success"
	result.Data = scopeSnapshot
	// The terminal task in the flow's `tasks:` list owns the HTTP response
	// shape: its Outputs become Data so the body matches the response task's
	// schema directly. Per-task traces still expose every step's outputs.
	if last := terminalTask(flow); last != "" {
		for _, te := range flowExec.Tasks {
			if te.Task != nil && te.Task.Name == last && te.Outputs != nil {
				result.Data = te.Outputs
				break
			}
		}
	}

	completedEvt := e.logger.Info().Str("flow", flowName).Str("request_id", execCtx.RequestID).Int64("execution_time", executionTime).Int("tasks_executed", len(flowExec.Tasks))
	if execCtx.FlowVersion != "" {
		completedEvt = completedEvt.Str("flow_version", execCtx.FlowVersion)
	}
	if len(execCtx.Versions) > 0 {
		completedEvt = completedEvt.Interface("versions", execCtx.Versions)
	}
	completedEvt.Msg("Flow execution completed")

	return result, nil
}

// terminalTask returns the name of the flow's final task — the one whose
// outputs are the HTTP response body. Parallel blocks are skipped (no
// single output owner); we walk backwards through the top-level Tasks
// list and return the first plain task ref we find. Returns "" if the
// flow ends in a parallel block or has no named terminal task.
func terminalTask(flow *types.Flow) string {
	if flow == nil {
		return ""
	}
	for i := len(flow.Tasks) - 1; i >= 0; i-- {
		if flow.Tasks[i].Name != "" {
			return flow.Tasks[i].Name
		}
	}
	return ""
}

// GetFlow returns a flow by name.
func (e *Engine) GetFlow(name string) (*types.Flow, error) {
	flow, ok := e.config.Flows[name]
	if !ok {
		return nil, fmt.Errorf("flow %q not found", name)
	}
	return flow, nil
}

// GetTask returns a task by name.
func (e *Engine) GetTask(name string) (*types.Task, error) {
	task, ok := e.config.Tasks[name]
	if !ok {
		return nil, fmt.Errorf("task %q not found", name)
	}
	return task, nil
}

// ListFlows returns all available flows.
func (e *Engine) ListFlows() []string {
	flows := make([]string, 0, len(e.config.Flows))
	for name := range e.config.Flows {
		flows = append(flows, name)
	}
	return flows
}

// Invoke dispatches a registered logic handler by name outside of the normal
// task-execution path. It is intended for use by wrapper handlers (caches,
// retries, multi-phase orchestrators) that need to call another logic from
// inside their own Run. The handler's bind.in / bind.out wiring does not
// apply — caller is responsible for shaping inputs and reading outputs.
//
// The output map is the post-marshal representation the engine would publish
// to scope, so cached payloads round-trip byte-identical to uncached ones.
// Invoke does not apply the inner logic's fallback policy; callers decide how
// to handle errors.
func (e *Engine) Invoke(ctx *types.ExecutionContext, name string, inputs map[string]any, params map[string]any, provider types.ProviderInstance) (map[string]any, error) {
	handler, err := e.logicRegistry.get(name)
	if err != nil {
		return nil, err
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	if params == nil {
		params = map[string]any{}
	}
	return handler.Execute(ctx, inputs, params, provider)
}

// InvokeTask dispatches a registered *task* by name. Unlike Invoke (which
// targets a logic), this resolves the task definition — applying inheritance,
// param defaults, and provider lookup — then runs through the same JSON
// round-trip the executor uses. bind.in / bind.out wiring does NOT apply:
// the caller passes raw inputs and receives raw outputs.
//
// paramOverrides are shallow-merged on top of the (inherited) task params.
// The task's fallback policy is NOT applied — callers decide how to handle
// errors so wrapper logics retain full control.
//
// While the inner task runs, ctx.Nested is rebound to the inner's declared
// nested ref (and restored on return), so chains of orchestrator logics see
// their own nested config.
func (e *Engine) InvokeTask(ctx *types.ExecutionContext, name string, inputs map[string]any, paramOverrides map[string]any) (map[string]any, error) {
	task, ok := e.config.Tasks[name]
	if !ok {
		return nil, fmt.Errorf("task %q not found", name)
	}

	exec := NewTaskExecutor(e.config, e.logger, e.logicRegistry)
	resolved, err := exec.resolveTaskInheritance(task)
	if err != nil {
		return nil, err
	}

	// Flow-level overrides recorded on ctx.Nested.Params (by executeWithWrapper)
	// already represent task defaults merged with the flow TaskRef's params, so
	// they should reach the inner task. Explicit paramOverrides from the caller
	// still win on top.
	var nestedParams map[string]any
	if ctx.Nested != nil && ctx.Nested.Task == name {
		nestedParams = ctx.Nested.Params
	}
	overlay := types.ParamOverlayForTask(ctx.Context, name)
	params := make(map[string]any, len(resolved.Params)+len(nestedParams)+len(paramOverrides)+len(overlay))
	maps.Copy(params, resolved.Params)
	maps.Copy(params, nestedParams)
	maps.Copy(params, paramOverrides)
	maps.Copy(params, overlay)

	if inputs == nil {
		inputs = map[string]any{}
	}

	// Detect whether the caller is already a wrapper currently wrapping THIS
	// task — in that case we must NOT re-apply task.Wrapper, or the wrapper
	// would recurse into itself. The pre-existing `ctx.Nested.Task == name`
	// signal is exactly that marker: it's set by executeWithWrapper /
	// invokeTaskThroughWrapper before handing control to the wrapper logic.
	alreadyWrappedByCaller := ctx.Nested != nil && ctx.Nested.Task == name

	// Clear ctx.Nested while the inner runs: ctx.Nested is meaningful only to
	// the wrapper logic that asked us to dispatch this task. The inner
	// shouldn't see its wrapper's Nested. Restore on return so the wrapper
	// can keep using it after we hand control back.
	prevNested := ctx.Nested
	ctx.Nested = nil
	defer func() { ctx.Nested = prevNested }()

	// Wrapper composition: if the dispatched inner task itself declares a
	// Wrapper AND the caller isn't already wrapping us, run *its* wrapper
	// around the actual logic. This is what makes a flow-step wrapper + task
	// wrapper compose — the outer wrapper calls InvokeTask, and here we
	// re-apply the inner task's own wrapper.
	if !alreadyWrappedByCaller && resolved.Wrapper != nil && resolved.Wrapper.Task != "" {
		return e.invokeTaskThroughWrapper(ctx, exec, resolved, resolved.Wrapper, inputs, params)
	}

	if resolved.Provider != "" {
		return exec.executeWithProvider(ctx, resolved, inputs, params)
	}
	return exec.executeLogic(ctx, resolved, inputs, params)
}

// invokeTaskThroughWrapper dispatches an inner task via its declared Wrapper.
// Mirrors TaskExecutor.executeWithWrapper but skips bind I/O (InvokeTask is
// raw-map-in / raw-map-out) so it composes cleanly when a wrapper logic calls
// ctx.Engine.InvokeTask on a task that itself has a Wrapper.
func (e *Engine) invokeTaskThroughWrapper(
	ctx *types.ExecutionContext,
	exec *TaskExecutor,
	innerTask *types.Task,
	wrapper *types.WrapperRef,
	inputs map[string]any,
	innerParams map[string]any,
) (map[string]any, error) {
	wrapperTask, ok := e.config.Tasks[wrapper.Task]
	if !ok {
		return nil, fmt.Errorf("wrapper task %q not found for task %q", wrapper.Task, innerTask.Name)
	}
	resolvedWrapper, err := exec.resolveTaskInheritance(wrapperTask)
	if err != nil {
		return nil, err
	}

	// Wrapper params: wrapper defaults, then inner task defaults, then the
	// wrapper ref's own params, then the inner params already resolved by the
	// caller (flow ref + overrides). Matches executeWithWrapper layering.
	params := make(map[string]any, len(resolvedWrapper.Params)+len(innerTask.Params)+len(wrapper.Params)+len(innerParams))
	maps.Copy(params, resolvedWrapper.Params)
	maps.Copy(params, innerTask.Params)
	if wrapper.Params != nil {
		maps.Copy(params, wrapper.Params)
	}
	maps.Copy(params, innerParams)

	prevNested := ctx.Nested
	ctx.Nested = &types.WrapperRef{
		Task:   innerTask.Name,
		Params: innerParams,
	}
	defer func() { ctx.Nested = prevNested }()

	if resolvedWrapper.Provider != "" {
		return exec.executeWithProvider(ctx, resolvedWrapper, inputs, params)
	}
	return exec.executeLogic(ctx, resolvedWrapper, inputs, params)
}

// ListTasks returns all available tasks.
func (e *Engine) ListTasks() []string {
	tasks := make([]string, 0, len(e.config.Tasks))
	for name := range e.config.Tasks {
		tasks = append(tasks, name)
	}
	return tasks
}
