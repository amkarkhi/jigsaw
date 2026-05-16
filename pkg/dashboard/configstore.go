package dashboard

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/amkarkhi/jigsaw/pkg/config"
	"github.com/amkarkhi/jigsaw/pkg/configlang"
	"github.com/amkarkhi/jigsaw/pkg/validator"
)

// SavePayload is the JSON body POSTed to /api/files. It is a small set of
// file paths (relative to the config root) mapped to their new contents.
// Files not present in the payload are left alone.
type SavePayload struct {
	Files map[string]string `json:"files"`
}

// SaveResult is what /api/files (and /api/bundle) return on success.
type SaveResult struct {
	OK             bool                    `json:"ok"`
	Written        []string                `json:"written,omitempty"`
	BundlePath     string                  `json:"bundle_path,omitempty"`
	Diagnostics    []configlang.Diagnostic `json:"diagnostics,omitempty"`
	Mode           string                  `json:"mode"`
}

// validateAndStage parses the payload, writes the proposed files to a temp
// overlay directory, runs validation against the merged tree, and returns
// either the overlay path (caller decides how to commit) or a diagnostics
// payload describing what's wrong.
//
// Files not in the payload are copied straight from the on-disk tree so the
// validator sees a complete world. The overlay is a temp directory that
// callers must clean up.
func (d *Dashboard) validateAndStage(payload SavePayload) (overlay string, diags []configlang.Diagnostic, err error) {
	overlay, err = os.MkdirTemp("", "jigsaw-stage-")
	if err != nil {
		return "", nil, err
	}
	cleanup := overlay
	defer func() {
		if err != nil {
			os.RemoveAll(cleanup)
			overlay = ""
		}
	}()

	// Mirror the existing tree into the overlay.
	if err = copyTree(d.opts.ConfigPath, overlay); err != nil {
		return "", nil, err
	}

	// Apply the payload on top, validating paths.
	for rel, body := range payload.Files {
		if !safeRelPath(rel) {
			return "", nil, fmt.Errorf("unsafe path: %q", rel)
		}
		// Round-trip through configlang to enforce well-formed YAML and the
		// canonical formatter on save. This is what enforces "files written by
		// the editor are always parseable and canonically formatted."
		parsed, perr := configlang.LoadBytes(rel, []byte(body))
		if perr != nil {
			diags = append(diags, configlang.Diagnostic{
				Severity: configlang.SeverityError,
				File:     rel,
				Message:  fmt.Sprintf("parse: %v", perr),
			})
			continue
		}
		out, ferr := configlang.Format(parsed)
		if ferr != nil {
			diags = append(diags, configlang.Diagnostic{
				Severity: configlang.SeverityError,
				File:     rel,
				Message:  fmt.Sprintf("format: %v", ferr),
			})
			continue
		}
		dest := filepath.Join(overlay, rel)
		if err = os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", nil, err
		}
		if err = os.WriteFile(dest, out, 0o644); err != nil {
			return "", nil, err
		}
	}
	if len(diags) > 0 {
		return "", diags, nil
	}

	// Validate the staged tree the way the running engine will.
	loader := config.NewLoader(d.opts.Logger)
	cfg, lerr := loader.Load(overlay)
	if lerr != nil {
		return "", []configlang.Diagnostic{{
			Severity: configlang.SeverityError,
			Message:  fmt.Sprintf("staged tree failed to load: %v", lerr),
		}}, nil
	}
	if err := validator.New(d.opts.Logger).ValidateConfig(cfg); err != nil {
		return "", []configlang.Diagnostic{{
			Severity: configlang.SeverityError,
			Message:  err.Error(),
		}}, nil
	}

	// Also run the symbol-aware check.
	opts := configlang.CheckOptions{}
	if specs := d.loadLogicSpecs(); specs != nil {
		opts.LogicRegistry = specs
		opts.RegistryProvided = true
	}
	if checkDiags := configlang.Check(cfg, opts); len(checkDiags) > 0 {
		// Errors fail the save; warnings are returned but don't block.
		hardFail := false
		for _, dx := range checkDiags {
			if dx.Severity == configlang.SeverityError {
				hardFail = true
				break
			}
		}
		if hardFail {
			return "", checkDiags, nil
		}
		diags = checkDiags
	}

	return overlay, diags, nil
}

