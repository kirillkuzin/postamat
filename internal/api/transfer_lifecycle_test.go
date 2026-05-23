package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/api"
	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestGetTransferReturnsExistingTransfer(t *testing.T) {
	handler := newTestRouter()
	created := createAgentTransfer(t, handler, "report.pdf")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/transfers/"+created["transfer_id"].(string), nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET transfer status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON decode: %v", err)
	}
	if got["transfer_id"] != created["transfer_id"] {
		t.Fatalf("transfer_id = %v, want %v", got["transfer_id"], created["transfer_id"])
	}
	if got["file_name"] != "report.pdf" {
		t.Fatalf("file_name = %v, want report.pdf", got["file_name"])
	}
}

func TestListActiveTransfersReturnsNonTerminalTransfers(t *testing.T) {
	handler := newTestRouter()
	cancelled := createAgentTransfer(t, handler, "cancelled.pdf")
	active := createAgentTransfer(t, handler, "active.pdf")
	deleteTransfer(t, handler, cancelled["transfer_id"].(string))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/transfers?status=active", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("list active status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var got struct {
		Transfers []map[string]any `json:"transfers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON decode: %v", err)
	}
	if len(got.Transfers) != 1 {
		t.Fatalf("active count = %d, want 1: %#v", len(got.Transfers), got.Transfers)
	}
	if got.Transfers[0]["transfer_id"] != active["transfer_id"] {
		t.Fatalf("active transfer_id = %v, want %v", got.Transfers[0]["transfer_id"], active["transfer_id"])
	}
}

func TestListActiveTransfersExcludesTTLExpiredByClock(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	service := sessions.NewService(sessions.NewMemoryRepository(), &fixedTokenIssuer{}, func() time.Time { return now })
	handler := api.NewRouter(service)
	body := []byte(`{"from_agent_id":"agent_a","to_agent_id":"agent_b","target":"agent","file_name":"expired.pdf","file_size_bytes":42,"ttl_seconds":1}`)
	createExpired := httptest.NewRecorder()
	handler.ServeHTTP(createExpired, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewReader(body)))
	if createExpired.Code != http.StatusCreated {
		t.Fatalf("create expiring transfer status = %d, body=%s", createExpired.Code, createExpired.Body.String())
	}
	now = now.Add(2 * time.Second)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/transfers?status=active", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list active status = %d, body=%s", response.Code, response.Body.String())
	}
	var got struct {
		Transfers []map[string]any `json:"transfers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON decode: %v", err)
	}
	if len(got.Transfers) != 0 {
		t.Fatalf("expired transfer should not be listed active: %#v", got.Transfers)
	}
}

func TestCancelTransferReturnsCancelledTransfer(t *testing.T) {
	handler := newTestRouter()
	created := createAgentTransfer(t, handler, "report.pdf")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/transfers/"+created["transfer_id"].(string), nil))

	if response.Code != http.StatusOK {
		t.Fatalf("DELETE transfer status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON decode: %v", err)
	}
	if got["status"] != "cancelled" {
		t.Fatalf("status = %v, want cancelled", got["status"])
	}
}

func TestTransferLifecycleReturnsNotFound(t *testing.T) {
	handler := newTestRouter()

	for _, tt := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/transfers/missing"},
		{method: http.MethodDelete, path: "/api/v1/transfers/missing"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s status = %d, want %d", tt.method, tt.path, response.Code, http.StatusNotFound)
			}
		})
	}
}

func TestGetAgentPlaceholderReturnsAgentID(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent_a", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET agent status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response JSON decode: %v", err)
	}
	if got["agent_id"] != "agent_a" {
		t.Fatalf("agent_id = %v, want agent_a", got["agent_id"])
	}
}

func createAgentTransfer(t *testing.T, handler http.Handler, fileName string) map[string]any {
	t.Helper()
	body := []byte(`{"from_agent_id":"agent_a","to_agent_id":"agent_b","target":"agent","file_name":"` + fileName + `","file_size_bytes":42}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", bytes.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("create transfer status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("create response JSON decode: %v", err)
	}
	return got
}

func deleteTransfer(t *testing.T, handler http.Handler, transferID string) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/transfers/"+transferID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("delete transfer status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
}
