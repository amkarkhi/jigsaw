package types

import (
	"context"
	"time"

	"github.com/rs/zerolog"
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
	Name        string         `yaml:"name" json:"name"`
	Path        string         `yaml:"path" json:"path"`
	Method      string         `yaml:"method" json:"method"`
	Description string         `yaml:"description" json:"description,omitempty"`
	Flows       []FlowMapping  `yaml:"flows" json:"flows"`
	// RequestParams declares the scope keys that the HTTP layer will seed into
	// the execution context before the flow runs (path / query / header / body
	// parameters). The flow validator uses this list to pre-populate its
	// simulated scope so first-task inputs read from these keys validate
	// cleanly. Names should match the scope keys produced by the gateway.
	RequestParams []string       `yaml:"request_params,omitempty" json:"request_params,omitempty"`
	Metadata      map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// FlowMapping maps sub parameter to a specific flow
type FlowMapping struct {
	Sub      int    `yaml:"sub" json:"sub"`
	FlowName string `yaml:"flow_name" json:"flow_name"`
}

// Flow defines a sequence of tasks to execute
type Flow struct {
	Name        string         `yaml:"name" json:"name"`
	Description string         `yaml:"description" json:"description,omitempty"`
	Version     string         `yaml:"version,omitempty" json:"version,omitempty"`
	Inherits    string         `yaml:"inherits,omitempty" json:"inherits,omitempty"`
	Tasks       []TaskRef      `yaml:"tasks" json:"tasks"`
	Metadata    map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// TaskRef can be a simple task reference or a parallel block.
// Exactly one of Name or Parallel must be set.
type TaskRef struct {
	Name      string         `yaml:"name,omitempty" json:"name,omitempty"`
	Overrides []TaskOverride `yaml:"overrides,omitempty" json:"overrides,omitempty"`
	Parallel  *ParallelBlock `yaml:"parallel,omitempty" json:"parallel,omitempty"`
	Bind      *Bind          `yaml:"bind,omitempty" json:"bind,omitempty"`
	// Params overrides individual keys of the referenced task's Params for this
	// flow only. Shallow merge: ref keys win, unspecified keys fall through to
	// the task definition.
	Params map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
}

// WrapperRef points at another task that wraps the execution of the task
// declaring it. The wrapper's logic receives ctx.Nested (pointing to the
// wrapped task) and is expected to dispatch the wrapped task via
// ctx.Engine.InvokeTask. Params are shallow-merged on top of the wrapper
// task's own params for this binding.
type WrapperRef struct {
	Task   string         `yaml:"task" json:"task"`
	Params map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
}

// Bind carries the input and output scope-wiring for a TaskRef.
// In maps handler-input-name → scope-key-to-read-from.
// Out maps handler-output-name → scope-key-to-publish-to.
// Skip lists handler-input-names that should be omitted from the input map
// for this task ref (the logic sees the Go zero value). A field can only be
// skipped if the logic's input struct marks it `jig:"skippable"`.
type Bind struct {
	In   map[string]string `yaml:"in,omitempty" json:"in,omitempty"`
	Out  map[string]string `yaml:"out,omitempty" json:"out,omitempty"`
	Skip []string          `yaml:"skip,omitempty" json:"skip,omitempty"`
}

// SkipList returns the Skip slice, or nil when b is nil.
func (b *Bind) SkipList() []string {
	if b == nil {
		return nil
	}
	return b.Skip
}

// SkipSet returns the Skip list as a lookup set, or nil when b is nil.
func (b *Bind) SkipSet() map[string]struct{} {
	if b == nil || len(b.Skip) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(b.Skip))
	for _, name := range b.Skip {
		set[name] = struct{}{}
	}
	return set
}

// IsSkipped reports whether field is listed in b.Skip.
func (b *Bind) IsSkipped(field string) bool {
	if b == nil {
		return false
	}
	for _, name := range b.Skip {
		if name == field {
			return true
		}
	}
	return false
}

// InMap returns the In map, or nil when b is nil.
func (b *Bind) InMap() map[string]string {
	if b == nil {
		return nil
	}
	return b.In
}

// OutMap returns the Out map, or nil when b is nil.
func (b *Bind) OutMap() map[string]string {
	if b == nil {
		return nil
	}
	return b.Out
}

// ResolveIn returns the scope key for the given handler input field.
// When no rename is declared it returns field unchanged.
func (b *Bind) ResolveIn(field string) string {
	if b == nil {
		return field
	}
	if mapped, ok := b.In[field]; ok {
		return mapped
	}
	return field
}

// ResolveOut returns the scope key for the given handler output field.
// When no rename is declared it returns field unchanged.
func (b *Bind) ResolveOut(field string) string {
	if b == nil {
		return field
	}
	if renamed, ok := b.Out[field]; ok {
		return renamed
	}
	return field
}

// ParallelBlock declares N branches to execute concurrently.
//
// MinSuccess (when > 0) is the number of branches that must succeed before
// the block returns; remaining branches are canceled via context. 0 means
// "wait for all branches".
//
// Timeout (when > 0) is a millisecond budget for the whole block; on
// expiry every in-flight branch is canceled and the block returns whatever
// has been collected.
type ParallelBlock struct {
	OnBranchFailure string   `yaml:"on_branch_failure,omitempty" json:"on_branch_failure,omitempty"`
	MinSuccess      int      `yaml:"min_success,omitempty" json:"min_success,omitempty"`
	Timeout         int      `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Branches        []Branch `yaml:"branches" json:"branches"`
}

// Branch is a labeled sequence of tasks executed inside a parallel block.
// Timeout (when > 0) is a millisecond budget for this branch only; on
// expiry the branch is canceled and reports a timeout error.
type Branch struct {
	Label   string    `yaml:"label" json:"label"`
	Timeout int       `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Tasks   []TaskRef `yaml:"tasks" json:"tasks"`
}

// TaskOverride defines conditional task execution changes
type TaskOverride struct {
	Condition map[string]any `yaml:"condition" json:"condition,omitempty"`
	Action    string         `yaml:"action" json:"action"`
	Task      string         `yaml:"task,omitempty" json:"task,omitempty"`
}

// Task defines a unit of work
type Task struct {
	Name        string         `yaml:"name" json:"name"`
	Description string         `yaml:"description" json:"description,omitempty"`
	Version     string         `yaml:"version,omitempty" json:"version,omitempty"`
	Inherits    string         `yaml:"inherits,omitempty" json:"inherits,omitempty"`
	Params      map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
	Provider    string         `yaml:"provider,omitempty" json:"provider,omitempty"`
	Fallback    *Fallback      `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	Logic       string         `yaml:"logic" json:"logic,omitempty"`
	Timeout     int            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retry       int            `yaml:"retry,omitempty" json:"retry,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	// Wrapper declares a parent/wrapper task that wraps this task's execution.
	// The wrapper task shares this task's I/O shape and intercepts execution
	// (e.g., for caching, logging, metrics). The wrapper's logic receives the
	// wrapped task name via ctx.Nested so it can invoke it via
	// ctx.Engine.InvokeTask. Cycles between wrappers are rejected at config-
	// validation time.
	Wrapper *WrapperRef `yaml:"wrapper,omitempty" json:"wrapper,omitempty"`
}

// Fallback defines error handling strategy
type Fallback struct {
	Strategy   string         `yaml:"strategy" json:"strategy"`
	Message    string         `yaml:"message,omitempty" json:"message,omitempty"`
	Defaults   map[string]any `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Providers  []string       `yaml:"providers,omitempty" json:"providers,omitempty"`
	RetryCount int            `yaml:"retry_count,omitempty" json:"retry_count,omitempty"`
	RetryDelay int            `yaml:"retry_delay,omitempty" json:"retry_delay,omitempty"`
}

// Provider defines external service configuration
type Provider struct {
	Name     string         `yaml:"name" json:"name"`
	Type     string         `yaml:"type" json:"type"`
	Version  string         `yaml:"version,omitempty" json:"version,omitempty"`
	Config   map[string]any `yaml:"config" json:"config,omitempty"`
	InitMode string         `yaml:"init_mode" json:"init_mode,omitempty"`
	PoolSize int            `yaml:"pool_size,omitempty" json:"pool_size,omitempty"`
	Metadata map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// =====================================================================
// RUNTIME TYPES
// =====================================================================

// ScopedVar holds a single named value in the execution scope along with its
// JSON-schema-style type tag (used for runtime type checking and validator
// simulation).
type ScopedVar struct {
	Value any
	Type  string // JSON-schema type: "string","number","boolean","object","array","null"
}

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
	Scope       map[string]ScopedVar // Flat execution scope; keyed by variable name
	Metadata    map[string]any    // Additional runtime metadata
	Versions    map[string]string // Version tracking: task_name -> version
	Providers   ProviderRegistry  // Provider registry interface
	Logger      zerolog.Logger    // Structured logger (zerolog, by value)
	Context     context.Context   // Go context for cancellation

	// Engine exposes the running engine to logic handlers so they can dispatch
	// other registered logics by name (e.g. cache wrappers). Populated by
	// Engine.ExecuteFlow; nil for contexts not produced by a real execution.
	Engine LogicDispatcher

	// Nested points at the task being wrapped by the currently executing
	// wrapper logic. Set by the executor before invoking a wrapper, restored
	// on return. Wrappers dispatch the inner via ctx.Engine.InvokeTask(
	// ctx.Nested.Task, ...). nil when no wrapper is active. (Uses the same
	// shape as Task.Wrapper for convenience.)
	Nested *WrapperRef

	// BranchPath identifies the parallel scope this context is executing inside.
	// Empty at the top level. Populated by context.Fork.
	BranchPath []string

	// parentScope is non-nil only for branch contexts created by Fork. Reads
	// fall back to the parent when the key is absent from the local Scope.
	parentScope *ExecutionContext

	// TraceEnabled controls whether Annotate/AnnotateLink record into the
	// current TaskExecution.Annotations. Off by default. Set true by the
	// playground only — production flow execution must leave this false so
	// annotation calls in logic/wrapper code are a no-op and never leak into
	// regular flow API responses.
	TraceEnabled bool

	// currentTaskExec points at the TaskExecution the currently running logic
	// or wrapper is producing. Set by the task executor immediately before
	// invoking logic and restored on return. Used by Annotate/AnnotateLink to
	// find the right row to write into. nil outside an active task.
	currentTaskExec *TaskExecution

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Annotate records a key/value on the currently running TaskExecution. No-op
// unless TraceEnabled is true (set by the playground). Intended for
// developer-facing trace info — cache hits, timings, free-form debug context.
// Two writes to the same key overwrite; last write wins.
func (e *ExecutionContext) Annotate(key string, value any) {
	if !e.TraceEnabled || e.currentTaskExec == nil {
		return
	}
	if e.currentTaskExec.Annotations == nil {
		e.currentTaskExec.Annotations = make(map[string]any)
	}
	e.currentTaskExec.Annotations[key] = value
}

// AnnotateLink records a clickable link annotation. The playground renders
// values of this shape as <a href="url">label</a>. No-op when TraceEnabled is
// false.
func (e *ExecutionContext) AnnotateLink(key, label, url string) {
	e.Annotate(key, map[string]any{"__link": true, "label": label, "url": url})
}

// ScopeGet returns the named variable, searching the local scope first, then
// the parent chain (set at Fork time).
func (e *ExecutionContext) ScopeGet(name string) (ScopedVar, bool) {
	if v, ok := e.Scope[name]; ok {
		return v, true
	}
	if e.parentScope != nil {
		return e.parentScope.ScopeGet(name)
	}
	return ScopedVar{}, false
}

// ScopePut writes a variable into the local scope of this context.
func (e *ExecutionContext) ScopePut(name string, v ScopedVar) {
	if e.Scope == nil {
		e.Scope = make(map[string]ScopedVar)
	}
	e.Scope[name] = v
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
	ActualTask      *Task // May differ if overridden
	Inputs          map[string]any
	Params          map[string]any
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

	// Annotations is a free-form debugging bag populated by logic/wrappers via
	// ExecutionContext.Annotate/AnnotateLink. Only collected when the
	// surrounding ExecutionContext has TraceEnabled set (currently only by the
	// playground). Never serialized through normal flow API responses.
	Annotations map[string]any
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
	Execute(ctx *ExecutionContext, task *Task, ref TaskRef) (map[string]any, error)
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

// Validator validates configuration.
type Validator interface {
	ValidateConfig(config *Config) error
}

// LogicDispatcher invokes a registered logic handler or task by name from
// inside another handler. Two flavors:
//
//   - Invoke: dispatches a logic by name; the caller supplies params and
//     provider explicitly. Bind wiring does not apply.
//   - InvokeTask: dispatches a *task* by name; the engine resolves the task
//     (inheritance, params defaults, provider) and runs it through the same
//     execution path a flow ref would, minus bind (caller passes/receives raw
//     maps). paramOverrides are shallow-merged on top of the task's params.
//
// Implemented by *engine.Engine.
type LogicDispatcher interface {
	Invoke(ctx *ExecutionContext, name string, inputs map[string]any, params map[string]any, provider ProviderInstance) (map[string]any, error)
	InvokeTask(ctx *ExecutionContext, name string, inputs map[string]any, paramOverrides map[string]any) (map[string]any, error)
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