// commitLocal moves the staged overlay over the live config tree atomically
// (per file: tmp → rename). Files in the overlay but missing from the live
// tree are added; files in the live tree but absent from the overlay are
// preserved (we never deleted them).
func (d *Dashboard) commitLocal(overlay string) ([]string, error) {
	var written []string
	err := filepath.Walk(overlay, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(overlay, path)
		dest := filepath.Join(d.opts.ConfigPath, rel)
		// Skip files we didn't change. Cheap content compare.
		newBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if oldBytes, err := os.ReadFile(dest); err == nil && bytesEqual(oldBytes, newBytes) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		tmp := dest + ".jigsave.tmp"
		if err := os.WriteFile(tmp, newBytes, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	})
	return written, err
}

// writeBundle streams the overlay as a gzip-compressed tar to w. The archive
// preserves the standard subdirectory layout so users can extract with
// `tar -xzf archive.tar.gz -C ./configs`.
func writeBundle(overlay string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(overlay, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(overlay, path)
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// /api/files (POST) — local-mode save.
func (d *Dashboard) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !d.opts.Edit {
		http.Error(w, "edit mode is disabled (start with --edit)", http.StatusForbidden)
		return
	}
	if d.opts.Mode != ModeLocal {
		http.Error(w, "use /api/bundle in server mode", http.StatusBadRequest)
		return
	}

	var payload SavePayload
	if err := decodeJSON(r, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(payload.Files) == 0 {
		http.Error(w, "no files supplied to save", http.StatusBadRequest)
		return
	}

	overlay, diags, err := d.validateAndStage(payload)
	if err != nil {
		if strings.HasPrefix(err.Error(), "unsafe path") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeError(w, err)
		return
	}
	if overlay == "" {
		// Validation failed; return diagnostics with 422.
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(w, SaveResult{OK: false, Diagnostics: diags, Mode: d.opts.Mode.String()})
		return
	}
	defer os.RemoveAll(overlay)

	written, err := d.commitLocal(overlay)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, SaveResult{
		OK:          true,
		Written:     written,
		Diagnostics: diags,
		Mode:        d.opts.Mode.String(),
	})
}

// /api/bundle (POST) — server-mode save: stream tar.gz back to the caller.
func (d *Dashboard) handleBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !d.opts.Edit {
		http.Error(w, "edit mode is disabled (start with --edit)", http.StatusForbidden)
		return
	}

	var payload SavePayload
	if err := decodeJSON(r, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	overlay, diags, err := d.validateAndStage(payload)
	if err != nil {
		if strings.HasPrefix(err.Error(), "unsafe path") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeError(w, err)
		return
	}
	if overlay == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(w, SaveResult{OK: false, Diagnostics: diags, Mode: d.opts.Mode.String()})
		return
	}
	defer os.RemoveAll(overlay)

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="jigsaw-config-bundle.tar.gz"`)
	if err := writeBundle(overlay, w); err != nil {
		// Headers already flushed in most cases; log and bail.
		d.opts.Logger.Error("bundle write", err, nil)
	}
}

// /api/file (GET ?path=...) — fetch one file's current contents. Used by the
// raw-YAML editor when the user opens a file.
func (d *Dashboard) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if !safeRelPath(rel) {
		http.Error(w, "unsafe path", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(filepath.Join(d.opts.ConfigPath, rel))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

// /api/tree — list all config files (relative paths) so the editor can build
// a file picker.
func (d *Dashboard) handleTree(w http.ResponseWriter, _ *http.Request) {
	var paths []string
	_ = configlang.WalkTree(d.opts.ConfigPath, func(p string) error {
		rel, _ := filepath.Rel(d.opts.ConfigPath, p)
		paths = append(paths, rel)
		return nil
	})
	if paths == nil {
		paths = []string{}
	}
	writeJSON(w, paths)
}

// safeRelPath validates that rel is a relative path with no traversal and
// targets one of the canonical config subdirectories.
func safeRelPath(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	clean := filepath.Clean(rel)
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
		return false
	}
	parts := strings.Split(clean, string(filepath.Separator))
	if len(parts) < 2 {
		return false
	}
	switch parts[0] {
	case "tasks", "flows", "providers", "endpoints":
		return strings.HasSuffix(clean, ".yml") || strings.HasSuffix(clean, ".yaml")
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// copyTree mirrors src into dst, creating directories as needed. Used to
// build the staging overlay so the validator sees a complete tree.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Skip non-yaml files except symbols.json (we want it for validation).
		base := filepath.Base(path)
		ext := filepath.Ext(path)
		if ext != ".yml" && ext != ".yaml" && base != "symbols.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
