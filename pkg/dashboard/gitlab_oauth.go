package dashboard

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitLab SSO via OAuth 2.0 Authorization Code flow.
//
// This wires a self-hosted GitLab instance (or gitlab.com) as an alternative
// to username+password login. Flow:
//
//   1. /auth/gitlab/login  — generate state, set state cookie, 302 → GitLab
//   2. user authorizes on GitLab → GitLab 302 → /auth/gitlab/callback?code=…&state=…
//   3. /auth/gitlab/callback validates state cookie, swaps code for access
//      token at <BaseURL>/oauth/token, fetches <BaseURL>/api/v4/user, then
//      either looks up the user in the auth file or provisions them with the
//      configured default role. Issues a `jigsaw_session` cookie and 302 → "/".
//
// Configuration lives in dashboard.Options.GitLabOAuth (see dashboard.go).
// All four fields must be set for SSO to be considered enabled.

const (
	gitlabStateCookie  = "jigsaw_gitlab_state"
	gitlabStateMaxAge  = 10 * 60 // seconds
	gitlabHTTPTimeout  = 15 * time.Second
	gitlabReqScope     = "read_user"
)

// GitLabOAuthConfig configures the GitLab Authorization Code flow. All fields
// except DefaultRole are required; DefaultRole defaults to "viewer" when
// empty. The dashboard only enables SSO when Enabled() returns true.
type GitLabOAuthConfig struct {
	// BaseURL is the GitLab origin, e.g. "https://gitlab.example.com".
	// No trailing slash.
	BaseURL string

	// ClientID and ClientSecret are issued by GitLab when you register an
	// OAuth application. The redirect URI on that application must match
	// RedirectURL exactly.
	ClientID     string
	ClientSecret string

	// RedirectURL is the absolute callback URL the dashboard exposes, e.g.
	// "https://jigsaw.example.com/auth/gitlab/callback".
	RedirectURL string

	// DefaultRole is the role granted to users on their first SSO login.
	// Subsequent logins use whatever role the auth file records, so admins
	// can promote users out of band. Defaults to "viewer".
	DefaultRole string
}

// Enabled reports whether the dashboard should accept the SSO route at all.
func (g *GitLabOAuthConfig) Enabled() bool {
	if g == nil {
		return false
	}
	return g.BaseURL != "" && g.ClientID != "" && g.ClientSecret != "" && g.RedirectURL != ""
}

// authorizeURL builds the URL the browser is redirected to for GitLab's
// authorization prompt.
func (g *GitLabOAuthConfig) authorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", g.ClientID)
	q.Set("redirect_uri", g.RedirectURL)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("scope", gitlabReqScope)
	return strings.TrimRight(g.BaseURL, "/") + "/oauth/authorize?" + q.Encode()
}

func (g *GitLabOAuthConfig) tokenURL() string {
	return strings.TrimRight(g.BaseURL, "/") + "/oauth/token"
}

func (g *GitLabOAuthConfig) userURL() string {
	return strings.TrimRight(g.BaseURL, "/") + "/api/v4/user"
}

// handleGitLabLogin is the entry point. Generates a random state, stashes it
// in an HttpOnly cookie, and 302's the browser to GitLab.
func (d *Dashboard) handleGitLabLogin(w http.ResponseWriter, r *http.Request) {
	g := d.opts.GitLabOAuth
	if !g.Enabled() {
		http.Error(w, "GitLab SSO is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok := d.opts.Auth.(*FileAuth); !ok {
		http.Error(w, "GitLab SSO requires file-based auth", http.StatusServiceUnavailable)
		return
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, err)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name:     gitlabStateCookie,
		Value:    state,
		Path:     "/auth/gitlab/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPSReq(r),
		MaxAge:   gitlabStateMaxAge,
	})
	http.Redirect(w, r, g.authorizeURL(state), http.StatusFound)
}

