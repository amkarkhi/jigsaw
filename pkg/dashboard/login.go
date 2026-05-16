package dashboard

import (
	"encoding/json"
	"net/http"
	"time"
)

// login / logout / me — endpoints for the dashboard's interactive auth flow.
// Only registered when the dashboard's Auth provider is a *FileAuth (which
// is the bundled provider for server mode).

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (d *Dashboard) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fa, ok := d.opts.Auth.(*FileAuth)
	if !ok {
		http.Error(w, "login not supported in this auth mode", http.StatusBadRequest)
		return
	}
	var body loginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sid, id, err := fa.Login(body.Username, body.Password)
	if err != nil {
		// Constant-time-ish: bcrypt comparison already burns CPU, so just
		// echo a single "invalid" without leaking which field is wrong.
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "jigsaw_session",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure on by default for non-loopback binds; harmless on loopback.
		Secure:  isHTTPSReq(r),
		Expires: time.Now().Add(12 * time.Hour),
	})
	writeJSON(w, map[string]any{
		"ok":    true,
		"label": id.Label,
		"role":  roleString(id.Role),
	})
}

func (d *Dashboard) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fa, ok := d.opts.Auth.(*FileAuth)
	if !ok {
		http.Error(w, "logout not supported in this auth mode", http.StatusBadRequest)
		return
	}
	if c, err := r.Cookie("jigsaw_session"); err == nil {
		fa.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "jigsaw_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, map[string]bool{"ok": true})
}

// handleMe returns the currently-authenticated identity, or 401 if none.
// Used by the SPA on load to decide whether to show the login screen.
func (d *Dashboard) handleMe(w http.ResponseWriter, r *http.Request) {
	if d.opts.Mode == ModeLocal {
		writeJSON(w, map[string]any{"authenticated": true, "label": "local", "role": "admin"})
		return
	}
	if d.opts.Auth == nil {
		http.Error(w, "auth unconfigured", http.StatusServiceUnavailable)
		return
	}
	id, err := d.opts.Auth.Authenticate(r)
	if err != nil {
		writeJSON(w, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, map[string]any{
		"authenticated": true,
		"label":         id.Label,
		"role":          roleString(id.Role),
	})
}

func roleString(r Role) string {
	if r == RoleAdmin {
		return "admin"
	}
	return "viewer"
}

func isHTTPSReq(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if v := r.Header.Get("X-Forwarded-Proto"); v == "https" {
		return true
	}
	return false
}
