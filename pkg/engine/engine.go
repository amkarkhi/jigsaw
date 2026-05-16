package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
)

// Engine is the main execution engine for flows and tasks
type Engine struct {
	config        *types.Config
	executor      *FlowExecutor
	validator     types.Validator
	logger        types.Logger
	logicRegistry *LogicRegistry
}

// New creates a new engine instance
func New(config *types.Config, validator types.Validator, logger types.Logger) *Engine {
	logicRegistry := NewLogicRegistry()
	executor := NewFlowExecutor(config, logger, logicRegistry)
	
	return &Engine{
		config:        config,
		executor:      executor,
		validator:     validator,
		logger:        logger,
		logicRegistry: logicRegistry,
	}
}

// RegisterLogic registers a custom logic handler. Optional RegisterOption
// arguments attach metadata: description, version, input/output schemas.
func (e *Engine) RegisterLogic(name string, handler LogicHandler, opts ...RegisterOption) error {
	if len(opts) == 0 {
		return e.logicRegistry.Register(name, handler)
	}
	info := &LogicHandlerInfo{Name: name}
	for _, opt := range opts {
		opt(info)
	}
	return e.logicRegistry.RegisterWithMetadata(name, handler, info)
}

// MustRegisterLogic registers a custom logic handler and panics on error.
func (e *Engine) MustRegisterLogic(name string, handler LogicHandler, opts ...RegisterOption) {
	if err := e.RegisterLogic(name, handler, opts...); err != nil {
		panic(err)
	}
}

// ListLogicHandlers returns all registered logic handler names
func (e *Engine) ListLogicHandlers() []string {
	return e.logicRegistry.List()
}

// ListLogicHandlersWithInfo returns all registered logic handlers with metadata
func (e *Engine) ListLogicHandlersWithInfo() []*LogicHandlerInfo {
	return e.logicRegistry.ListWithInfo()
}

// GetLogicHandlerInfo returns metadata about a specific logic handler
func (e *Engine) GetLogicHandlerInfo(name string) (*LogicHandlerInfo, error) {
	return e.logicRegistry.GetInfo(name)
}

// ValidateLogicHandlers validates that all required logic handlers are registered
func (e *Engine) ValidateLogicHandlers() []ValidationError {
	return e.logicRegistry.ValidateConfig(e.config)
}

// HasLogicHandler checks if a logic handler is registered
func (e *Engine) HasLogicHandler(name string) bool {
	return e.logicRegistry.Has(name)
}

// ExecuteFlow executes a flow by name
func (e *Engine) ExecuteFlow(ctx context.Context, flowName string, sub int, params map[string]any, headers map[string]string, providers types.ProviderRegistry) (*types.ExecutionResult, error) {
	startTime := time.Now()
	
	// Get flow
	flow, ok := e.config.Flows[flowName]
	if !ok {
		return nil, fmt.Errorf("flow '%s' not found", flowName)
	}
	
	// Create execution context
	execCtx := jigsawctx.New(ctx, flowName, sub, params, headers)
	execCtx = jigsawctx.WithProviders(execCtx, providers)
	execCtx = jigsawctx.WithLogger(execCtx, e.logger)
	
	e.logger.Info("Starting flow execution", map[string]any{
		"flow":       flowName,
		"request_id": execCtx.RequestID,
		"sub":        sub,
		"tag":        execCtx.Tag,
	})
	
	// Execute flow
	flowExec, err := e.executor.Execute(execCtx, flow)
	
	executionTime := time.Since(startTime).Milliseconds()
	
	// Build result
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
		
		logFields := map[string]any{
			"flow":           flowName,
			"request_id":     execCtx.RequestID,
			"execution_time": executionTime,
		}
		if execCtx.FlowVersion != "" {
			logFields["flow_version"] = execCtx.FlowVersion
		}
		e.logger.Error("Flow execution failed", err, logFields)
		
		return result, err
	}
	
	result.Status = "success"
	result.Data = execCtx.LastOutput
	
	logFields := map[string]any{
		"flow":           flowName,
		"request_id":     execCtx.RequestID,
		"execution_time": executionTime,
		"tasks_executed": len(flowExec.Tasks),
	}
	if execCtx.FlowVersion != "" {
		logFields["flow_version"] = execCtx.FlowVersion
	}
	if len(execCtx.Versions) > 0 {
		logFields["versions"] = execCtx.Versions
	}
	e.logger.Info("Flow execution completed", logFields)
	
	return result, nil
}

// GetFlow returns a flow by name
func (e *Engine) GetFlow(name string) (*types.Flow, error) {
	flow, ok := e.config.Flows[name]
	if !ok {
		return nil, fmt.Errorf("flow '%s' not found", name)
	}
	return flow, nil
}

// GetTask returns a task by name
func (e *Engine) GetTask(name string) (*types.Task, error) {
	task, ok := e.config.Tasks[name]
	if !ok {
		return nil, fmt.Errorf("task '%s' not found", name)
	}
	return task, nil
}

// ListFlows returns all available flows
func (e *Engine) ListFlows() []string {
	flows := make([]string, 0, len(e.config.Flows))
	for name := range e.config.Flows {
		flows = append(flows, name)
	}
	return flows
}

// ListTasks returns all available tasks
func (e *Engine) ListTasks() []string {
	tasks := make([]string, 0, len(e.config.Tasks))
	for name := range e.config.Tasks {
		tasks = append(tasks, name)
	}
	return tasks
}
