package engine

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/invopop/jsonschema"
)

// LogicMeta describes the identity of a logic.
type LogicMeta struct {
	Name        string
	Description string
	Version     string
}

// Logic is the canonical no-provider logic contract.
type Logic[I, O, P any] interface {
	LogicMeta() LogicMeta
	Run(ctx *types.ExecutionContext, in I, params P) (*O, error)
}

// ProviderLogic is the canonical provider-aware logic contract.
type ProviderLogic[I, O, P any] interface {
	LogicMeta() LogicMeta
	Run(ctx *types.ExecutionContext, in I, params P, prov types.ProviderInstance) (*O, error)
}

// LogicHandlerInfo contains metadata about a registered logic handler. It is
// JSON-encodable so /api/_logic keeps working.
type LogicHandlerInfo struct {
	Name         string             `json:"name"`
	Description  string             `json:"description,omitempty"`
	Version      string             `json:"version,omitempty"`
	RegisteredAt time.Time          `json:"registered_at"`
	UsedBy       []string           `json:"used_by,omitempty"`
	InputSchema  *jsonschema.Schema `json:"input_schema,omitempty"`
	OutputSchema *jsonschema.Schema `json:"output_schema,omitempty"`
	ParamsSchema *jsonschema.Schema `json:"params_schema,omitempty"`
}

// logicHandler is the interface every internal handler wrapper must satisfy.
type logicHandler interface {
	Meta() LogicMeta
	InputSchema() *jsonschema.Schema
	OutputSchema() *jsonschema.Schema
	ParamsSchema() *jsonschema.Schema
	Execute(ctx *types.ExecutionContext, inputs map[string]any, params map[string]any,
		provider types.ProviderInstance) (map[string]any, error)
}

// logicRegistry manages task logic handlers.
type logicRegistry struct {
	handlers map[string]logicHandler
	metadata map[string]*LogicHandlerInfo
	mu       sync.RWMutex
}

// newLogicRegistry creates a new logic registry.
func newLogicRegistry() *logicRegistry {
	return &logicRegistry{
		handlers: make(map[string]logicHandler),
		metadata: make(map[string]*LogicHandlerInfo),
	}
}

// register registers a logicHandler by its Meta().Name.
func (r *logicRegistry) register(handler logicHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	meta := handler.Meta()
	name := meta.Name
	if _, exists := r.handlers[name]; exists {
		return fmt.Errorf("logic handler %q already registered", name)
	}

	r.handlers[name] = handler
	r.metadata[name] = &LogicHandlerInfo{
		Name:         name,
		Description:  meta.Description,
		Version:      meta.Version,
		RegisteredAt: time.Now(),
		InputSchema:  handler.InputSchema(),
		OutputSchema: handler.OutputSchema(),
		ParamsSchema: handler.ParamsSchema(),
	}
	return nil
}

// get retrieves a logic handler by name.
func (r *logicRegistry) get(name string) (logicHandler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h, ok := r.handlers[name]
	if !ok {
		return nil, fmt.Errorf("logic handler %q not found", name)
	}
	return h, nil
}

// has reports whether a handler with the given name is registered.
func (r *logicRegistry) has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[name]
	return ok
}

// list returns all registered handler names.
func (r *logicRegistry) list() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.handlers))
	for n := range r.handlers {
		names = append(names, n)
	}
	return names
}

// getInfo returns metadata about a registered handler.
func (r *logicRegistry) getInfo(name string) (*LogicHandlerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.metadata[name]
	if !ok {
		return nil, fmt.Errorf("logic handler %q not found", name)
	}
	return info, nil
}

// listWithInfo returns metadata for all registered handlers.
func (r *logicRegistry) listWithInfo() []*LogicHandlerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*LogicHandlerInfo, 0, len(r.metadata))
	for _, info := range r.metadata {
		out = append(out, info)
	}
	return out
}

// validateConfig validates that all logic handlers required by config are registered.
func (r *logicRegistry) validateConfig(config *types.Config) []ValidationError {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var errs []ValidationError
	usageMap := make(map[string][]string)

	for taskName, task := range config.Tasks {
		if task.Logic == "" {
			continue
		}
		usageMap[task.Logic] = append(usageMap[task.Logic], taskName)
		if _, ok := r.handlers[task.Logic]; !ok {
			errs = append(errs, ValidationError{
				Type:    "missing_logic_handler",
				Logic:   task.Logic,
				Task:    taskName,
				Message: fmt.Sprintf("logic handler %q required by task %q is not registered", task.Logic, taskName),
			})
		}
	}

	for logic, tasks := range usageMap {
		if info, ok := r.metadata[logic]; ok {
			info.UsedBy = tasks
		}
	}
	return errs
}

// ValidationError represents a logic validation error.
type ValidationError struct {
	Type    string `json:"type"`
	Logic   string `json:"logic"`
	Task    string `json:"task,omitempty"`
	Message string `json:"message"`
}

// =====================================================================
// Generic typed wrappers — no reflection on the hot path
// =====================================================================

type typedHandler[I, O, P any] struct {
	meta         LogicMeta
	inputSchema  *jsonschema.Schema
	outputSchema *jsonschema.Schema
	paramsSchema *jsonschema.Schema
	logic        Logic[I, O, P]
}

