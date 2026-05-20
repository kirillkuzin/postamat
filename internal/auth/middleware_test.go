package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearerMiddlewareAuthenticatesTokenAndStoresIdentityInContext(t *testing.T) {
	store := StaticTokenStore{
		"token-send": TokenIdentity{
			AgentID:  "agent-a",
			DeviceID: "device-1",
			Scopes:   []Scope{ScopeTransferCreate, ScopeTransferRead},
		},
	}
	var got Identity
	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		identity, ok := IdentityFromContext(req.Context())
		if !ok {
			t.Fatal("identity missing from request context")
		}
		got = identity
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", nil)
	req.Header.Set("Authorization", "Bearer token-send")
	res := httptest.NewRecorder()
	BearerMiddleware(store, ScopeTransferCreate)(next).ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
	if got.AgentID != "agent-a" || got.DeviceID != "device-1" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	if !got.HasScope(ScopeTransferRead) {
		t.Fatalf("expected identity to preserve granted scopes: %#v", got.Scopes)
	}
}

func TestBearerMiddlewareRejectsMissingMalformedUnknownAndInsufficientScope(t *testing.T) {
	store := StaticTokenStore{
		"token-read": TokenIdentity{AgentID: "agent-a", DeviceID: "device-1", Scopes: []Scope{ScopeTransferRead}},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Fatal("next handler should not run")
	})

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "malformed", authorization: "Basic abc", wantStatus: http.StatusUnauthorized},
		{name: "unknown", authorization: "Bearer unknown", wantStatus: http.StatusUnauthorized},
		{name: "insufficient scope", authorization: "Bearer token-read", wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", nil)
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			res := httptest.NewRecorder()
			BearerMiddleware(store, ScopeTransferCreate)(next).ServeHTTP(res, req)
			if res.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, tc.wantStatus)
			}
		})
	}
}

func TestContextWithIdentityRoundTripsIdentity(t *testing.T) {
	ctx := ContextWithIdentity(context.Background(), Identity{AgentID: "agent-a", DeviceID: "device-1"})
	identity, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("expected identity in context")
	}
	if identity.AgentID != "agent-a" || identity.DeviceID != "device-1" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}
