package configlang

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormatPreservesComments verifies the #1 risk listed in CONFIG_MANAGER.md:
// the AST round-trip must not strip user comments.
func TestFormatPreservesComments(t *testing.T) {
	input := []byte(`# header comment
tasks:
  # task-level comment
  - name: foo
    description: "an example"  # inline comment
    inputs:
      - name: bar
        type: string
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.yml")
	if err := os.WriteFile(path, input, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out, err := Format(f)
	if err != nil {
		t.Fatalf("format: %v", err)
	}

	for _, want := range []string{
		"# header comment",
		"# task-level comment",
		"# inline comment",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("output missing comment %q\n---\n%s", want, out)
		}
	}
}

// TestFormatIsIdempotent verifies that formatting twice produces the same bytes.
// This is what makes `jigsaw fmt --check` reliable in CI.
func TestFormatIsIdempotent(t *testing.T) {
	input := []byte(`tasks:
  - name: a
    description: "first"
  - name: b
    description: "second"
    inputs:
      - {name: x, type: string}
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "t.yml")
	if err := os.WriteFile(path, input, 0o644); err != nil {
		t.Fatal(err)
	}
	f1, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pass1, err := Format(f1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pass1, 0o644); err != nil {
		t.Fatal(err)
	}
	f2, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pass2, err := Format(f2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pass1, pass2) {
		t.Errorf("format not idempotent:\n--- pass1 ---\n%s\n--- pass2 ---\n%s", pass1, pass2)
	}
}

func TestWalkTreeVisitsAllResources(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"tasks", "flows", "providers", "endpoints"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "a.yml"), []byte("name: a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A non-yaml file in tasks should be skipped.
	if err := os.WriteFile(filepath.Join(dir, "tasks", "readme.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}

	var visited []string
	if err := WalkTree(dir, func(path string) error {
		visited = append(visited, filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(visited, ",")
	for _, want := range []string{"tasks/a.yml", "flows/a.yml", "providers/a.yml", "endpoints/a.yml"} {
		if !strings.Contains(got, want) {
			t.Errorf("WalkTree missed %s; got: %s", want, got)
		}
	}
	if strings.Contains(got, "readme.txt") {
		t.Errorf("WalkTree included non-yaml: %s", got)
	}
}
