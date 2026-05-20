package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kirillkuzin/postamat/internal/api"
	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestRouterHealthz(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", response.Code, http.StatusOK)
	}
	if got, want := response.Body.String(), "ok\n"; got != want {
		t.Fatalf("GET /healthz body = %q, want %q", got, want)
	}
}

func TestRouterRejectsUnknownRoute(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /missing status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestRouterRejectsUnsupportedMethod(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/v1/transfers", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PATCH /api/v1/transfers status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func newTestRouter() http.Handler {
	return api.NewRouter(sessions.NewService(sessions.NewMemoryRepository(), &fixedTokenIssuer{}, nil))
}

type fixedTokenIssuer struct {
	transfer int
	public   int
	agent    int
}

func (f *fixedTokenIssuer) NewTransferID() string {
	f.transfer++
	return "transfer_" + string(rune('0'+f.transfer))
}

func (f *fixedTokenIssuer) NewPublicToken() sessions.StoredToken {
	f.public++
	return sessions.StoredToken{Raw: "public_token_" + string(rune('0'+f.public)), Stored: "public_hash_" + string(rune('0'+f.public))}
}

func (f *fixedTokenIssuer) NewAgentTicket() sessions.StoredToken {
	f.agent++
	return sessions.StoredToken{Raw: "agent_ticket_" + string(rune('0'+f.agent)), Stored: "agent_hash_" + string(rune('0'+f.agent))}
}
