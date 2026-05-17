package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// TokenInfo describes one valid bearer token: the label it represents and
// the role it grants.
type TokenInfo struct {
	Label string
	Role  Role
}

// BearerTokens returns an AuthProvider that accepts any token in the map.
// Tokens are compared with constant-time-ish equality (length-checked map
// lookup); rotate by swapping the map.
//
// Token strings should be high-entropy random bytes (>= 32 bytes recommended).
// They are kept in memory; persistence is the consumer's responsibility.
func BearerTokens(tokens map[string]TokenInfo) AuthProvider {
	return &bearerAuth{tokens: tokens}
}

type bearerAuth struct {
	tokens map[string]TokenInfo
}

func (b *bearerAuth) Authenticate(r *http.Request) (Identity, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return Identity{}, fmt.Errorf("missing Authorization header")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return Identity{}, fmt.Errorf("Authorization header must use Bearer scheme")
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return Identity{}, fmt.Errorf("empty bearer token")
	}
	info, ok := b.tokens[token]
	if !ok {
		return Identity{}, fmt.Errorf("invalid token")
	}
	return Identity{Label: info.Label, Role: info.Role, Access: deriveAccess(info.Role, nil)}, nil
}

// CustomAuth lets a consumer plug in their own auth function — useful when
// they already have middleware (SSO, mTLS, signed cookies) they want to
// reuse. The function returns an Identity on success or an error on denial.
func CustomAuth(fn func(*http.Request) (Identity, error)) AuthProvider {
	return authFunc(fn)
}

type authFunc func(*http.Request) (Identity, error)

func (f authFunc) Authenticate(r *http.Request) (Identity, error) { return f(r) }

// contextWithIdentity returns a context carrying the given identity.
func contextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext recovers the authenticated identity, if any.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(Identity)
	return id, ok
}
