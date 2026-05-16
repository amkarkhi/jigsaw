package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Auth file lives at <config>/.jigsaw/auth.json and contains:
//   - master key fingerprint: SHA-256 of the master key; used to gate
//     management operations (user/token create/delete). Cheap to verify,
//     impossible to reverse from the file.
//   - users: username + bcrypt password hash + role
//   - tokens: name + sha256(token) + role. Plaintext tokens are shown
//     exactly once at creation time.
//
// Server-mode dashboard loads this file. Local-mode ignores it.

const authFilename = "auth.json"
const authSchemaVersion = "1"

type AuthFile struct {
	Version          string          `json:"version"`
	MasterFingerprint string         `json:"master_fingerprint"`
	Users            []AuthUser      `json:"users"`
	Tokens           []AuthToken     `json:"tokens"`
}

type AuthUser struct {
	Username  string `json:"username"`
	Role      string `json:"role"` // "admin" | "viewer"
	Hash      string `json:"hash"` // bcrypt hash of password
	CreatedAt string `json:"created_at"`
}

type AuthToken struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Hash      string `json:"hash"` // sha256(token), hex
	CreatedAt string `json:"created_at"`
}

// authFilePath returns the on-disk path for a given config root.
func authFilePath(configPath string) string {
	return filepath.Join(configPath, sidecarDir, authFilename)
}

