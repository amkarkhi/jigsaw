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
	Name        string         `yaml:"name"`
	Path        string         `yaml:"path"`
	Method      string         `yaml:"method"`
	Description string         `yaml:"description"`
	Flows       []FlowMapping  `yaml:"flows"`
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// FlowMapping maps sub parameter to a specific flow
type FlowMapping struct {
	Sub      int    `yaml:"sub"`       // Direct sub -> flow mapping
	FlowName string `yaml:"flow_name"` // Target flow name
}

// Flow defines a sequence of tasks to execute
type Flow struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version,omitempty"`  // Semantic version (e.g., "1.0.0", "2.1.3")
	Inherits    string         `yaml:"inherits,omitempty"` // Parent flow to inherit from
	Tasks       []TaskRef      `yaml:"tasks"`              // Ordered list of tasks
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// TaskRef can be a simple task reference or a parallel block.
// Exactly one of Name or Parallel must be set.
type TaskRef struct {
	Name      string         `yaml:"name,omitempty"`
	Overrides []TaskOverride `yaml:"overrides,omitempty"`
	Parallel  *ParallelBlock `yaml:"parallel,omitempty"`
}

// ParallelBlock declares N branches to execute concurrently.
type ParallelBlock struct {
	OnBranchFailure string   `yaml:"on_branch_failure,omitempty"` // "continue" (default) | "cancel"
	Branches        []Branch `yaml:"branches"`
}

// Branch is a labeled sequence of tasks executed inside a parallel block.
type Branch struct {
	Label string    `yaml:"label"`
	Tasks []TaskRef `yaml:"tasks"`
}

// TaskOverride defines conditional task execution changes
type TaskOverride struct {
	Condition map[string]any `yaml:"condition"`      // e.g., {tag: "premium"}
	Action    string         `yaml:"action"`         // skip, replace
	Task      string         `yaml:"task,omitempty"` // Replacement task name
}

// Task defines a unit of work
type Task struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version,omitempty"` // Semantic version (e.g., "1.0.0", "2.1.3")
	Label       string         `yaml:"label,omitempty"`   // Flow-local logical name for this task's outputs
	Inherits    string         `yaml:"inherits,omitempty"`
	Inputs      []FieldDef     `yaml:"inputs"`
	Outputs     []FieldDef     `yaml:"outputs"`
	Provider    string         `yaml:"provider,omitempty"`
	Fallback    *Fallback      `yaml:"fallback,omitempty"`
	Logic       string         `yaml:"logic"`
	Timeout     int            `yaml:"timeout,omitempty"` // milliseconds
	Retry       int            `yaml:"retry,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// FieldDef defines input/output field definition.
// For inputs, From and Field control label-based resolution:
//   - From is a dotted path "[branch.]*label" identifying the producer.
//   - Field picks a single field from the producer's outputs; defaults to Name.
type FieldDef struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"` // string, int, bool, object, array, any
	Required   bool   `yaml:"required"`
	Default    any    `yaml:"default,omitempty"`
	Validation string `yaml:"validation,omitempty"`
	From       string `yaml:"from,omitempty"`
	Field      string `yaml:"field,omitempty"`
}

// Fallback defines error handling strategy
type Fallback struct {
	Strategy   string         `yaml:"strategy"` // abort, continue, switch_task, switch_provider
	Message    string         `yaml:"message,omitempty"`
	Defaults   map[string]any `yaml:"defaults,omitempty"`    // For continue strategy
	TargetTask string         `yaml:"target_task,omitempty"` // For switch_task
	Providers  []string       `yaml:"providers,omitempty"`   // For switch_provider
	RetryCount int            `yaml:"retry_count,omitempty"`
	RetryDelay int            `yaml:"retry_delay,omitempty"` // milliseconds
}

// Provider defines external service configuration
type Provider struct {
	Name     string         `yaml:"name"`
	Type     string         `yaml:"type"`              // cache, database, search_engine, http, etc.
	Version  string         `yaml:"version,omitempty"` // Provider version (e.g., "cache:1.0", "database:2.0")
	Config   map[string]any `yaml:"config"`
	InitMode string         `yaml:"init_mode"` // lazy, eager, pooled
	PoolSize int            `yaml:"pool_size,omitempty"`
	Metadata map[string]any `yaml:"metadata,omitempty"`
}

// =====================================================================
// RUNTIME TYPES
// =====================================================================

// LabeledProducer records a single producer's contribution to the label index.
type LabeledProducer struct {
	TaskName   string
	BranchPath []string // Empty for main-flow producers
	Outputs    map[string]any
}

// LabelIndex maps a label to all producers that have published under it, in
// completion order. Resolution rules live in ExecutionContext.ResolveLabel.
type LabelIndex map[string][]LabeledProducer

// ExecutionContext carries data through flow execution.
// Within a single goroutine it is safe to mutate. When forking into parallel
// branches, use context.Fork (pkg/context) to obtain a branch-local copy;
// never share a single ExecutionContext across goroutines.
type ExecutionContext struct {
	RequestID   string            // Unique request identifier
	FlowName    string            // Current flow name
	FlowVersion string            // Version of the flow being executed
	CurrentTask string            // Current task name
	Sub         int               // Sub parameter (flow selector)
	Tag         string            // Tag for task overrides
	Parameters  map[string]any    // Request parameters
	Headers     map[string]string // HTTP headers
	TaskOutputs map[string]any    // All task outputs (keyed by qualified task name)
	LastOutput  any               // Output from previous task; nil after a parallel block
	Metadata    map[string]any    // Additional runtime metadata
	Versions    map[string]string // Version tracking: task_name -> version
	Providers   ProviderRegistry  // Provider registry interface
	Logger      Logger            // Logger interface
	Context     context.Context   // Go context for cancellation

	// BranchPath identifies the parallel scope this context is executing inside.
	// Empty at the top level. Populated by context.Fork.
	BranchPath []string

	// Labels backs label-based input resolution. Shared structure between a
	// parent and a freshly-forked branch (the branch only writes to its own
	// snapshot, which is merged back at join time by context.Merge).
	Labels LabelIndex

	CreatedAt time.Time
	UpdatedAt time.Time
}

