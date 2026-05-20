package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type Scope string

const (
	ScopeTransferCreate Scope = "transfer:create"
	ScopeTransferRead   Scope = "transfer:read"
	ScopeTransferCancel Scope = "transfer:cancel"
	ScopeAgentRead      Scope = "agent:read"
)

var ErrTokenNotFound = errors.New("token not found")

type Identity struct {
	AgentID  string
	DeviceID string
	Scopes   []Scope
}

type TokenIdentity = Identity

type TokenStore interface {
	LookupToken(ctx context.Context, rawToken string) (TokenIdentity, error)
}

type StaticTokenStore map[string]TokenIdentity

func (s StaticTokenStore) LookupToken(_ context.Context, rawToken string) (TokenIdentity, error) {
	identity, ok := s[rawToken]
	if !ok {
		return TokenIdentity{}, ErrTokenNotFound
	}
	return identity, nil
}

type identityContextKey struct{}

func ContextWithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}

func (i Identity) HasScope(scope Scope) bool {
	for _, candidate := range i.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func BearerMiddleware(store TokenStore, requiredScopes ...Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			raw, ok := bearerToken(req.Header.Get("Authorization"))
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			identity, err := store.LookupToken(req.Context(), raw)
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			for _, scope := range requiredScopes {
				if !identity.HasScope(scope) {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, req.WithContext(ContextWithIdentity(req.Context(), Identity(identity))))
		})
	}
}

func bearerToken(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	prefix, token, found := strings.Cut(header, " ")
	if !found || prefix != "Bearer" || token == "" || strings.Contains(token, " ") {
		return "", false
	}
	return token, true
}
