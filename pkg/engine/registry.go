package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
)

// LogicHandler is a function that executes task logic
type LogicHandler func(ctx *types.ExecutionContext, inputs map[string]any, provider types.ProviderInstance) (map[string]any, error)

// LogicHandlerInfo contains metadata about a registered logic handler.
// InputSchema / OutputSchema are optional declarations of what the handler
// expects and emits; when present they enable schema-level validation of
// every task that references this handler.
type LogicHandlerInfo struct {
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	Version      string           `json:"version,omitempty"`
	RegisteredAt time.Time        `json:"registered_at"`
	UsedBy       []string         `json:"used_by,omitempty"` // Tasks that use this logic
	InputSchema  []types.FieldDef `json:"input_schema,omitempty"`
	OutputSchema []types.FieldDef `json:"output_schema,omitempty"`
}

// RegisterOption customizes a logic handler registration.
type RegisterOption func(*LogicHandlerInfo)

// WithDescription attaches human-readable docs to a handler.
func WithDescription(s string) RegisterOption {
	return func(i *LogicHandlerInfo) { i.Description = s }
}

// WithVersion records the handler's semantic version.
func WithVersion(s string) RegisterOption {
	return func(i *LogicHandlerInfo) { i.Version = s }
}

// WithInputSchema declares the inputs the handler expects. Every task that
// references this handler will be validated against this schema at config-load
// time and by `jigsaw check`.
func WithInputSchema(fields ...types.FieldDef) RegisterOption {
	return func(i *LogicHandlerInfo) { i.InputSchema = fields }
}

// WithOutputSchema declares the outputs the handler emits.
func WithOutputSchema(fields ...types.FieldDef) RegisterOption {
	return func(i *LogicHandlerInfo) { i.OutputSchema = fields }
}

// LogicRegistry manages task logic handlers
type LogicRegistry struct {
	handlers map[string]LogicHandler
	metadata map[string]*LogicHandlerInfo
	mu       sync.RWMutex
}

// NewLogicRegistry creates a new logic registry
func NewLogicRegistry() *LogicRegistry {
	return &LogicRegistry{
		handlers: make(map[string]LogicHandler),
		metadata: make(map[string]*LogicHandlerInfo),
	}
}

// Register registers a logic handler
func (r *LogicRegistry) Register(name string, handler LogicHandler) error {
	return r.RegisterWithMetadata(name, handler, nil)
}

// RegisterWithMetadata registers a logic handler with metadata
func (r *LogicRegistry) RegisterWithMetadata(name string, handler LogicHandler, info *LogicHandlerInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if _, exists := r.handlers[name]; exists {
		return fmt.Errorf("logic handler '%s' already registered", name)
	}
	
	r.handlers[name] = handler
	
	// Store metadata
	if info == nil {
		info = &LogicHandlerInfo{
			Name: name,
		}
	}
	info.Name = name
	info.RegisteredAt = time.Now()
	r.metadata[name] = info
	
	return nil
}

// Get retrieves a logic handler
func (r *LogicRegistry) Get(name string) (LogicHandler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	handler, exists := r.handlers[name]
	if !exists {
		return nil, fmt.Errorf("logic handler '%s' not found", name)
	}
	
	return handler, nil
}

// Has checks if a logic handler exists
func (r *LogicRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	_, exists := r.handlers[name]
	return exists
}

// List returns all registered logic handler names
func (r *LogicRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	return names
}

// MustRegister registers a logic handler and panics on error
func (r *LogicRegistry) MustRegister(name string, handler LogicHandler) {
	if err := r.Register(name, handler); err != nil {
		panic(err)
	}
}

// GetInfo returns metadata about a logic handler
func (r *LogicRegistry) GetInfo(name string) (*LogicHandlerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	info, exists := r.metadata[name]
	if !exists {
		return nil, fmt.Errorf("logic handler '%s' not found", name)
	}
	
	return info, nil
}

// ListWithInfo returns all registered logic handlers with their metadata
func (r *LogicRegistry) ListWithInfo() []*LogicHandlerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	infos := make([]*LogicHandlerInfo, 0, len(r.metadata))
	for _, info := range r.metadata {
		infos = append(infos, info)
	}
	return infos
}

// ValidateConfig validates that all logic handlers required by config are registered
func (r *LogicRegistry) ValidateConfig(config *types.Config) []ValidationError {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	var errors []ValidationError
	usageMap := make(map[string][]string) // logic -> tasks that use it
	
	// Check all tasks for logic handlers
	for taskName, task := range config.Tasks {
		if task.Logic == "" {
			continue
		}
		
		// Track usage
		usageMap[task.Logic] = append(usageMap[task.Logic], taskName)
		
		// Check if handler exists
		if _, exists := r.handlers[task.Logic]; !exists {
			errors = append(errors, ValidationError{
				Type:    "missing_logic_handler",
				Logic:   task.Logic,
				Task:    taskName,
				Message: fmt.Sprintf("Logic handler '%s' required by task '%s' is not registered", task.Logic, taskName),
			})
		}
	}
	
	// Update metadata with usage information
	for logic, tasks := range usageMap {
		if info, exists := r.metadata[logic]; exists {
			info.UsedBy = tasks
		}
	}
	
	return errors
}

// ValidationError represents a logic validation error
type ValidationError struct {
	Type    string `json:"type"`
	Logic   string `json:"logic"`
	Task    string `json:"task,omitempty"`
	Message string `json:"message"`
}