// GetScopedData returns data accessible to the current task. Inputs are
// resolved with the following priority:
//  1. Request parameters (by input name).
//  2. Label-based lookup when input.From is set (see ResolveLabel).
//  3. Field-name scan across recorded task outputs (legacy behavior).
func (e *ExecutionContext) GetScopedData(task *Task) map[string]any {
	data := make(map[string]any)
	data["_last_output"] = e.LastOutput

	for _, input := range task.Inputs {
		if val, ok := e.Parameters[input.Name]; ok {
			data[input.Name] = val
			continue
		}

		if input.From != "" {
			if val, ok := e.ResolveLabel(input.From, input.fieldOrName()); ok {
				data[input.Name] = val
			}
			continue
		}

		for _, taskOutput := range e.TaskOutputs {
			if outputMap, ok := taskOutput.(map[string]any); ok {
				if val, ok := outputMap[input.Name]; ok {
					data[input.Name] = val
					break
				}
			}
		}
	}

	data["_metadata"] = e.Metadata
	data["_tag"] = e.Tag
	data["_sub"] = e.Sub

	return data
}

// fieldOrName returns the effective output field name to read from the
// resolved producer's outputs.
func (f FieldDef) fieldOrName() string {
	if f.Field != "" {
		return f.Field
	}
	return f.Name
}

// ResolveLabel implements `from:` lookup. The path is "[branch.]*label".
// A consumer at branch path C resolving "P1.P2....label" looks for a producer
// whose BranchPath == C + [P1..Pn].
//
// When the path is unqualified (`from: label`), the producer must live in the
// same branch path as the consumer. If sibling branches produced the same
// label, this is ambiguous and the function returns (nil, false) — the
// validator is expected to catch such cases at config load time.
func (e *ExecutionContext) ResolveLabel(fromPath, field string) (any, bool) {
	if fromPath == "" {
		return nil, false
	}

	segments := splitDots(fromPath)
	label := segments[len(segments)-1]
	branchSuffix := segments[:len(segments)-1]

	expectedPath := append(append([]string{}, e.BranchPath...), branchSuffix...)

	producers, ok := e.Labels[label]
	if !ok {
		return nil, false
	}

	var match *LabeledProducer
	ambiguous := false
	for i := range producers {
		p := &producers[i]
		if !pathEqual(p.BranchPath, expectedPath) {
			// If the consumer used an unqualified label, also accept producers
			// whose branch path is a strict prefix of the consumer's (i.e.
			// they live in an enclosing scope). We never accept producers in
			// sibling or unrelated branches.
			if len(branchSuffix) == 0 && isPrefix(p.BranchPath, e.BranchPath) {
				if match != nil && !pathEqual(match.BranchPath, p.BranchPath) {
					ambiguous = true
				}
				match = p
			}
			continue
		}
		match = p
	}
	if ambiguous || match == nil {
		return nil, false
	}

	if field == "" {
		return match.Outputs, true
	}
	val, ok := match.Outputs[field]
	return val, ok
}

// SetTaskOutput stores task output in context. The task name is stored using
// the context's branch path as a prefix, ensuring sibling branches that run
// the same task name do not collide in TaskOutputs.
func (e *ExecutionContext) SetTaskOutput(taskName string, output map[string]any) {
	e.TaskOutputs[e.qualify(taskName)] = output
	e.LastOutput = output
	e.UpdatedAt = time.Now()
}

// PublishLabel records a labeled output in the label index under the current
// branch path. Called by the task executor whenever a task with a non-empty
// label completes successfully.
func (e *ExecutionContext) PublishLabel(label, taskName string, output map[string]any) {
	if label == "" {
		return
	}
	if e.Labels == nil {
		e.Labels = make(LabelIndex)
	}
	branchPath := append([]string{}, e.BranchPath...)
	e.Labels[label] = append(e.Labels[label], LabeledProducer{
		TaskName:   taskName,
		BranchPath: branchPath,
		Outputs:    output,
	})
}

// qualify returns the branch-qualified storage key for a task name.
func (e *ExecutionContext) qualify(taskName string) string {
	if len(e.BranchPath) == 0 {
		return taskName
	}
	out := ""
	for _, b := range e.BranchPath {
		out += b + "."
	}
	return out + taskName
}

func splitDots(s string) []string {
	parts := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func pathEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isPrefix(prefix, full []string) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		if prefix[i] != full[i] {
			return false
		}
	}
	return true
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
	ActualTask      *Task // May differ if overridden
	Inputs          map[string]any
	Outputs         map[string]any
	Status          ExecutionStatus
	StartedAt       time.Time
	CompletedAt     *time.Time
	Error           error
	FallbackUsed    bool
	RetryCount      int
	ProviderUsed    string
	ProviderVersion string // Version of provider used
	TaskVersion     string // Version of task executed
	LogicVersion    string // Version of logic handler (if available)
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
	Trace(msg string, fields map[string]any)
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
	Versions      map[string]string `json:"versions,omitempty"` // Task/provider versions used
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
