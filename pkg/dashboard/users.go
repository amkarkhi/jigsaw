package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// User-management API. Admin-only. Backed by the same auth.json that the
// CLI (`jigsaw user ...`) uses, so changes here take effect for any user
// at their next request (sessions still cache the role until they expire,
// but new logins pick up the change immediately).

// resourceFromPath maps a config-relative path to a resource category.
// Returns "" for paths that aren't part of the editable resource tree
// (e.g. .jigsaw/ system files) — callers should treat that as "denied".
func resourceFromPath(rel string) string {
	rel = strings.TrimPrefix(rel, "./")
	// Split on the first "/" only; nested layouts like flows/group/foo.yml
	// still map to "flows".
	idx := strings.Index(rel, "/")
	var top string
	if idx < 0 {
		top = rel
	} else {
		top = rel[:idx]
	}
	for _, r := range AllResources {
		if r == top {
			return r
		}
	}
	return ""
}

type userView struct {
	Username  string   `json:"username"`
	Role      string   `json:"role"`
	Email     string   `json:"email,omitempty"`
	Access    []string `json:"access"`
	CreatedAt string   `json:"created_at"`
}

func toUserView(u AuthUser) userView {
	access := u.Access
	if access == nil {
		access = []string{} // avoid `null` in JSON; the SPA wants []
	}
	return userView{
		Username:  u.Username,
		Role:      u.Role,
		Email:     u.Email,
		Access:    access,
		CreatedAt: u.CreatedAt,
	}
}

// requireAdmin returns the authenticated identity when it has admin role,
// and writes a 403 (or 401) otherwise. In ModeLocal there's no auth — we
// treat the caller as admin since the user is the dev on their laptop.
func (d *Dashboard) requireAdmin(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	if d.opts.Mode == ModeLocal {
		return Identity{Label: "local", Role: RoleAdmin, Access: deriveAccess(RoleAdmin, nil)}, true
	}
	if d.opts.Auth == nil {
		http.Error(w, "auth unconfigured", http.StatusServiceUnavailable)
		return Identity{}, false
	}
	id, err := d.opts.Auth.Authenticate(r)
	if err != nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return Identity{}, false
	}
	if id.Role != RoleAdmin {
		http.Error(w, "admin role required", http.StatusForbidden)
		return Identity{}, false
	}
	return id, true
}

// handleUsers — GET /api/users (list) and POST /api/users (create).
func (d *Dashboard) handleUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		af, err := LoadAuthFile(d.opts.ConfigPath)
		if err != nil {
			writeError(w, err)
			return
		}
		out := []userView{}
		if af != nil {
			for _, u := range af.Users {
				out = append(out, toUserView(u))
			}
		}
		writeJSON(w, map[string]any{"users": out, "resources": AllResources})

	case http.MethodPost:
		var body struct {
			Username string   `json:"username"`
			Password string   `json:"password"`
			Role     string   `json:"role"`
			Email    string   `json:"email"`
			Access   []string `json:"access"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		af, err := LoadAuthFile(d.opts.ConfigPath)
		if err != nil {
			writeError(w, err)
			return
		}
		if af == nil {
			http.Error(w, "auth not initialized", http.StatusBadRequest)
			return
		}
		if err := af.CreateUser(body.Username, body.Password, body.Role); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Backfill email + access on the freshly-created record.
		for i := range af.Users {
			if af.Users[i].Username == body.Username {
				af.Users[i].Email = strings.TrimSpace(body.Email)
				af.Users[i].Access = sanitizeAccess(body.Access)
				break
			}
		}
		if err := SaveAuthFile(d.opts.ConfigPath, af); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUser — PATCH /api/users/{username} and DELETE /api/users/{username}.
func (d *Dashboard) handleUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := d.requireAdmin(w, r)
	if !ok {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/users/")
	if name == "" || strings.Contains(name, "/") {
		http.Error(w, "username required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Role   *string   `json:"role,omitempty"`
			Email  *string   `json:"email,omitempty"`
			Access *[]string `json:"access,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		af, err := LoadAuthFile(d.opts.ConfigPath)
		if err != nil || af == nil {
			http.Error(w, "auth not initialized", http.StatusBadRequest)
			return
		}
		var target *AuthUser
		for i := range af.Users {
			if af.Users[i].Username == name {
				target = &af.Users[i]
				break
			}
		}
		if target == nil {
			http.Error(w, fmt.Sprintf("user %q not found", name), http.StatusNotFound)
			return
		}
		if body.Role != nil {
			r := strings.TrimSpace(*body.Role)
			if r != "admin" && r != "viewer" {
				http.Error(w, "role must be 'admin' or 'viewer'", http.StatusBadRequest)
				return
			}
			if target.Username == caller.Label && target.Role == "admin" && r != "admin" {
				http.Error(w, "you can't demote yourself — ask another admin", http.StatusBadRequest)
				return
			}
			target.Role = r
		}
		if body.Email != nil {
			target.Email = strings.TrimSpace(*body.Email)
		}
		if body.Access != nil {
			target.Access = sanitizeAccess(*body.Access)
		}
		if err := SaveAuthFile(d.opts.ConfigPath, af); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case http.MethodDelete:
		if name == caller.Label {
			http.Error(w, "you can't delete yourself", http.StatusBadRequest)
			return
		}
		af, err := LoadAuthFile(d.opts.ConfigPath)
		if err != nil || af == nil {
			http.Error(w, "auth not initialized", http.StatusBadRequest)
			return
		}
		if !af.DeleteUser(name) {
			http.Error(w, fmt.Sprintf("user %q not found", name), http.StatusNotFound)
			return
		}
		if err := SaveAuthFile(d.opts.ConfigPath, af); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// sanitizeAccess intersects a caller-supplied list with the known resource
// set, dedupes, and drops blanks. Anything unknown is silently dropped
// rather than returning an error — keeps the API tolerant to clients that
// might roll forward to new resources before the server does.
func sanitizeAccess(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		known := false
		for _, r := range AllResources {
			if r == x {
				known = true
				break
			}
		}
		if !known {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
