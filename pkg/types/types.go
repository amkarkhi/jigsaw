package types

import (
	"context"
	"time"
)

// =====================================================================
// CONFIGURATION TYPES
// =====================================================================

// Config holds all configuration for the Jigsaw engine
type Config struct {
	Tasks     map[string]*Task
	Flows     map[string]*Flow
	Providers map[string]*Provider
	Endpoints map[string]*Endpoint
}

// Endpoint defines an HTTP route that maps to flows
type Endpoint struct {
	Name        string              `yaml:"name"`
	Path        string              `yaml:"path"`
	Method      string              `yaml:"method"`
	Description string              `yaml:"description"`
	Flows       []FlowMapping       `yaml:"flows"`
	Metadata    map[string]any      `yaml:"metadata,omitempty"`
}

// FlowMapping maps sub parameter to a specific flow
type FlowMapping struct {
	Sub      int    `yaml:"sub"`       // Direct sub -> flow mapping
	FlowName string `yaml:"flow_name"` // Target flow name
}

// Flow defines a sequence of tasks to execute
type Flow struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Version     string       `yaml:"version,omitempty"`     // Semantic version (e.g., "1.0.0", "2.1.3")
	Inherits    string       `yaml:"inherits,omitempty"`    // Parent flow to inherit from
	Tasks       []TaskRef    `yaml:"tasks"`                 // Ordered list of tasks
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// TaskRef can be a simple task name or complex task with overrides
type TaskRef struct {
	Name      string           `yaml:"name"`
	Overrides []TaskOverride   `yaml:"overrides,omitempty"`
	Parallel  []TaskRef        `yaml:"parallel,omitempty"` // For parallel execution
}

// TaskOverride defines conditional task execution changes
type TaskOverride struct {
	Condition map[string]any `yaml:"condition"` // e.g., {tag: "premium"}
	Action    string         `yaml:"action"`    // skip, replace
	Task      string         `yaml:"task,omitempty"` // Replacement task name
}

// Task defines a unit of work
type Task struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version,omitempty"`    // Semantic version (e.g., "1.0.0", "2.1.3")
	Inherits    string         `yaml:"inherits,omitempty"`
	Inputs      []FieldDef     `yaml:"inputs"`
	Outputs     []FieldDef     `yaml:"outputs"`
	Provider    string         `yaml:"provider,omitempty"`
	Fallback    *Fallback      `yaml:"fallback,omitempty"`
	Logic       string         `yaml:"logic"`
	Timeout     int            `yaml:"timeout,omitempty"`  // milliseconds
	Retry       int            `yaml:"retry,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// FieldDef defines input/output field definition
type FieldDef struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"` // string, int, bool, object, array, any
	Required   bool   `yaml:"required"`
	Default    any    `yaml:"default,omitempty"`
	Validation string `yaml:"validation,omitempty"`
}

// Fallback defines error handling strategy
type Fallback struct {
	Strategy      string         `yaml:"strategy"` // abort, continue, switch_task, switch_provider
	Message       string         `yaml:"message,omitempty"`
	Defaults      map[string]any `yaml:"defaults,omitempty"`      // For continue strategy
	TargetTask    string         `yaml:"target_task,omitempty"`   // For switch_task
	Providers     []string       `yaml:"providers,omitempty"`     // For switch_provider
	RetryCount    int            `yaml:"retry_count,omitempty"`
	RetryDelay    int            `yaml:"retry_delay,omitempty"` // milliseconds
}

// Provider defines external service configuration
type Provider struct {
	Name     string         `yaml:"name"`
	Type     string         `yaml:"type"` // cache, database, search_engine, http, etc.
	Version  string         `yaml:"version,omitempty"` // Provider version (e.g., "cache:1.0", "database:2.0")
	Config   map[string]any `yaml:"config"`
	InitMode string         `yaml:"init_mode"` // lazy, eager, pooled
	PoolSize int            `yaml:"pool_size,omitempty"`
	Metadata map[string]any `yaml:"metadata,omitempty"`
}

// =====================================================================
// RUNTIME TYPES
// =====================================================================

