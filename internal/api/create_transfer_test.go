package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateTransferReturnsCreatedTransfer(t *testing.T) {
	handler := newTestRouter()
	body := []byte(`{
		"from_agent_id":"agent_a",
		"to_agent_id":"agent_b",
		"target":"agent",
		"file_name":"report.pdf",
		"file_size_bytes":42
	}`)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewReader(body)))

	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/transfers status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON decode: %v", err)
	}
	if got["transfer_id"] != "transfer_1" {
		t.Fatalf("transfer_id = %v, want transfer_1", got["transfer_id"])
	}
	if got["status"] != "created" {
		t.Fatalf("status = %v, want created", got["status"])
	}
	if got["target"] != "agent" {
		t.Fatalf("target = %v, want agent", got["target"])
	}
	if got["transport"] != "webrtc_p2p" {
		t.Fatalf("transport = %v, want webrtc_p2p", got["transport"])
	}
	if got["agent_ticket"] != "agent_ticket_1" {
		t.Fatalf("agent_ticket = %v, want agent_ticket_1", got["agent_ticket"])
	}
	if _, ok := got["public_token"]; ok {
		t.Fatalf("agent target response must not include public_token: %#v", got)
	}
}

func TestCreateBrowserLinkTransferReturnsPublicToken(t *testing.T) {
	handler := newTestRouter()
	body := []byte(`{
		"from_agent_id":"agent_a",
		"target":"browser_link",
		"file_name":"report.pdf",
		"file_size_bytes":42
	}`)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewReader(body)))

	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/transfers status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON decode: %v", err)
	}
	if got["public_token"] != "public_token_1" {
		t.Fatalf("public_token = %v, want public_token_1", got["public_token"])
	}
}

func TestCreateTransferRejectsInvalidJSON(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewReader([]byte(`{`))))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCreateTransferRejectsInvalidMetadata(t *testing.T) {
	handler := newTestRouter()
	body := []byte(`{"from_agent_id":"agent_a","target":"agent","file_name":"report.pdf","file_size_bytes":42}`)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewReader(body)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid metadata status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}
