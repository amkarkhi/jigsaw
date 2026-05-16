package configlang

import (
	"strings"
	"testing"

	"github.com/amkarkhi/jigsaw/pkg/types"
)

func TestSchemaCheckSurfacesMismatches(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"search": {
				Name:  "search",
				Logic: "search.byID",
				Inputs: []types.FieldDef{
					{Name: "id", Type: "int"},                  // type mismatch: schema says string
					{Name: "extra", Type: "string"},            // not in schema → warning
					// missing required "filter"
				},
				Outputs: []types.FieldDef{
					{Name: "result", Type: "object"},
				},
			},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{{Name: "search"}}},
		},
	}

	opts := CheckOptions{
		RegistryProvided: true,
		LogicRegistry: []LogicSpec{
			{
				Name: "search.byID",
				InputSchema: []types.FieldDef{
					{Name: "id", Type: "string"},
					{Name: "filter", Type: "string", Required: true},
				},
				OutputSchema: []types.FieldDef{
					{Name: "result", Type: "object"},
				},
			},
		},
	}

	diags := Check(cfg, opts)

	wantSubstrings := []string{
		`task "search" input "id" has type "int" but logic handler declares "string"`,
		`task "search" is missing required input "filter"`,
		`task "search" declares input "extra" which is not in the logic handler's schema`,
	}
	got := joinDiagMessages(diags)
	for _, w := range wantSubstrings {
		if !strings.Contains(got, w) {
			t.Errorf("missing expected diagnostic: %s\nfull output:\n%s", w, got)
		}
	}

	errs, warns := Counts(diags)
	if errs == 0 {
		t.Errorf("expected at least one error diagnostic, got %d", errs)
	}
	if warns == 0 {
		t.Errorf("expected at least one warning diagnostic, got %d", warns)
	}
}

func TestSchemaCheckSilentWhenSchemaEmpty(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"t": {Name: "t", Logic: "h",
				Inputs: []types.FieldDef{{Name: "x", Type: "string"}}},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{{Name: "t"}}},
		},
	}
	// Registry knows the handler but provides no schema → incremental adoption.
	opts := CheckOptions{
		RegistryProvided: true,
		LogicRegistry:    []LogicSpec{{Name: "h"}},
	}
	diags := Check(cfg, opts)
	if errs, _ := Counts(diags); errs != 0 {
		t.Errorf("expected no errors when handler has no schema, got %d:\n%s", errs, joinDiagMessages(diags))
	}
}

func joinDiagMessages(diags []Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.String())
		b.WriteByte('\n')
	}
	return b.String()
}