func (h *typedHandler[I, O, P]) Meta() LogicMeta                  { return h.meta }
func (h *typedHandler[I, O, P]) InputSchema() *jsonschema.Schema  { return h.inputSchema }
func (h *typedHandler[I, O, P]) OutputSchema() *jsonschema.Schema { return h.outputSchema }
func (h *typedHandler[I, O, P]) ParamsSchema() *jsonschema.Schema { return h.paramsSchema }

func (h *typedHandler[I, O, P]) Execute(
	ctx *types.ExecutionContext,
	inputs map[string]any,
	params map[string]any,
	_ types.ProviderInstance,
) (map[string]any, error) {
	var in I
	if err := roundTrip(inputs, &in); err != nil {
		return nil, fmt.Errorf("handler %q: decode inputs: %w", h.meta.Name, err)
	}
	var p P
	if err := roundTrip(params, &p); err != nil {
		return nil, fmt.Errorf("handler %q: decode params: %w", h.meta.Name, err)
	}
	out, err := h.logic.Run(ctx, in, p)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("handler %q: nil output without error", h.meta.Name)
	}
	var result map[string]any
	if err := roundTrip(*out, &result); err != nil {
		return nil, fmt.Errorf("handler %q: encode outputs: %w", h.meta.Name, err)
	}
	return result, nil
}

type providerTypedHandler[I, O, P any] struct {
	meta         LogicMeta
	inputSchema  *jsonschema.Schema
	outputSchema *jsonschema.Schema
	paramsSchema *jsonschema.Schema
	logic        ProviderLogic[I, O, P]
}

func (h *providerTypedHandler[I, O, P]) Meta() LogicMeta                  { return h.meta }
func (h *providerTypedHandler[I, O, P]) InputSchema() *jsonschema.Schema  { return h.inputSchema }
func (h *providerTypedHandler[I, O, P]) OutputSchema() *jsonschema.Schema { return h.outputSchema }
func (h *providerTypedHandler[I, O, P]) ParamsSchema() *jsonschema.Schema { return h.paramsSchema }

func (h *providerTypedHandler[I, O, P]) Execute(
	ctx *types.ExecutionContext,
	inputs map[string]any,
	params map[string]any,
	prov types.ProviderInstance,
) (map[string]any, error) {
	var in I
	if err := roundTrip(inputs, &in); err != nil {
		return nil, fmt.Errorf("handler %q: decode inputs: %w", h.meta.Name, err)
	}
	var p P
	if err := roundTrip(params, &p); err != nil {
		return nil, fmt.Errorf("handler %q: decode params: %w", h.meta.Name, err)
	}
	out, err := h.logic.Run(ctx, in, p, prov)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("handler %q: nil output without error", h.meta.Name)
	}
	var result map[string]any
	if err := roundTrip(*out, &result); err != nil {
		return nil, fmt.Errorf("handler %q: encode outputs: %w", h.meta.Name, err)
	}
	return result, nil
}

// =====================================================================
// Public registration API
// =====================================================================

// Register registers l under its LogicMeta.Name. Returns an error if the name
// is empty or already taken.
func Register[I, O, P any](eng *Engine, l Logic[I, O, P]) error {
	meta := l.LogicMeta()
	if meta.Name == "" {
		return fmt.Errorf("Register: LogicMeta.Name must not be empty")
	}
	h := &typedHandler[I, O, P]{
		meta:         meta,
		inputSchema:  reflectSchema[I](),
		outputSchema: reflectSchema[O](),
		paramsSchema: reflectSchema[P](),
		logic:        l,
	}
	return eng.logicRegistry.register(h)
}

// MustRegister is like Register but panics on error.
func MustRegister[I, O, P any](eng *Engine, l Logic[I, O, P]) {
	if err := Register(eng, l); err != nil {
		panic(err)
	}
}

// RegisterWithProvider registers a provider-aware logic under its LogicMeta.Name.
func RegisterWithProvider[I, O, P any](eng *Engine, l ProviderLogic[I, O, P]) error {
	meta := l.LogicMeta()
	if meta.Name == "" {
		return fmt.Errorf("RegisterWithProvider: LogicMeta.Name must not be empty")
	}
	h := &providerTypedHandler[I, O, P]{
		meta:         meta,
		inputSchema:  reflectSchema[I](),
		outputSchema: reflectSchema[O](),
		paramsSchema: reflectSchema[P](),
		logic:        l,
	}
	return eng.logicRegistry.register(h)
}

// MustRegisterWithProvider is like RegisterWithProvider but panics on error.
func MustRegisterWithProvider[I, O, P any](eng *Engine, l ProviderLogic[I, O, P]) {
	if err := RegisterWithProvider(eng, l); err != nil {
		panic(err)
	}
}

// =====================================================================
// Internal helpers
// =====================================================================

// roundTrip marshals src to JSON then unmarshals into dst.
func roundTrip(src, dst any) error {
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// reflectSchema returns the JSON schema for a zero value of type T.
func reflectSchema[T any]() *jsonschema.Schema {
	var zero T
	r := jsonschema.Reflector{
		DoNotReference: true,
	}
	return r.Reflect(zero)
}
