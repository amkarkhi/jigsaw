package configlang

import (
	"fmt"
	"sort"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
)

// checkAgainstSchema cross-references a task's declared inputs/outputs against
// a logic handler's schema. Mismatches produce errors; extras produce warnings.
func checkAgainstSchema(taskName string, task *types.Task, spec LogicSpec) []Diagnostic {
	var diags []Diagnostic
	diags = append(diags, compareFields(taskName, "input", task.Inputs, spec.InputSchema)...)
	diags = append(diags, compareFields(taskName, "output", task.Outputs, spec.OutputSchema)...)
	return diags
}

// compareFields enforces, for one side (inputs or outputs):
//   - Every required schema field must be present in the task declaration
//     (or have a Default on the task side that makes it optional).
//   - Types must match for any field present on both sides.
//   - Task fields not declared in the schema produce a warning ("unknown field").
//
// An empty schema is treated as "no signal" — nothing is checked. This lets
// handlers adopt schemas incrementally without breaking existing configs.
func compareFields(taskName, side string, declared, schema []types.FieldDef) []Diagnostic {
	if len(schema) == 0 {
		return nil
	}
	declaredByName := make(map[string]types.FieldDef, len(declared))
	for _, f := range declared {
		declaredByName[f.Name] = f
	}
	schemaByName := make(map[string]types.FieldDef, len(schema))
	for _, f := range schema {
		schemaByName[f.Name] = f
	}

	var diags []Diagnostic
	for _, sf := range schema {
		df, present := declaredByName[sf.Name]
		if !present {
			if sf.Required {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Message: fmt.Sprintf(
						"task %q is missing required %s %q (declared by logic handler)",
						taskName, side, sf.Name,
					),
				})
			}
			continue
		}
		if sf.Type != "" && df.Type != "" && sf.Type != df.Type && sf.Type != "any" && df.Type != "any" {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"task %q %s %q has type %q but logic handler declares %q",
					taskName, side, sf.Name, df.Type, sf.Type,
				),
			})
		}
	}
	for _, df := range declared {
		if _, ok := schemaByName[df.Name]; !ok {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Message: fmt.Sprintf(
					"task %q declares %s %q which is not in the logic handler's schema",
					taskName, side, df.Name,
				),
			})
		}
	}
	return diags
}

// Severity classifies a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one finding produced by the checker.
type Diagnostic struct {
	Severity Severity
	File     string
	Message  string
}

// String renders a diagnostic in a compact, grep-friendly form.
func (d Diagnostic) String() string {
	loc := d.File
	if loc == "" {
		loc = "<unknown>"
	}
	return fmt.Sprintf("%s: %s: %s", loc, d.Severity, d.Message)
}

// LogicSpec is the minimal handler description Check needs: a name plus
// optional input/output schemas. It mirrors symbols.Logic but lives here so
// pkg/configlang doesn't import pkg/symbols (which imports the engine).
type LogicSpec struct {
	Name         string
	InputSchema  []types.FieldDef
	OutputSchema []types.FieldDef
}

// CheckOptions controls how Check runs.
type CheckOptions struct {
	// LogicRegistry is the set of logic handlers known to be registered.
	// Only consulted when RegistryProvided is true.
	LogicRegistry []LogicSpec
	// RegistryProvided asserts that LogicRegistry is authoritative — i.e. a
	// consumer binary or manifest declared the full set. When false, the
	// "logic handler not implemented" check is skipped (no signal).
	RegistryProvided bool
}

// Check runs the full validator over an already-loaded config and produces a
// stable, sorted list of diagnostics. It is intentionally thin: today it
// returns the validator's structured errors, plus a logic-registry cross-check
// when a registry is provided.
//
// File-precise locations are not yet emitted; this v0 surfaces the resource
// name in the message instead. Line/column mapping will be added when the LSP
// path lands.
func Check(cfg *types.Config, opts CheckOptions) []Diagnostic {
	var diags []Diagnostic

	// Run the structural validator.
	v := validator.New(silentLogger{})
	if err := v.ValidateConfig(cfg); err != nil {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  err.Error(),
		})
	}

	// Logic-handler cross-check, only when a registry was provided.
	if opts.RegistryProvided {
		known := make(map[string]LogicSpec, len(opts.LogicRegistry))
		for _, spec := range opts.LogicRegistry {
			known[spec.Name] = spec
		}
		var names []string
		for name := range cfg.Tasks {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			task := cfg.Tasks[name]
			if task.Logic == "" || task.Logic == `\` {
				continue
			}
			spec, ok := known[task.Logic]
			if !ok {
				diags = append(diags, Diagnostic{
					Severity: SeverityWarning,
					Message: fmt.Sprintf(
						"task %q references logic handler %q which is not in the registry snapshot",
						name, task.Logic,
					),
				})
				continue
			}
			diags = append(diags, checkAgainstSchema(name, task, spec)...)
		}
	}

	sort.SliceStable(diags, func(i, j int) bool {
		if diags[i].File != diags[j].File {
			return diags[i].File < diags[j].File
		}
		if diags[i].Severity != diags[j].Severity {
			return diags[i].Severity < diags[j].Severity
		}
		return diags[i].Message < diags[j].Message
	})
	return diags
}

// Counts returns the number of errors and warnings in a diagnostics slice.
func Counts(diags []Diagnostic) (errors, warnings int) {
	for _, d := range diags {
		switch d.Severity {
		case SeverityError:
			errors++
		case SeverityWarning:
			warnings++
		}
	}
	return
}

// silentLogger is a no-op logger for use inside the checker, so validator
// info/debug messages don't pollute `jigsaw check` output.
type silentLogger struct{}

func (l silentLogger) Trace(string, map[string]any)        {}
func (l silentLogger) Debug(string, map[string]any)        {}
func (l silentLogger) Info(string, map[string]any)         {}
func (l silentLogger) Warn(string, map[string]any)         {}
func (l silentLogger) Error(string, error, map[string]any) {}
func (l silentLogger) With(map[string]any) types.Logger    { return l }
