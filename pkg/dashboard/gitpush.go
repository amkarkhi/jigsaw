package dashboard

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitpush — per-user GitLab push pipeline.
//
// Flow at /api/git/push (POST):
//   1. authenticate the request (the dashboard's auth middleware did this)
//   2. load the user's GitSettings (base URL, project, default branch,
//      author identity, encrypted PAT) from the auth file
//   3. decrypt the PAT with the dashboard's server-side key
//   4. shell out to `git`:
//        - clone <base>/<project>.git into a temp dir, single-branch
//        - if the target branch exists, checkout; else create from HEAD
//        - rsync the live config tree (minus .jigsaw/) into the clone
//        - git add -A; git commit (author/email overridden); git push
//   5. return stdout/stderr + the URL the user can visit to see the result
//
// Secrets handling: the PAT is encrypted with AES-256-GCM under a key derived
// from JIGSAW_GIT_SECRET_KEY (sha256 of the string, so any length is fine for
// ops). The decrypted token only exists in the remote URL we hand to `git`
// and in a temporary env var; never logged.

const (
	gitPushTimeout = 90 * time.Second
)

// gitSecretKeyBytes is the encryption key (32 bytes) used to seal per-user
// PATs at rest. The dashboard CLI sets it from JIGSAW_GIT_SECRET_KEY before
// the HTTP server starts; an empty value disables the push feature for
// safety.
var gitSecretKeyBytes []byte

// SetGitSecretKey installs the AES key. Call once at startup, before any
// requests run. Empty string leaves the key unset (push disabled).
func SetGitSecretKey(raw string) {
	if raw == "" {
		gitSecretKeyBytes = nil
		return
	}
	sum := sha256.Sum256([]byte(raw))
	gitSecretKeyBytes = sum[:]
}

func gitSecretKey() []byte { return gitSecretKeyBytes }

