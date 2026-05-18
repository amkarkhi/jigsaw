package configlang

import (
	"strings"
	"testing"

	"github.com/amkarkhi/jigsaw/pkg/types"
)

// TestCheckSurfacesMissingHandler verifies that Check reports a warning when a
// task references a logic handler that is absent from the provided registry.
func TestCheckSurfacesMissingHandler(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"search": {
				Name:  "search",
				Logic: "search.byID",
			},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{{Name: "search"}}},
		},
	}

	// Registry is provided but does not contain "search.byID".
	opts := CheckOptions{
		RegistryProvided: true,
		LogicRegistry:    []LogicSpec{},
	}

	diags := Check(cfg, opts)

	_, warns := Counts(diags)
	if warns == 0 {
		t.Errorf("expected at least one warning for missing handler, got none; diags: %s", joinDiagMessages(diags))
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "search.byID") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected diagnostic mentioning 'search.byID', got:\n%s", joinDiagMessages(diags))
	}
}

// TestCheckSilentWhenRegistryNotProvided verifies that Check skips the handler
// check entirely when RegistryProvided is false.
func TestCheckSilentWhenRegistryNotProvided(t *testing.T) {
	cfg := &types.Config{
		Tasks: map[string]*types.Task{
			"t": {Name: "t", Logic: "unregistered_handler"},
		},
		Flows: map[string]*types.Flow{
			"f": {Name: "f", Tasks: []types.TaskRef{{Name: "t"}}},
		},
	}
	opts := CheckOptions{
		RegistryProvided: false,
	}
	diags := Check(cfg, opts)
	_, warns := Counts(diags)
	if warns != 0 {
		t.Errorf("expected no warnings when registry not provided, got %d:\n%s", warns, joinDiagMessages(diags))
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
