package validator

import (
	"fmt"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// Validator validates configuration and inputs/outputs
type Validator struct {
	logger zerolog.Logger
}

// New creates a new validator
func New(logger zerolog.Logger) *Validator {
	return &Validator{
		logger: logger,
	}
}

// ValidateConfig validates the entire configuration
func (v *Validator) ValidateConfig(config *types.Config) error {
	v.logger.Info().Msg("Validating configuration")
	
	// Validate tasks
	for name, task := range config.Tasks {
		if err := v.validateTask(task); err != nil {
			return fmt.Errorf("invalid task '%s': %w", name, err)
		}
	}
	
	// Validate flows
	for name, flow := range config.Flows {
		if err := v.validateFlow(flow, config); err != nil {
			return fmt.Errorf("invalid flow '%s': %w", name, err)
		}
	}
	
	// Validate endpoints
	for name, endpoint := range config.Endpoints {
		if err := v.validateEndpoint(endpoint, config); err != nil {
			return fmt.Errorf("invalid endpoint '%s': %w", name, err)
		}
	}
	
	v.logger.Info().Msg("Configuration validation successful")
	return nil
}

// ValidateInputs validates task inputs
func (v *Validator) ValidateInputs(task *types.Task, inputs map[string]any) error {
	for _, inputDef := range task.Inputs {
		value, ok := inputs[inputDef.Name]
		
		if !ok {
			if inputDef.Required && inputDef.Default == nil {
				return fmt.Errorf("required input '%s' is missing", inputDef.Name)
			}
			continue
		}
		
		// Type validation
		if err := v.validateType(inputDef.Name, value, inputDef.Type); err != nil {
			return err
		}
	}
	
	return nil
}

// ValidateOutputs validates task outputs
func (v *Validator) ValidateOutputs(task *types.Task, outputs map[string]any) error {
	for _, outputDef := range task.Outputs {
		value, ok := outputs[outputDef.Name]
		
		if !ok {
			if outputDef.Required {
				return fmt.Errorf("required output '%s' is missing", outputDef.Name)
			}
			continue
		}
		
		// Type validation
		if err := v.validateType(outputDef.Name, value, outputDef.Type); err != nil {
			return err
		}
	}
	
	return nil
}

// validateTask validates a single task configuration
func (v *Validator) validateTask(task *types.Task) error {
	if task.Name == "" {
		return fmt.Errorf("task name cannot be empty")
	}
	
	if task.Logic == "" && task.Provider == "" {
		return fmt.Errorf("task must have either logic or provider")
	}
	
	// Validate inputs
	for _, input := range task.Inputs {
		if input.Name == "" {
			return fmt.Errorf("input name cannot be empty")
		}
		if input.Type == "" {
			return fmt.Errorf("input '%s' must have a type", input.Name)
		}
	}
	
	// Validate outputs
	for _, output := range task.Outputs {
		if output.Name == "" {
			return fmt.Errorf("output name cannot be empty")
		}
		if output.Type == "" {
			return fmt.Errorf("output '%s' must have a type", output.Name)
		}
	}
	
	// Validate fallback
	if task.Fallback != nil {
		if err := v.validateFallback(task.Fallback); err != nil {
			return err
		}
	}
	
	return nil
}

// validateFlow validates a single flow configuration.
//
// In addition to the per-TaskRef structural checks, validateFlow walks the
// task list and tracks which labels are "visible" at each position. This lets
// us validate `from:` references against the actual scoping rules (sequential
// producers, branch-qualified producers after a join) at config-load time.
func (v *Validator) validateFlow(flow *types.Flow, config *types.Config) error {
	if flow.Name == "" {
		return fmt.Errorf("flow name cannot be empty")
	}

	if len(flow.Tasks) == 0 && flow.Inherits == "" {
		return fmt.Errorf("flow must have tasks or inherit from another flow")
	}

	if flow.Inherits != "" {
		if _, ok := config.Flows[flow.Inherits]; !ok {
			return fmt.Errorf("parent flow '%s' not found", flow.Inherits)
		}
	}

	// Walk the task list, accumulating visible labels as we go.
	scope := newLabelScope()
	return v.validateTaskList(flow.Tasks, config, scope)
}

// validateTaskList enforces structural and label-scoping rules over a flat
// task list (either the flow's top-level list or a branch's task list).
func (v *Validator) validateTaskList(tasks []types.TaskRef, config *types.Config, scope *labelScope) error {
	for _, ref := range tasks {
		if err := v.validateTaskRef(ref, config, scope); err != nil {
			return err
		}
	}
	return nil
}

// validateTaskRef validates a single TaskRef and updates `scope` with any
// labels the task contributes to the visible set.
func (v *Validator) validateTaskRef(ref types.TaskRef, config *types.Config, scope *labelScope) error {
	hasName := ref.Name != ""
	hasParallel := ref.Parallel != nil

	if hasName == hasParallel {
		return fmt.Errorf("task reference must have exactly one of 'name' or 'parallel'")
	}

	if hasParallel {
		return v.validateParallel(ref.Parallel, config, scope)
	}

	task, ok := config.Tasks[ref.Name]
	if !ok {
		return fmt.Errorf("task '%s' not found", ref.Name)
	}

	// Resolve inheritance just enough to know the effective Label and Inputs.
	resolvedTask, err := resolveTaskForValidation(task, config)
	if err != nil {
		return err
	}

	for _, input := range resolvedTask.Inputs {
		if input.From == "" {
			continue
		}
		if err := scope.checkFrom(input.From); err != nil {
			return fmt.Errorf("task '%s' input '%s': %w", ref.Name, input.Name, err)
		}
	}

	for _, override := range ref.Overrides {
		if override.Action == "" {
			return fmt.Errorf("override action cannot be empty")
		}
		if override.Action == "replace" {
			if override.Task == "" {
				return fmt.Errorf("override with action 'replace' must specify a task")
			}
			if _, ok := config.Tasks[override.Task]; !ok {
				return fmt.Errorf("override task '%s' not found", override.Task)
			}
		}
	}

	// Labels are flow-scoped. A per-placement label on the TaskRef wins;
	// fall back to the Task's own label (kept for back-compat) only if the
	// TaskRef didn't set one.
	effectiveLabel := ref.Label
	if effectiveLabel == "" {
		effectiveLabel = resolvedTask.Label
	}
	if effectiveLabel != "" {
		scope.publish(effectiveLabel)
	}
	return nil
}

// validateParallel validates a parallel block and updates the parent scope
// with any labels its branches publish (qualified by branch label, so they
// remain addressable downstream as `from: <branch>.<label>`).
func (v *Validator) validateParallel(block *types.ParallelBlock, config *types.Config, parentScope *labelScope) error {
	switch block.OnBranchFailure {
	case "", "continue", "cancel":
	default:
		return fmt.Errorf("invalid on_branch_failure '%s'; must be 'continue' or 'cancel'", block.OnBranchFailure)
	}

	if len(block.Branches) == 0 {
		return fmt.Errorf("parallel block must declare at least one branch")
	}

	seenBranchLabels := make(map[string]struct{}, len(block.Branches))
	branchScopes := make([]*labelScope, len(block.Branches))

	for i, branch := range block.Branches {
		if branch.Label == "" {
			return fmt.Errorf("parallel branch %d must have a label", i)
		}
		if _, dup := seenBranchLabels[branch.Label]; dup {
			return fmt.Errorf("duplicate branch label '%s' in parallel block", branch.Label)
		}
		seenBranchLabels[branch.Label] = struct{}{}
		if len(branch.Tasks) == 0 {
			return fmt.Errorf("branch '%s' must declare at least one task", branch.Label)
		}

		branchScope := parentScope.child(branch.Label)
		if err := v.validateTaskList(branch.Tasks, config, branchScope); err != nil {
			return fmt.Errorf("branch '%s': %w", branch.Label, err)
		}
		branchScopes[i] = branchScope
	}

	// Promote branch-qualified labels into the parent scope so downstream
	// tasks may reference them via `from: <branch>.<label>`.
	for i, branch := range block.Branches {
		for label := range branchScopes[i].produced {
			parentScope.publishQualified(branch.Label, label)
		}
	}
	return nil
}

// validateEndpoint validates an endpoint configuration
func (v *Validator) validateEndpoint(endpoint *types.Endpoint, config *types.Config) error {
	if endpoint.Name == "" {
		return fmt.Errorf("endpoint name cannot be empty")
	}
	
	if endpoint.Path == "" {
		return fmt.Errorf("endpoint path cannot be empty")
	}
	
	if endpoint.Method == "" {
		return fmt.Errorf("endpoint method cannot be empty")
	}
	
	if len(endpoint.Flows) == 0 {
		return fmt.Errorf("endpoint must have at least one flow mapping")
	}
	
	// Validate flow mappings
	seenSubs := make(map[int]bool)
	for _, mapping := range endpoint.Flows {
		// Check for duplicate sub values
		if seenSubs[mapping.Sub] {
			return fmt.Errorf("duplicate sub value %d in endpoint", mapping.Sub)
		}
		seenSubs[mapping.Sub] = true
		
		// Check if flow exists
		if _, ok := config.Flows[mapping.FlowName]; !ok {
			return fmt.Errorf("flow '%s' not found in mapping", mapping.FlowName)
		}
	}
	
	return nil
}

// validateFallback validates fallback configuration
func (v *Validator) validateFallback(fallback *types.Fallback) error {
	validStrategies := map[string]bool{
		"abort":           true,
		"continue":        true,
		"switch_task":     true,
		"switch_provider": true,
	}
	
	if !validStrategies[fallback.Strategy] {
		return fmt.Errorf("invalid fallback strategy '%s'", fallback.Strategy)
	}
	
	if fallback.Strategy == "switch_task" && fallback.TargetTask == "" {
		return fmt.Errorf("fallback strategy 'switch_task' requires target_task")
	}
	
	if fallback.Strategy == "switch_provider" && len(fallback.Providers) == 0 {
		return fmt.Errorf("fallback strategy 'switch_provider' requires providers list")
	}
	
	return nil
}

// labelScope tracks which labels are visible to a consumer at its current
// position in the flow. It is used purely for config-time `from:` validation.
//
// Two kinds of entries exist:
//   - `produced`: unqualified labels visible in the current sequential scope
//     (set by tasks earlier in the same list).
//   - `qualified`: branch-qualified label paths visible after a parallel join
//     (e.g. "left.user_data", "outer.inner.user_data" for nested parallels).
type labelScope struct {
	produced  map[string]struct{}
	qualified map[string]struct{}
}

func newLabelScope() *labelScope {
	return &labelScope{
		produced:  make(map[string]struct{}),
		qualified: make(map[string]struct{}),
	}
}

// child returns a fresh scope for a branch. The child inherits the parent's
// visible labels (the branch can read labels produced upstream of the parallel
// block) but maintains its own produced/qualified sets.
func (s *labelScope) child(branchLabel string) *labelScope {
	c := newLabelScope()
	for k := range s.produced {
		c.produced[k] = struct{}{}
	}
	for k := range s.qualified {
		c.qualified[k] = struct{}{}
	}
	_ = branchLabel
	return c
}

func (s *labelScope) publish(label string) {
	s.produced[label] = struct{}{}
}

// publishQualified registers `<branch>.<label>` (and propagates nested
// qualified labels) so downstream consumers can address them.
func (s *labelScope) publishQualified(branch, label string) {
	s.qualified[branch+"."+label] = struct{}{}
}

// checkFrom returns nil if `from` resolves against this scope. An unqualified
// `from: label` matches a sequential producer in `produced`; a dotted path
// must match an entry in `qualified`.
func (s *labelScope) checkFrom(from string) error {
	if from == "" {
		return nil
	}
	if !containsDot(from) {
		if _, ok := s.produced[from]; ok {
			return nil
		}
		return fmt.Errorf("from '%s' has no visible producer in scope", from)
	}
	if _, ok := s.qualified[from]; ok {
		return nil
	}
	return fmt.Errorf("from '%s' does not match any reachable branch producer", from)
}

func containsDot(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return true
		}
	}
	return false
}