func encryptPAT(plain string) (string, error) {
	key := gitSecretKey()
	if len(key) == 0 {
		return "", fmt.Errorf("server is missing JIGSAW_GIT_SECRET_KEY — refusing to store PAT in cleartext")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	out := append(nonce, ct...)
	return base64.RawStdEncoding.EncodeToString(out), nil
}

func decryptPAT(enc string) (string, error) {
	key := gitSecretKey()
	if len(key) == 0 {
		return "", fmt.Errorf("server is missing JIGSAW_GIT_SECRET_KEY — cannot decrypt PAT")
	}
	raw, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce := raw[:gcm.NonceSize()]
	ct := raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// requireAuthenticatedUser returns the request's identity in ModeServer, or
// a synthetic "local" identity in ModeLocal. Push endpoints require server
// mode because we need a per-user identity to attribute the commit to.
func (d *Dashboard) requireAuthenticatedUser(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	if d.opts.Mode != ModeServer {
		http.Error(w, "git push requires --mode=server", http.StatusBadRequest)
		return Identity{}, false
	}
	id, err := d.opts.Auth.Authenticate(r)
	if err != nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return Identity{}, false
	}
	return id, true
}

// /api/git/settings — GET reads the current user's settings, POST writes them.
// PAT is never returned in responses (just a "configured": bool flag).
func (d *Dashboard) handleGitSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := d.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		af, err := LoadAuthFile(d.opts.ConfigPath)
		if err != nil {
			writeError(w, err)
			return
		}
		var s UserGitSettings
		if af != nil {
			s = af.GitSettings[id.Label]
		}
		writeJSON(w, map[string]any{
			"base_url":       s.BaseURL,
			"project":        s.Project,
			"default_branch": s.DefaultBranch,
			"author_name":    s.AuthorName,
			"author_email":   s.AuthorEmail,
			"pat_configured": s.EncPAT != "",
			"secret_key_set": len(gitSecretKey()) > 0,
		})

	case http.MethodPost:
		var body struct {
			BaseURL       string `json:"base_url"`
			Project       string `json:"project"`
			DefaultBranch string `json:"default_branch"`
			AuthorName    string `json:"author_name"`
			AuthorEmail   string `json:"author_email"`
			PAT           string `json:"pat"`
			ClearPAT      bool   `json:"clear_pat"`
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
		if af.GitSettings == nil {
			af.GitSettings = map[string]UserGitSettings{}
		}
		existing := af.GitSettings[id.Label]
		next := UserGitSettings{
			BaseURL:       strings.TrimRight(strings.TrimSpace(body.BaseURL), "/"),
			Project:       strings.TrimPrefix(strings.TrimSpace(body.Project), "/"),
			DefaultBranch: strings.TrimSpace(body.DefaultBranch),
			AuthorName:    strings.TrimSpace(body.AuthorName),
			AuthorEmail:   strings.TrimSpace(body.AuthorEmail),
			EncPAT:        existing.EncPAT,
		}
		if body.ClearPAT {
			next.EncPAT = ""
		} else if strings.TrimSpace(body.PAT) != "" {
			enc, err := encryptPAT(strings.TrimSpace(body.PAT))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			next.EncPAT = enc
		}
		af.GitSettings[id.Label] = next
		if err := SaveAuthFile(d.opts.ConfigPath, af); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// /api/git/push — body: {branch, commit_message}. Returns
// {ok, branch, browse_url, output} or 4xx/5xx with a diagnostic.
func (d *Dashboard) handleGitPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, ok := d.requireAuthenticatedUser(w, r)
	if !ok {
		return
	}
	if !d.opts.Edit {
		http.Error(w, "edit mode is disabled (start with --edit)", http.StatusForbidden)
		return
	}

	var body struct {
		Branch        string `json:"branch"`
		CommitMessage string `json:"commit_message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	commitMsg := strings.TrimSpace(body.CommitMessage)
	if commitMsg == "" {
		http.Error(w, "commit_message is required", http.StatusBadRequest)
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
	s, has := af.GitSettings[id.Label]
	if !has {
		http.Error(w, "GitLab settings not configured for this user", http.StatusPreconditionRequired)
		return
	}
	if s.BaseURL == "" || s.Project == "" {
		http.Error(w, "base_url and project must be set in git settings", http.StatusPreconditionRequired)
		return
	}
	if s.EncPAT == "" {
		http.Error(w, "personal access token not configured", http.StatusPreconditionRequired)
		return
	}
	pat, err := decryptPAT(s.EncPAT)
	if err != nil {
		http.Error(w, "failed to decrypt PAT: "+err.Error(), http.StatusInternalServerError)
		return
	}

	branch := strings.TrimSpace(body.Branch)
	if branch == "" {
		branch = s.DefaultBranch
	}
	if branch == "" {
		branch = "jigsaw-configs"
	}
	if !safeBranch(branch) {
		http.Error(w, "branch name has unsafe characters", http.StatusBadRequest)
		return
	}

	authorName := s.AuthorName
	if authorName == "" {
		authorName = id.Label
	}
	authorEmail := s.AuthorEmail
	if authorEmail == "" {
		authorEmail = id.Label + "@gitlab.local"
	}

	ctx, cancel := context.WithTimeout(r.Context(), gitPushTimeout)
	defer cancel()

	output, err := runGitPush(ctx, gitPushInput{
		BaseURL:     s.BaseURL,
		Project:     s.Project,
		PAT:         pat,
		Branch:      branch,
		CommitMsg:   commitMsg,
		AuthorName:  authorName,
		AuthorEmail: authorEmail,
		SourceDir:   d.opts.ConfigPath,
	})
	if err != nil {
		d.opts.Logger.Warn().Err(err).Str("user", id.Label).Str("branch", branch).Msg("dashboard.git_push_failed")
		writeJSON(w, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"output": output,
		})
		return
	}
	d.opts.Logger.Info().Str("user", id.Label).Str("branch", branch).Msg("dashboard.git_push_ok")
	writeJSON(w, map[string]any{
		"ok":         true,
		"branch":     branch,
		"output":     output,
		"browse_url": fmt.Sprintf("%s/%s/-/tree/%s", strings.TrimRight(s.BaseURL, "/"), s.Project, url.PathEscape(branch)),
	})
}

type gitPushInput struct {
	BaseURL     string
	Project     string
	PAT         string
	Branch      string
	CommitMsg   string
	AuthorName  string
	AuthorEmail string
	SourceDir   string
}

// runGitPush executes the clone/copy/commit/push pipeline in a scratch dir.
// All git invocations share a sanitized env so credentials don't leak via
// askpass or shell quirks.
func runGitPush(ctx context.Context, in gitPushInput) (string, error) {
	tmp, err := os.MkdirTemp("", "jigsaw-push-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	clone := filepath.Join(tmp, "repo")

	// Build the authenticated remote URL once and reuse it. GitLab accepts
	// "oauth2:<PAT>" as basic-auth for HTTPS pushes.
	parsed, err := url.Parse(in.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	parsed.User = url.UserPassword("oauth2", in.PAT)
	remote := strings.TrimRight(parsed.String(), "/") + "/" + in.Project + ".git"

	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
		"GIT_AUTHOR_NAME="+in.AuthorName,
		"GIT_AUTHOR_EMAIL="+in.AuthorEmail,
		"GIT_COMMITTER_NAME="+in.AuthorName,
		"GIT_COMMITTER_EMAIL="+in.AuthorEmail,
	)

	var transcript strings.Builder

	// 1. Shallow clone — single branch when the user named one, otherwise
	//    default. If the named branch doesn't exist we fall back to the
	//    repo's default and create the branch locally.
	cloneArgs := []string{"clone", "--depth", "1", "--branch", in.Branch, remote, clone}
	if _, err := runGitStep(ctx, env, tmp, cloneArgs, &transcript); err != nil {
		// Branch likely doesn't exist; clone default and we'll create it.
		if _, err2 := runGitStep(ctx, env, tmp, []string{"clone", "--depth", "1", remote, clone}, &transcript); err2 != nil {
			return transcript.String(), fmt.Errorf("clone failed: %w", err2)
		}
		if _, err := runGitStep(ctx, env, clone, []string{"checkout", "-B", in.Branch}, &transcript); err != nil {
			return transcript.String(), fmt.Errorf("create branch %q: %w", in.Branch, err)
		}
	}

	// 2. Replace the clone's tracked content with our config tree. We do a
	//    full sync: remove everything except .git/, then copy our config
	//    tree in. Anything previously in the repo that isn't in our tree
	//    will be deleted in the commit. This matches the "the dashboard is
	//    the source of truth" semantics the user asked for.
	if err := wipeExceptGit(clone); err != nil {
		return transcript.String(), fmt.Errorf("wipe clone: %w", err)
	}
	if err := copyConfigTree(in.SourceDir, clone); err != nil {
		return transcript.String(), fmt.Errorf("copy configs: %w", err)
	}

	// 3. Stage + commit + push.
	if _, err := runGitStep(ctx, env, clone, []string{"add", "-A"}, &transcript); err != nil {
		return transcript.String(), err
	}
	// Skip the commit if nothing changed; otherwise git commit would error
	// with "nothing to commit" and we'd surface that as a failure.
	if hasChanges, err := runGitStep(ctx, env, clone, []string{"status", "--porcelain"}, &transcript); err != nil {
		return transcript.String(), err
	} else if strings.TrimSpace(hasChanges) == "" {
		transcript.WriteString("\n(nothing to commit — working tree matches remote)\n")
		return transcript.String(), nil
	}
	if _, err := runGitStep(ctx, env, clone, []string{"commit", "-m", in.CommitMsg}, &transcript); err != nil {
		return transcript.String(), err
	}
	if _, err := runGitStep(ctx, env, clone, []string{"push", "-u", "origin", in.Branch}, &transcript); err != nil {
		return transcript.String(), err
	}
	return transcript.String(), nil
}

// runGitStep runs a single git command, appending the command + output to the
// transcript (PAT redacted). Returns stdout for callers that need to inspect it.
func runGitStep(ctx context.Context, env []string, dir string, args []string, transcript *strings.Builder) (string, error) {
	transcript.WriteString("$ git ")
	transcript.WriteString(strings.Join(redactArgs(args), " "))
	transcript.WriteString("\n")

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	redacted := redactToken(string(out))
	transcript.WriteString(redacted)
	if !strings.HasSuffix(redacted, "\n") {
		transcript.WriteString("\n")
	}
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}

func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = redactToken(a)
	}
	return out
}

// redactToken masks "oauth2:<anything>@" inside a string. Catches the cases
// where git happens to echo the remote URL.
func redactToken(s string) string {
	for {
		i := strings.Index(s, "oauth2:")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "@")
		if j < 0 {
			return s[:i] + "oauth2:***"
		}
		s = s[:i] + "oauth2:***" + s[i+j:]
	}
}

// wipeExceptGit removes all entries in dir except a ".git" directory.
func wipeExceptGit(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyConfigTree copies everything under src to dst, skipping the dashboard's
// own sidecar directory (.jigsaw/) — that holds auth secrets and is not
// safe to publish.
func copyConfigTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip the sidecar/auth area at any depth.
		if strings.HasPrefix(rel, sidecarDir+string(os.PathSeparator)) || rel == sidecarDir {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// safeBranch checks for shell/path injection in branch names. Git accepts a
// wide range of characters, but we constrain to a sane subset because the
// name flows into `git checkout -B`/`git push`.
func safeBranch(b string) bool {
	if b == "" || strings.HasPrefix(b, "-") || strings.Contains(b, "..") {
		return false
	}
	for _, r := range b {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '/' || r == '_' || r == '-' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}
