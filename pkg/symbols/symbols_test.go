package symbols

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadAbsentManifestReturnsNilNil(t *testing.T) {
	m, err := Read(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("expected no error on missing file, got %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil manifest, got %+v", m)
	}
}

func TestWriteThenReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "symbols.json")

	want := &Manifest{
		Version:     SchemaVersion,
		GeneratedAt: time.Now().UTC().Truncate(time.Second),
		Binary:      "myapp",
		Logic: []Logic{
			{Name: "search.byID", Description: "Lookup by primary key"},
		},
		Providers: []Provider{
			{Name: "mysql", Type: "sql"},
		},
	}

	if err := Write(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got == nil {
		t.Fatal("read returned nil")
	}
	if got.Binary != "myapp" || len(got.Logic) != 1 || got.Logic[0].Name != "search.byID" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Providers[0].Type != "sql" {
		t.Errorf("provider type lost: %+v", got.Providers[0])
	}
}

func TestReadRejectsUnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "symbols.json")
	if err := os.WriteFile(path, []byte(`{"version":"999","generated_at":"2024-01-01T00:00:00Z","logic":[],"providers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestLogicNamesNilSafe(t *testing.T) {
	var m *Manifest
	if names := m.LogicNames(); names != nil {
		t.Errorf("nil manifest should yield nil names, got %v", names)
	}
}
