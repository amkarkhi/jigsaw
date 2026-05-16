// Package configlang provides comment-preserving AST loading, formatting, and
// diagnostic reporting for Jigsaw configuration files.
//
// It is the foundation that the future LSP, web dashboard, and CLI tooling
// (jigsaw check, jigsaw fmt) all share. Today, it exposes only what the CLI
// tools need: walk a config tree, round-trip individual files, and surface
// validation diagnostics with file context.
package configlang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// File represents one parsed config file with its yaml.v3 Node preserved.
// Preserving the Node is what lets a future writer keep comments and key
// order on save.
type File struct {
	Path string
	Root *yaml.Node
}

// LoadFile parses a single config file into an AST.
func LoadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return LoadBytes(path, data)
}

// LoadBytes parses YAML from memory. Path is recorded on the resulting File
// for use in diagnostics and is not opened on disk.
func LoadBytes(path string, data []byte) (*File, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &File{Path: path, Root: &root}, nil
}

// Format renders a File using the canonical formatter: 2-space indent, LF.
// Comments and key order from the original Node are preserved.
func Format(f *File) ([]byte, error) {
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(f.Root); err != nil {
		return nil, fmt.Errorf("encode %s: %w", f.Path, err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

// IsConfigFile reports whether a path is one we treat as a Jigsaw config:
// .yml, .yaml, .jig.yml, or .jig.yaml.
func IsConfigFile(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".yml" || ext == ".yaml"
}

// WalkTree visits every config file under the given root, in the standard
// subdirectories (tasks, flows, providers, endpoints). Subdirectories that
// don't exist are skipped silently.
func WalkTree(root string, fn func(path string) error) error {
	for _, sub := range []string{"tasks", "flows", "providers", "endpoints"} {
		dir := filepath.Join(root, sub)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !IsConfigFile(path) {
				return nil
			}
			return fn(path)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