// handleGitLabCallback completes the flow: verify state, swap code, fetch
// user, issue a Jigsaw session.
func (d *Dashboard) handleGitLabCallback(w http.ResponseWriter, r *http.Request) {
	g := d.opts.GitLabOAuth
	if !g.Enabled() {
		http.Error(w, "GitLab SSO is not configured", http.StatusServiceUnavailable)
		return
	}
	fa, ok := d.opts.Auth.(*FileAuth)
	if !ok {
		http.Error(w, "GitLab SSO requires file-based auth", http.StatusServiceUnavailable)
		return
	}

	// 1. State check — we set the cookie at /auth/gitlab/login. Mismatch =>
	//    bail; it's either CSRF or a stale browser tab.
	stateCookie, err := r.Cookie(gitlabStateCookie)
	if err != nil || stateCookie.Value == "" {
		http.Error(w, "missing OAuth state cookie", http.StatusBadRequest)
		return
	}
	returned := r.URL.Query().Get("state")
	if returned == "" || returned != stateCookie.Value {
		http.Error(w, "OAuth state mismatch", http.StatusBadRequest)
		return
	}
	// Clear the state cookie regardless of outcome.
	http.SetCookie(w, &http.Cookie{
		Name:     gitlabStateCookie,
		Value:    "",
		Path:     "/auth/gitlab/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		// GitLab passes ?error= when the user denies. Surface it verbatim
		// so the user knows what happened.
		if e := r.URL.Query().Get("error"); e != "" {
			http.Error(w, "GitLab auth: "+e, http.StatusUnauthorized)
			return
		}
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	// 2. Exchange code for access token.
	tok, err := exchangeGitLabCode(g, code)
	if err != nil {
		d.opts.Logger.Warn().Err(err).Msg("dashboard.gitlab_oauth_token_exchange_failed")
		http.Error(w, "GitLab token exchange failed", http.StatusBadGateway)
		return
	}

	// 3. Fetch user identity from GitLab.
	username, err := fetchGitLabUsername(g, tok.AccessToken)
	if err != nil {
		d.opts.Logger.Warn().Err(err).Msg("dashboard.gitlab_oauth_user_fetch_failed")
		http.Error(w, "GitLab user lookup failed", http.StatusBadGateway)
		return
	}

	// 4. Provision (if first login) + create Jigsaw session.
	sid, id, err := fa.LoginOAuth(username, g.DefaultRole)
	if err != nil {
		d.opts.Logger.Warn().Err(err).Str("username", username).Msg("dashboard.gitlab_oauth_provision_failed")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "jigsaw_session",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPSReq(r),
		Expires:  time.Now().Add(12 * time.Hour),
	})
	d.opts.Logger.Info().Str("username", id.Label).Str("role", roleString(id.Role)).Msg("dashboard.gitlab_oauth_login")
	http.Redirect(w, r, "/", http.StatusFound)
}

type gitlabTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

func exchangeGitLabCode(g *GitLabOAuthConfig, code string) (*gitlabTokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", g.ClientID)
	form.Set("client_secret", g.ClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", g.RedirectURL)

	req, err := http.NewRequest(http.MethodPost, g.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: gitlabHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var out gitlabTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	return &out, nil
}

type gitlabUserResponse struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

func fetchGitLabUsername(g *GitLabOAuthConfig, accessToken string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, g.userURL(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: gitlabHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("user endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var u gitlabUserResponse
	if err := json.Unmarshal(body, &u); err != nil {
		return "", fmt.Errorf("decode user response: %w", err)
	}
	if u.Username == "" {
		return "", fmt.Errorf("GitLab user has no username")
	}
	return u.Username, nil
}

// handleAuthInfo reports which auth methods the SPA should offer to the user.
// Unauthenticated; bypassed by the auth middleware.
func (d *Dashboard) handleAuthInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := map[string]any{
		"password": d.opts.Mode == ModeServer,
		"gitlab":   d.opts.GitLabOAuth.Enabled(),
	}
	writeJSON(w, resp)
}
