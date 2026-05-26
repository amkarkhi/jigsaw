// Package symbols defines the manifest format that lets the Jigsaw CLI know
// which logic handlers and providers a consumer binary has registered.
//
// The consumer binary writes a manifest (typically to ./.jigsaw/symbols.json)
// using BuildFromEngine + Write. The CLI tools (jigsaw check, the future LSP)
// read it back with Read, and use it to flag config references to symbols
// that aren't registered anywhere.
//
// The manifest is intentionally a flat JSON file rather than an IPC contract.
// It works offline, survives across processes, and never blocks startup if
// the consumer binary is unavailable.
package symbols

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/invopop/jsonschema"
)

// DefaultManifestPath is the conventional location, relative to the config root.
const DefaultManifestPath = ".jigsaw/symbols.json"

// SchemaVersion is bumped when the on-disk format changes incompatibly.
const SchemaVersion = "2"

// Manifest is the on-disk representation of a consumer binary's registered
// symbols.
type Manifest struct {
	Version     string     `json:"version"`
	GeneratedAt time.Time  `json:"generated_at"`
	Binary      string     `json:"binary,omitempty"`
	Logic       []Logic    `json:"logic"`
	Providers   []Provider `json:"providers"`
}

// Logic describes one registered logic handler. InputSchema/OutputSchema/
// ParamsSchema are full JSON Schema objects reflected from the handler's typed
// structs.
type Logic struct {
	Name            string             `json:"name"`
	Description     string             `json:"description,omitempty"`
	Version         string             `json:"version,omitempty"`
	InputSchema     *jsonschema.Schema `json:"input_schema,omitempty"`
	OutputSchema    *jsonschema.Schema `json:"output_schema,omitempty"`
	ParamsSchema    *jsonschema.Schema `json:"params_schema,omitempty"`
	SkippableInputs []string           `json:"skippable_inputs,omitempty"`
}

// Provider describes one configured provider.
type Provider struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// BuildFromEngine constructs a Manifest from a live engine + config.
func BuildFromEngine(eng *engine.Engine, cfg *types.Config, binary string) *Manifest {
	m := &Manifest{
		Version:     SchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Binary:      binary,
	}

	for _, info := range eng.ListLogicHandlersWithInfo() {
		m.Logic = append(m.Logic, Logic{
			Name:            info.Name,
			Description:     info.Description,
			Version:         info.Version,
			InputSchema:     info.InputSchema,
			OutputSchema:    info.OutputSchema,
			ParamsSchema:    info.ParamsSchema,
			SkippableInputs: info.SkippableInputs,
		})
	}
	sort.Slice(m.Logic, func(i, j int) bool { return m.Logic[i].Name < m.Logic[j].Name })

	if cfg != nil {
		for name, prov := range cfg.Providers {
			m.Providers = append(m.Providers, Provider{
				Name: name,
				Type: prov.Type,
			})
		}
		sort.Slice(m.Providers, func(i, j int) bool { return m.Providers[i].Name < m.Providers[j].Name })
	}

	return m
}

// Write serializes the manifest as pretty-printed JSON to path, creating any
// missing parent directories. The write is atomic (tmp + rename).
func Write(path string, m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp manifest: %w", err)
	}
	return os.Rename(tmp, path)
}

// Read loads a manifest from path. Returns (nil, nil) if the file does not
// exist.
func Read(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version != SchemaVersion {
		return nil, fmt.Errorf("manifest schema version %q is not supported (want %q)", m.Version, SchemaVersion)
	}
	return &m, nil
}

// LogicNames returns the names of all registered logic handlers.
func (m *Manifest) LogicNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, len(m.Logic))
	for i, l := range m.Logic {
		names[i] = l.Name
	}
	return names
}

// Age returns how long ago the manifest was generated.
func (m *Manifest) Age() time.Duration {
	if m == nil {
		return 0
	}
	return time.Since(m.GeneratedAt)
}

// DumpToFile is the one-liner consumers wire into their own `main` to expose
// a --dump-symbols flag. Builds a manifest from the engine + config and
// writes it under <configPath>/.jigsaw/symbols.json.
func DumpToFile(eng *engine.Engine, cfg *types.Config, configPath, binary string) error {
	m := BuildFromEngine(eng, cfg, binary)
	return Write(filepath.Join(configPath, DefaultManifestPath), m)
}
