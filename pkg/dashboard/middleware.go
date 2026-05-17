package dashboard

import (
	"net/http"
	"strings"
)

// identityCtxKey is the request-context key that carries the authenticated
// Identity. It is unset in ModeLocal (no auth) and populated in ModeServer.
type identityCtxKey struct{}

// withMiddleware composes the dashboard middleware: auth gating + access log.
func (d *Dashboard) withMiddleware(h http.Handler) http.Handler {
	h = d.withAuth(h)
	return h
}

// withAuth enforces authentication in ModeServer. ModeLocal is wide-open.
func (d *Dashboard) withAuth(next http.Handler) http.Handler {
	if d.opts.Mode == ModeLocal {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypass: login + the "who am I" probe always need to be reachable
		// (otherwise nobody can ever authenticate). Static SPA assets too,
		// so the login page itself can render.
		if isAuthBypass(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Defensive: ModeServer must have an Auth provider. If a consumer
		// embeds the dashboard incorrectly we surface a clean 503 instead
		// of a nil-pointer panic.
		if d.opts.Auth == nil {
			d.opts.Logger.Error().Str("hint", "ModeServer requires Auth to be configured").Msg("dashboard.auth_unconfigured")
			http.Error(w, "server auth is not configured", http.StatusServiceUnavailable)
			return
		}
		id, err := d.opts.Auth.Authenticate(r)
		if err != nil {
			d.opts.Logger.Warn().Str("path", r.URL.Path).Err(err).Msg("dashboard.auth_denied")
			w.Header().Set("WWW-Authenticate", `Bearer realm="jigsaw"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Viewers may not call any mutating verb.
		if isMutating(r.Method) && id.Role != RoleAdmin {
			d.opts.Logger.Warn().Str("path", r.URL.Path).Str("identity", id.Label).Msg("dashboard.role_denied")
			http.Error(w, "forbidden: viewer role cannot modify", http.StatusForbidden)
			return
		}
		// Stash identity for handlers that want it (audit, etc.).
		r = r.WithContext(contextWithIdentity(r.Context(), id))
		next.ServeHTTP(w, r)
	})
}

func isMutating(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// isAuthBypass identifies requests that must succeed even without auth:
// the login endpoint itself, the "who am I" probe (so the SPA can render
// a login page), and static assets that don't carry data.
func isAuthBypass(r *http.Request) bool {
	p := r.URL.Path
	switch p {
	case "/api/login", "/api/me", "/api/auth-info",
		"/auth/gitlab/login", "/auth/gitlab/callback":
		return true
	}
	// Static SPA: anything not under /api/ is served from the bundle. We let
	// it through; the SPA itself decides whether to redirect to /login based
	// on /api/me's response.
	if !strings.HasPrefix(p, "/api/") {
		return true
	}
	return false
}