// LoadAuthFile reads the auth file. Returns (nil, nil) if it does not exist —
// callers treat absence as "auth not initialized" rather than an error.
func LoadAuthFile(configPath string) (*AuthFile, error) {
	data, err := os.ReadFile(authFilePath(configPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var af AuthFile
	if err := json.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("parse auth file: %w", err)
	}
	if af.Version != authSchemaVersion {
		return nil, fmt.Errorf("auth file schema %q is not supported (want %q)", af.Version, authSchemaVersion)
	}
	return &af, nil
}

// SaveAuthFile writes the auth file atomically (tmp + rename) with 0600
// permissions because it contains password hashes.
func SaveAuthFile(configPath string, af *AuthFile) error {
	af.Version = authSchemaVersion
	p := authFilePath(configPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// InitAuthFile creates a brand-new auth file with the master-key fingerprint
// set. Refuses to overwrite an existing file.
func InitAuthFile(configPath, masterKey string) error {
	existing, err := LoadAuthFile(configPath)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("auth file already exists at %s", authFilePath(configPath))
	}
	if len(masterKey) < 16 {
		return fmt.Errorf("master key must be at least 16 characters")
	}
	af := &AuthFile{
		Version:          authSchemaVersion,
		MasterFingerprint: fingerprint(masterKey),
	}
	return SaveAuthFile(configPath, af)
}

// VerifyMasterKey returns true if the provided key matches the file's
// fingerprint.
func (af *AuthFile) VerifyMasterKey(key string) bool {
	if af == nil || af.MasterFingerprint == "" {
		return false
	}
	want, err := hex.DecodeString(af.MasterFingerprint)
	if err != nil {
		return false
	}
	got := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

func fingerprint(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// CreateUser adds a new user with a bcrypted password. Refuses duplicate
// usernames and roles other than admin/viewer.
func (af *AuthFile) CreateUser(username, password, role string) error {
	if !validUsername(username) {
		return fmt.Errorf("invalid username (letters, digits, underscore, dash; 1-32 chars)")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if role != "admin" && role != "viewer" {
		return fmt.Errorf("role must be 'admin' or 'viewer'")
	}
	for _, u := range af.Users {
		if u.Username == username {
			return fmt.Errorf("user %q already exists", username)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	af.Users = append(af.Users, AuthUser{
		Username:  username,
		Role:      role,
		Hash:      string(hash),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

// DeleteUser removes a user by name. Returns false if no such user.
func (af *AuthFile) DeleteUser(username string) bool {
	for i, u := range af.Users {
		if u.Username == username {
			af.Users = append(af.Users[:i], af.Users[i+1:]...)
			return true
		}
	}
	return false
}

// VerifyUserPassword constant-time-checks the password against the stored
// bcrypt hash for `username`. Returns the user's role on success.
func (af *AuthFile) VerifyUserPassword(username, password string) (string, bool) {
	for _, u := range af.Users {
		if u.Username == username {
			if err := bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)); err == nil {
				return u.Role, true
			}
			return "", false
		}
	}
	return "", false
}

// CreateToken generates a fresh random bearer token, stores only its hash,
// and returns the plaintext token to the caller. The plaintext is the only
// chance to copy it.
func (af *AuthFile) CreateToken(name, role string) (string, error) {
	if !validUsername(name) {
		return "", fmt.Errorf("invalid token name")
	}
	if role != "admin" && role != "viewer" {
		return "", fmt.Errorf("role must be 'admin' or 'viewer'")
	}
	for _, t := range af.Tokens {
		if t.Name == name {
			return "", fmt.Errorf("token %q already exists", name)
		}
	}
	// 32 random bytes → 43-char base64-url (no padding). Plenty of entropy.
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	af.Tokens = append(af.Tokens, AuthToken{
		Name:      name,
		Role:      role,
		Hash:      hashToken(token),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return token, nil
}

// RevokeToken removes a token by name. Returns false if no such token.
func (af *AuthFile) RevokeToken(name string) bool {
	for i, t := range af.Tokens {
		if t.Name == name {
			af.Tokens = append(af.Tokens[:i], af.Tokens[i+1:]...)
			return true
		}
	}
	return false
}

// VerifyToken returns the matching token's role on success.
func (af *AuthFile) VerifyToken(token string) (string, bool) {
	h := hashToken(token)
	hBytes, _ := hex.DecodeString(h)
	for _, t := range af.Tokens {
		stored, err := hex.DecodeString(t.Hash)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(hBytes, stored) == 1 {
			return t.Role, true
		}
	}
	return "", false
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func validUsername(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// FileAuth — an AuthProvider backed by the auth file plus an in-memory session
// store. Cookie-based sessions for browsers, bearer-token header for API.
// -----------------------------------------------------------------------------

type session struct {
	username string
	role     Role
	expires  time.Time
}

// FileAuth is the AuthProvider implementation used by `--mode=server`.
// It wraps an AuthFile and a session map keyed by signed cookie id.
type FileAuth struct {
	configPath string
	mu         sync.RWMutex
	sessions   map[string]session
}

// NewFileAuth constructs a FileAuth bound to the given config root. It does
// not load the file eagerly — each request re-reads it so user/token mutations
// from the CLI take effect immediately. In practice this is cheap (small JSON).
func NewFileAuth(configPath string) *FileAuth {
	return &FileAuth{
		configPath: configPath,
		sessions:   make(map[string]session),
	}
}

// EnsureInitialized refuses to start ModeServer with no auth file.
func (f *FileAuth) EnsureInitialized() error {
	af, err := LoadAuthFile(f.configPath)
	if err != nil {
		return err
	}
	if af == nil {
		return fmt.Errorf(
			"auth file not found at %s — run `jigsaw user init` first",
			authFilePath(f.configPath),
		)
	}
	if len(af.Users) == 0 && len(af.Tokens) == 0 {
		return fmt.Errorf(
			"auth file has no users or tokens — run `jigsaw user create` first",
		)
	}
	return nil
}

// Authenticate satisfies AuthProvider. Order of precedence:
//   1. session cookie
//   2. Authorization: Bearer <token>
// Anything else → 401.
func (f *FileAuth) Authenticate(r *http.Request) (Identity, error) {
	if c, err := r.Cookie("jigsaw_session"); err == nil && c.Value != "" {
		f.mu.RLock()
		s, ok := f.sessions[c.Value]
		f.mu.RUnlock()
		if ok && time.Now().Before(s.expires) {
			return Identity{Label: s.username, Role: s.role}, nil
		}
	}

	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if token != "" {
			af, err := LoadAuthFile(f.configPath)
			if err == nil && af != nil {
				if role, ok := af.VerifyToken(token); ok {
					return Identity{Label: "token", Role: roleFromString(role)}, nil
				}
			}
		}
	}

	return Identity{}, fmt.Errorf("not authenticated")
}

// Login verifies a username+password against the auth file, creates a
// session, and returns the session id. The caller sets it as a cookie.
func (f *FileAuth) Login(username, password string) (string, Identity, error) {
	af, err := LoadAuthFile(f.configPath)
	if err != nil {
		return "", Identity{}, err
	}
	if af == nil {
		return "", Identity{}, fmt.Errorf("auth not initialized")
	}
	role, ok := af.VerifyUserPassword(username, password)
	if !ok {
		return "", Identity{}, fmt.Errorf("invalid credentials")
	}
	id := newSessionID()
	f.mu.Lock()
	f.sessions[id] = session{
		username: username,
		role:     roleFromString(role),
		expires:  time.Now().Add(12 * time.Hour),
	}
	f.mu.Unlock()
	return id, Identity{Label: username, Role: roleFromString(role)}, nil
}

// Logout invalidates a session id.
func (f *FileAuth) Logout(id string) {
	f.mu.Lock()
	delete(f.sessions, id)
	f.mu.Unlock()
}

func newSessionID() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func roleFromString(s string) Role {
	if s == "admin" {
		return RoleAdmin
	}
	return RoleViewer
}
