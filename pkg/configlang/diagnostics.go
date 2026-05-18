package configlang

import (
	"fmt"
	"sort"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/amkarkhi/jigsaw/pkg/validator"
	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
)

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

// LogicSpec is the minimal handler description Check needs.
type LogicSpec struct {
	Name         string
	Description  string
	Version      string
	InputSchema  *jsonschema.Schema
	OutputSchema *jsonschema.Schema
	ParamsSchema *jsonschema.Schema
}

// CheckOptions controls how Check runs.
type CheckOptions struct {
	LogicRegistry    []LogicSpec
	RegistryProvided bool
}

// Check runs the full validator over an already-loaded config and produces a
// stable, sorted list of diagnostics.
func Check(cfg *types.Config, opts CheckOptions) []Diagnostic {
	var diags []Diagnostic

	v := validator.New(zerolog.Nop())
	if err := v.ValidateConfig(cfg); err != nil {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  err.Error(),
		})
	}

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
			if task.Logic == "" {
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
			_ = spec // Schema cross-check is now done via engine.ValidateFlows.
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