// resolveTaskForValidation produces the effective Task (after inheritance) so
// validation sees the same Inputs/Label the executor will. Cycle-safe via a
// visited set.
func resolveTaskForValidation(task *types.Task, config *types.Config) (*types.Task, error) {
	visited := make(map[string]struct{})
	cur := task
	for cur.Inherits != "" {
		if _, seen := visited[cur.Name]; seen {
			return nil, fmt.Errorf("task inheritance cycle at '%s'", cur.Name)
		}
		visited[cur.Name] = struct{}{}
		parent, ok := config.Tasks[cur.Inherits]
		if !ok {
			return nil, fmt.Errorf("parent task '%s' not found for '%s'", cur.Inherits, cur.Name)
		}
		// Child wins; only fill blanks from parent.
		merged := *cur
		if merged.Label == "" {
			merged.Label = parent.Label
		}
		if len(merged.Inputs) == 0 {
			merged.Inputs = parent.Inputs
		}
		if len(merged.Outputs) == 0 {
			merged.Outputs = parent.Outputs
		}
		merged.Inherits = parent.Inherits
		cur = &merged
	}
	return cur, nil
}

// validateType validates value type
func (v *Validator) validateType(name string, value any, expectedType string) error {
	if expectedType == "any" {
		return nil
	}
	
	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("field '%s' must be a string", name)
		}
	case "int":
		switch value.(type) {
		case int, int32, int64, float64:
			return nil
		default:
			return fmt.Errorf("field '%s' must be an integer", name)
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field '%s' must be a boolean", name)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("field '%s' must be an object", name)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("field '%s' must be an array", name)
		}
	}
	
	return nil
}
