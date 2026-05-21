package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRecipientPageServedForPublicTokenRoute(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/p/public_token_1", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET /p/{token} status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
	if !strings.Contains(response.Body.String(), "id=\"app\"") {
		t.Fatalf("recipient page should serve SPA entry, body=%s", response.Body.String())
	}
}

func TestPublicTransferMetadataDoesNotExposeSecrets(t *testing.T) {
	handler := newTestRouter()
	publicToken := createBrowserLinkViaHTTP(t, handler)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/public/transfers/"+url.PathEscape(publicToken), nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET public metadata status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	for _, forbidden := range []string{"public_token", "agent_ticket", "public_token_hash", "agent_ticket_hash", "receiver_ticket", "password_hash"} {
		if _, ok := got[forbidden]; ok {
			t.Fatalf("metadata must not expose %q: %#v", forbidden, got)
		}
	}
	if got["file_name"] != "report.pdf" || got["file_size_bytes"] != float64(42) || got["from_agent_id"] != "agent_a" {
		t.Fatalf("unexpected metadata: %#v", got)
	}
	if got["password_required"] != false {
		t.Fatalf("password_required = %v, want false", got["password_required"])
	}
}

func TestPublicTransferReceiverTicketRequiresConsentAndIsShortLived(t *testing.T) {
	handler := newTestRouter()
	publicToken := createBrowserLinkViaHTTP(t, handler)

	noConsent := httptest.NewRecorder()
	handler.ServeHTTP(noConsent, httptest.NewRequest(http.MethodPost, "/api/public/transfers/"+url.PathEscape(publicToken)+"/receiver-ticket", bytes.NewReader([]byte(`{"consent":false}`))))
	if noConsent.Code != http.StatusBadRequest {
		t.Fatalf("receiver ticket without consent status = %d, want %d", noConsent.Code, http.StatusBadRequest)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/public/transfers/"+url.PathEscape(publicToken)+"/receiver-ticket", bytes.NewReader([]byte(`{"consent":true}`))))
	if response.Code != http.StatusCreated {
		t.Fatalf("receiver ticket status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode receiver ticket: %v", err)
	}
	if got["receiver_ticket"] == "" || got["receiver_ticket"] == publicToken {
		t.Fatalf("receiver_ticket must be non-empty and distinct from public token: %#v", got)
	}
	if got["expires_at"] == "" {
		t.Fatalf("receiver ticket response must include expires_at: %#v", got)
	}
	if !strings.HasPrefix(got["browser_agent_id"].(string), "browser_recipient:transfer_") {
		t.Fatalf("browser_agent_id must be transfer-scoped: %#v", got)
	}
	if strings.Contains(response.Body.String(), publicToken) {
		t.Fatalf("receiver ticket response must not echo public token in any field: %s", response.Body.String())
	}
	if _, ok := got["public_token"]; ok {
		t.Fatalf("receiver ticket response must not expose public_token field: %#v", got)
	}
}

func createBrowserLinkViaHTTP(t *testing.T, handler http.Handler) string {
	t.Helper()
	body := []byte(`{
		"from_agent_id":"agent_a",
		"target":"browser_link",
		"file_name":"report.pdf",
		"file_size_bytes":42
	}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("create browser transfer status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	publicToken, ok := got["public_token"].(string)
	if !ok || publicToken == "" {
		t.Fatalf("missing public_token in create response: %#v", got)
	}
	return publicToken
}
