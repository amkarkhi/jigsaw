package engine

import (
	"fmt"

	"github.com/amkarkhi/jigsaw/pkg/types"
)

// validateFlows performs a post-registration scope-tracking validation pass
// over every flow in the config. It must be called after all handlers are
// registered (see Engine.ValidateFlows).
func validateFlows(config *types.Config, registry *logicRegistry) error {
	for flowName, flow := range config.Flows {
		scope := make(map[string]string) // name → JSON-schema type
		if err := validateFlowTasks(flow.Tasks, config, registry, scope); err != nil {
			return fmt.Errorf("flow %q: %w", flowName, err)
		}
	}
	return nil
}

// validateFlowTasks walks a task list simulating scope mutations.
func validateFlowTasks(
	tasks []types.TaskRef,
	config *types.Config,
	registry *logicRegistry,
	scope map[string]string,
) error {
	for _, ref := range tasks {
		if ref.Parallel != nil {
			if err := validateParallelBlock(ref.Parallel, config, registry, scope); err != nil {
				return err
			}
			continue
		}

		task, ok := config.Tasks[ref.Name]
		if !ok {
			return fmt.Errorf("task %q not found", ref.Name)
		}

		// Resolve inheritance for validation purposes.
		resolved := resolveTaskForFlowValidation(task, config)

		handler, err := registry.get(resolved.Logic)
		if err != nil {
			// Handler not registered — skip schema-level check; registry
			// validation (ValidateLogicHandlers) catches the missing handler.
			continue
		}

		// Check inputs.
		inputSchema := handler.InputSchema()
		if inputSchema != nil && inputSchema.Properties != nil {
			// Validate bind.skip entries: every name must exist on the input
			// schema and must be declared `jig:"skippable"` on the logic.
			if len(ref.Bind.SkipList()) > 0 {
				schemaFields := make(map[string]struct{})
				for pair := inputSchema.Properties.Oldest(); pair != nil; pair = pair.Next() {
					schemaFields[pair.Key] = struct{}{}
				}
				allowedSkip := make(map[string]struct{}, len(handler.SkippableInputs()))
				for _, name := range handler.SkippableInputs() {
					allowedSkip[name] = struct{}{}
				}
				for _, fieldName := range ref.Bind.SkipList() {
					if _, ok := schemaFields[fieldName]; !ok {
						return fmt.Errorf("task %q: bind.skip references unknown input %q (not on logic %q)", ref.Name, fieldName, resolved.Logic)
					}
					if _, ok := allowedSkip[fieldName]; !ok {
						return fmt.Errorf("task %q: input %q is not declared `jig:\"skippable\"` on logic %q and cannot be skipped", ref.Name, fieldName, resolved.Logic)
					}
				}
			}
			skipped := ref.Bind.SkipSet()
			for pair := inputSchema.Properties.Oldest(); pair != nil; pair = pair.Next() {
				fieldName := pair.Key
				if _, isSkipped := skipped[fieldName]; isSkipped {
					continue
				}
				scopeKey := ref.Bind.ResolveIn(fieldName)
				if _, exists := scope[scopeKey]; !exists {
					if isRequired(inputSchema, fieldName) {
						return fmt.Errorf("task %q: input %q: source %q not in scope at this point in the flow", ref.Name, fieldName, scopeKey)
					}
				}
			}
		}

		// Publish outputs to simulated scope.
		outputSchema := handler.OutputSchema()
		if outputSchema != nil && outputSchema.Properties != nil {
			for pair := outputSchema.Properties.Oldest(); pair != nil; pair = pair.Next() {
				fieldName := pair.Key
				scopeKey := ref.Bind.ResolveOut(fieldName)
				newType := schemaTypeString(pair.Value)
				if existing, exists := scope[scopeKey]; exists && existing != newType && newType != "" && existing != "" {
					return fmt.Errorf("task %q: output %q published to scope key %q with type %q, but %q already holds type %q; use 'bind.out' to rename", ref.Name, fieldName, scopeKey, newType, scopeKey, existing)
				}
				if newType != "" {
					scope[scopeKey] = newType
				} else {
					scope[scopeKey] = "object"
				}
			}
		}
	}
	return nil
}

// validateParallelBlock validates a parallel block and merges branch outputs
// into the parent scope under "<branch_label>.<key>".
func validateParallelBlock(
	block *types.ParallelBlock,
	config *types.Config,
	registry *logicRegistry,
	parentScope map[string]string,
) error {
	for _, branch := range block.Branches {
		// Each branch starts with a copy of the parent scope (read access).
		branchScope := make(map[string]string, len(parentScope))
		for k, v := range parentScope {
			branchScope[k] = v
		}

		if err := validateFlowTasks(branch.Tasks, config, registry, branchScope); err != nil {
			return fmt.Errorf("branch %q: %w", branch.Label, err)
		}

		// Publish keys the branch wrote (not present in parent snapshot) into
		// the parent scope under "<branch_label>.<key>".
		for k, t := range branchScope {
			if _, existsInParent := parentScope[k]; !existsInParent {
				parentScope[branch.Label+"."+k] = t
			}
		}
	}
	return nil
}

// resolveTaskForFlowValidation returns the effective Task (after inheritance).
func resolveTaskForFlowValidation(task *types.Task, config *types.Config) *types.Task {
	visited := make(map[string]struct{})
	cur := task
	for cur.Inherits != "" {
		if _, seen := visited[cur.Name]; seen {
			break
		}
		visited[cur.Name] = struct{}{}
		parent, ok := config.Tasks[cur.Inherits]
		if !ok {
			break
		}
		merged := *cur
		if merged.Logic == "" {
			merged.Logic = parent.Logic
		}
		if merged.Provider == "" {
			merged.Provider = parent.Provider
		}
		merged.Inherits = parent.Inherits
		cur = &merged
	}
	return cur
}