// ExecutionContext carries data through flow execution
type ExecutionContext struct {
	RequestID      string                 // Unique request identifier
	FlowName       string                 // Current flow name
	FlowVersion    string                 // Version of the flow being executed
	CurrentTask    string                 // Current task name
	Sub            int                    // Sub parameter (flow selector)
	Tag            string                 // Tag for task overrides
	Parameters     map[string]any         // Request parameters
	Headers        map[string]string      // HTTP headers
	TaskOutputs    map[string]any         // All task outputs (keyed by task name)
	LastOutput     any                    // Output from previous task
	Metadata       map[string]any         // Additional runtime metadata
	Versions       map[string]string      // Version tracking: task_name -> version
	Providers      ProviderRegistry       // Provider registry interface
	Logger         Logger                 // Logger interface
	Context        context.Context        // Go context for cancellation
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// GetScopedData returns data accessible to the current task
// Tasks can only access their defined inputs from the context
func (e *ExecutionContext) GetScopedData(task *Task) map[string]any {
	data := make(map[string]any)
	
	// Add last output
	data["_last_output"] = e.LastOutput
	
	// Add specific task outputs if they exist
	// Task outputs are stored by task name, so we need to search through all task outputs
	// to find the matching output field names
	for _, input := range task.Inputs {
		// First check if it's in the parameters (from request)
		if val, ok := e.Parameters[input.Name]; ok {
			data[input.Name] = val
			continue
		}
		
		// Then search through all task outputs for this field
		for _, taskOutput := range e.TaskOutputs {
			if outputMap, ok := taskOutput.(map[string]any); ok {
				if val, ok := outputMap[input.Name]; ok {
					data[input.Name] = val
					break
				}
			}
		}
	}
	
	// Add metadata keys if referenced in inputs
	data["_metadata"] = e.Metadata
	data["_tag"] = e.Tag
	data["_sub"] = e.Sub
	
	return data
}

// SetTaskOutput stores task output in context
func (e *ExecutionContext) SetTaskOutput(taskName string, output map[string]any) {
	e.TaskOutputs[taskName] = output
	e.LastOutput = output
	e.UpdatedAt = time.Now()
}

// FlowExecution represents a flow execution instance
type FlowExecution struct {
	Flow        *Flow
	Context     *ExecutionContext
	Status      ExecutionStatus
	CurrentStep int
	StartedAt   time.Time
	CompletedAt *time.Time
	Error       error
	Tasks       []*TaskExecution
}

// TaskExecution represents a task execution instance
type TaskExecution struct {
	Task            *Task
	ActualTask      *Task              // May differ if overridden
	Inputs          map[string]any
	Outputs         map[string]any
	Status          ExecutionStatus
	StartedAt       time.Time
	CompletedAt     *time.Time
	Error           error
	FallbackUsed    bool
	RetryCount      int
	ProviderUsed    string
	ProviderVersion string             // Version of provider used
	TaskVersion     string             // Version of task executed
	LogicVersion    string             // Version of logic handler (if available)
	Skipped         bool
}

// ExecutionStatus represents execution state
type ExecutionStatus string

const (
	StatusPending   ExecutionStatus = "pending"
	StatusRunning   ExecutionStatus = "running"
	StatusCompleted ExecutionStatus = "completed"
	StatusFailed    ExecutionStatus = "failed"
	StatusSkipped   ExecutionStatus = "skipped"
)

// =====================================================================
// INTERFACES
// =====================================================================

// TaskExecutor executes task logic
type TaskExecutor interface {
	Execute(ctx *ExecutionContext, task *Task, inputs map[string]any) (map[string]any, error)
}

// ProviderRegistry manages provider instances
type ProviderRegistry interface {
	Get(name string) (ProviderInstance, error)
	Register(name string, instance ProviderInstance) error
	Close() error
}

// ProviderInstance represents a connected provider
type ProviderInstance interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	IsConnected() bool
	GetConnection() any
	GetProvider() *Provider
}

// Logger defines logging interface
type Logger interface {
	Debug(msg string, fields map[string]any)
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
	Error(msg string, err error, fields map[string]any)
	With(fields map[string]any) Logger
}

// ConfigLoader loads and reloads configuration
type ConfigLoader interface {
	Load(configPath string) (*Config, error)
	Watch(configPath string, onChange func(*Config)) error
	StopWatch() error
}

// FlowExecutor executes flows
type FlowExecutor interface {
	Execute(ctx *ExecutionContext, flow *Flow) (*FlowExecution, error)
}

// FlowRouter selects flow based on parameters
type FlowRouter interface {
	Route(endpoint *Endpoint, sub int) (*Flow, error)
}

// Validator validates inputs and outputs
type Validator interface {
	ValidateInputs(task *Task, inputs map[string]any) error
	ValidateOutputs(task *Task, outputs map[string]any) error
	ValidateConfig(config *Config) error
}

// =====================================================================
// HELPER TYPES
// =====================================================================

// ExecutionResult is the final result returned to the caller
type ExecutionResult struct {
	RequestID     string            `json:"request_id"`
	FlowName      string            `json:"flow_name"`
	FlowVersion   string            `json:"flow_version,omitempty"`
	Status        string            `json:"status"`
	Data          any               `json:"data,omitempty"`
	Error         string            `json:"error,omitempty"`
	ExecutionTime int64             `json:"execution_time_ms"`
	Versions      map[string]string `json:"versions,omitempty"`      // Task/provider versions used
	Metadata      map[string]any    `json:"metadata,omitempty"`
}

// HTTPRequest represents incoming HTTP request data
type HTTPRequest struct {
	Method      string
	Path        string
	Headers     map[string]string
	Body        []byte
	QueryParams map[string]string
}
