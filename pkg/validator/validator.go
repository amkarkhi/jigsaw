package validator

import (
	"fmt"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// Validator validates configuration.
type Validator struct {
	logger zerolog.Logger
}

// New creates a new validator.
func New(logger zerolog.Logger) *Validator {
	return &Validator{
		logger: logger,
	}
}

// ValidateConfig validates the entire configuration.
func (v *Validator) ValidateConfig(config *types.Config) error {
	v.logger.Info().Msg("Validating configuration")

	for name, task := range config.Tasks {
		if err := v.validateTask(task); err != nil {
			return fmt.Errorf("invalid task %q: %w", name, err)
		}
	}

	for name, flow := range config.Flows {
		if err := v.validateFlow(flow, config); err != nil {
			return fmt.Errorf("invalid flow %q: %w", name, err)
		}
	}

	for name, endpoint := range config.Endpoints {
		if err := v.validateEndpoint(endpoint, config); err != nil {
			return fmt.Errorf("invalid endpoint %q: %w", name, err)
		}
	}

	v.logger.Info().Msg("Configuration validation successful")
	return nil
}

// validateTask validates a single task configuration.
func (v *Validator) validateTask(task *types.Task) error {
	if task.Name == "" {
		return fmt.Errorf("task name cannot be empty")
	}

	if task.Logic == "" && task.Provider == "" {
		return fmt.Errorf("task must have either logic or provider")
	}

	if task.Fallback != nil {
		if err := v.validateFallback(task.Fallback); err != nil {
			return err
		}
	}

	return nil
}

// validateFlow validates a single flow configuration.
func (v *Validator) validateFlow(flow *types.Flow, config *types.Config) error {
	if flow.Name == "" {
		return fmt.Errorf("flow name cannot be empty")
	}

	if len(flow.Tasks) == 0 && flow.Inherits == "" {
		return fmt.Errorf("flow must have tasks or inherit from another flow")
	}

	if flow.Inherits != "" {
		if _, ok := config.Flows[flow.Inherits]; !ok {
			return fmt.Errorf("parent flow %q not found", flow.Inherits)
		}
	}

	return v.validateTaskList(flow.Tasks, config)
}

// validateTaskList validates a list of TaskRefs.
func (v *Validator) validateTaskList(tasks []types.TaskRef, config *types.Config) error {
	for _, ref := range tasks {
		if err := v.validateTaskRef(ref, config); err != nil {
			return err
		}
	}
	return nil
}

// validateTaskRef validates a single TaskRef.
func (v *Validator) validateTaskRef(ref types.TaskRef, config *types.Config) error {
	hasName := ref.Name != ""
	hasParallel := ref.Parallel != nil

	if hasName == hasParallel {
		return fmt.Errorf("task reference must have exactly one of 'name' or 'parallel'")
	}

	if hasParallel {
		return v.validateParallel(ref.Parallel, config)
	}

	if _, ok := config.Tasks[ref.Name]; !ok {
		return fmt.Errorf("task %q not found", ref.Name)
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
				return fmt.Errorf("override task %q not found", override.Task)
			}
		}
	}

	return nil
}

// validateParallel validates a parallel block.
func (v *Validator) validateParallel(block *types.ParallelBlock, config *types.Config) error {
	switch block.OnBranchFailure {
	case "", "continue", "cancel":
	default:
		return fmt.Errorf("invalid on_branch_failure %q; must be 'continue' or 'cancel'", block.OnBranchFailure)
	}

	if len(block.Branches) == 0 {
		return fmt.Errorf("parallel block must declare at least one branch")
	}

	seen := make(map[string]struct{}, len(block.Branches))
	for i, branch := range block.Branches {
		if branch.Label == "" {
			return fmt.Errorf("parallel branch %d must have a label", i)
		}
		if _, dup := seen[branch.Label]; dup {
			return fmt.Errorf("duplicate branch label %q in parallel block", branch.Label)
		}
		seen[branch.Label] = struct{}{}
		if len(branch.Tasks) == 0 {
			return fmt.Errorf("branch %q must declare at least one task", branch.Label)
		}
		if err := v.validateTaskList(branch.Tasks, config); err != nil {
			return fmt.Errorf("branch %q: %w", branch.Label, err)
		}
	}
	return nil
}

// validateEndpoint validates an endpoint configuration.
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

	seenSubs := make(map[int]bool)
	for _, mapping := range endpoint.Flows {
		if seenSubs[mapping.Sub] {
			return fmt.Errorf("duplicate sub value %d in endpoint", mapping.Sub)
		}
		seenSubs[mapping.Sub] = true
		if _, ok := config.Flows[mapping.FlowName]; !ok {
			return fmt.Errorf("flow %q not found in mapping", mapping.FlowName)
		}
	}

	return nil
}

// validateFallback validates fallback configuration.
func (v *Validator) validateFallback(fallback *types.Fallback) error {
	validStrategies := map[string]bool{
		"abort":           true,
		"continue":        true,
		"switch_provider": true,
	}

	if !validStrategies[fallback.Strategy] {
		return fmt.Errorf("invalid fallback strategy %q", fallback.Strategy)
	}

	if fallback.Strategy == "switch_provider" && len(fallback.Providers) == 0 {
		return fmt.Errorf("fallback strategy 'switch_provider' requires providers list")
	}

	return nil
}
