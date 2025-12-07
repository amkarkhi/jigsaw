package validator

import (
	"fmt"

	"github.com/amkarkhi/jigsaw/pkg/types"
)

// Validator validates configuration and inputs/outputs
type Validator struct {
	logger types.Logger
}

// New creates a new validator
func New(logger types.Logger) *Validator {
	return &Validator{
		logger: logger,
	}
}

// ValidateConfig validates the entire configuration
func (v *Validator) ValidateConfig(config *types.Config) error {
	v.logger.Info("Validating configuration", nil)
	
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
	
	v.logger.Info("Configuration validation successful", nil)
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

// validateFlow validates a single flow configuration
func (v *Validator) validateFlow(flow *types.Flow, config *types.Config) error {
	if flow.Name == "" {
		return fmt.Errorf("flow name cannot be empty")
	}
	
	if len(flow.Tasks) == 0 && flow.Inherits == "" {
		return fmt.Errorf("flow must have tasks or inherit from another flow")
	}
	
	// Validate inheritance
	if flow.Inherits != "" {
		if _, ok := config.Flows[flow.Inherits]; !ok {
			return fmt.Errorf("parent flow '%s' not found", flow.Inherits)
		}
	}
	
	// Validate task references
	for _, taskRef := range flow.Tasks {
		if err := v.validateTaskRef(taskRef, config); err != nil {
			return err
		}
	}
	
	return nil
}

// validateTaskRef validates a task reference in a flow
func (v *Validator) validateTaskRef(taskRef types.TaskRef, config *types.Config) error {
	// Check parallel tasks
	if len(taskRef.Parallel) > 0 {
		for _, parallelTask := range taskRef.Parallel {
			if _, ok := config.Tasks[parallelTask.Name]; !ok {
				return fmt.Errorf("parallel task '%s' not found", parallelTask.Name)
			}
		}
		return nil
	}
	
	// Check regular task
	if taskRef.Name != "" {
		if _, ok := config.Tasks[taskRef.Name]; !ok {
			return fmt.Errorf("task '%s' not found", taskRef.Name)
		}
	}
	
	// Validate overrides
	for _, override := range taskRef.Overrides {
		if override.Action == "" {
			return fmt.Errorf("override action cannot be empty")
		}
		if override.Action == "replace" && override.Task == "" {
			return fmt.Errorf("override with action 'replace' must specify a task")
		}
		if override.Action == "replace" {
			if _, ok := config.Tasks[override.Task]; !ok {
				return fmt.Errorf("override task '%s' not found", override.Task)
			}
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
