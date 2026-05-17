package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sidecar files live under <config>/.jigsaw/ and exist only for the dashboard.
// The engine never reads them. We keep them out of the flow YAML so that
// production config files stay clean and aren't churned by mouse movement
// in the editor.
//
// Files:
//   .jigsaw/layouts/<flow>.json   — graph node positions per flow

const sidecarDir = ".jigsaw"

// /api/flow-location?name=X — scans flows/*.yml and returns the path of the
// file that contains the flow with the requested name. This lets the graph
// editor open a flow regardless of file-name convention.
func (d *Dashboard) handleFlowLocation(w http.ResponseWriter, r *http.Request) {
	d.locateResource(w, r, "flows", "flows")
}

// /api/task-location?name=X — same idea, for tasks/*.yml. Used by the
// inspector when the user wants to edit a task's label/timeout/retry/etc.
func (d *Dashboard) handleTaskLocation(w http.ResponseWriter, r *http.Request) {
	d.locateResource(w, r, "tasks", "tasks")
}

// /api/endpoint-location?name=X — same idea, for endpoints/*.yml. Used by
// the endpoints editor when appending flow mappings or removing them.
func (d *Dashboard) handleEndpointLocation(w http.ResponseWriter, r *http.Request) {
	d.locateResource(w, r, "endpoints", "endpoints")
}

// locateResource walks <subdir>/**/*.yml (recursive) looking for an entry
// with the requested name under the given top-level YAML key (e.g. "flows:"
// or "tasks:") and returns the relative path of the file that contains it.
// Recursive so that nested layouts like tasks/group-a/foo.yml still work.
func (d *Dashboard) locateResource(w http.ResponseWriter, r *http.Request, subdir, key string) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing ?name=", http.StatusBadRequest)
		return
	}
	root := filepath.Join(d.opts.ConfigPath, subdir)
	if _, err := os.Stat(root); err != nil {
		http.Error(w, "no "+subdir+" directory", http.StatusNotFound)
		return
	}
	var found string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || found != "" || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var doc map[string][]struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil
		}
		for _, entry := range doc[key] {
			if entry.Name == name {
				rel, _ := filepath.Rel(d.opts.ConfigPath, path)
				found = filepath.ToSlash(rel)
				return filepath.SkipAll
			}
		}
		return nil
	})
	if found != "" {
		writeJSON(w, map[string]string{"path": found})
		return
	}
	http.Error(w, fmt.Sprintf("%s %q not found", key, name), http.StatusNotFound)
}

// /api/layout?flow=X — GET reads the layout sidecar, PUT writes it.
// An absent sidecar is reported as {} (200), not 404, because "no layout yet"
// is a normal first-time state for any flow.
func (d *Dashboard) handleLayout(w http.ResponseWriter, r *http.Request) {
	flow := r.URL.Query().Get("flow")
	if !safeFlowName(flow) {
		http.Error(w, "invalid ?flow=", http.StatusBadRequest)
		return
	}
	path := filepath.Join(d.opts.ConfigPath, sidecarDir, "layouts", flow+".json")

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, map[string]any{})
				return
			}
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)

	case http.MethodPut, http.MethodPost:
		if !d.opts.Edit {
			http.Error(w, "edit mode is disabled", http.StatusForbidden)
			return
		}
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			writeError(w, err)
			return
		}
		out, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			writeError(w, err)
			return
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, out, 0o644); err != nil {
			writeError(w, err)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// safeFlowName rejects names that would let a request escape the layouts
// directory. We allow letters, digits, _, -, and . (but not "..").
func safeFlowName(n string) bool {
	if n == "" || n == "." || n == ".." || strings.Contains(n, "/") || strings.Contains(n, "\\") {
		return false
	}
	for _, r := range n {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}
