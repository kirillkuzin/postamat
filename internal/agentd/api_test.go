package agentd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalAPITransferLifecycle(t *testing.T) {
	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	handler := NewLocalRouter(manager)

	createBody := []byte(`{"source_path":"/tmp/report.pdf","to_agent_id":"agent-b","file_name":"report.pdf","file_size_bytes":42}`)
	createReq := httptest.NewRequest(http.MethodPost, "/local/v1/transfers", bytes.NewReader(createBody))
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created jobResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Status != string(JobStatusQueued) || created.Direction != string(JobDirectionSend) {
		t.Fatalf("unexpected create response: %+v", created)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/local/v1/transfers/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/local/v1/transfers/"+created.ID+"/cancel", nil)
	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelled jobResponse
	if err := json.NewDecoder(cancelRec.Body).Decode(&cancelled); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if cancelled.Status != string(JobStatusCancelled) {
		t.Fatalf("cancelled status = %q, want %q", cancelled.Status, JobStatusCancelled)
	}
}

func TestLocalAPIEventsAndInbox(t *testing.T) {
	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	receive, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr_in", FromAgentID: "agent-a", FileName: "payload.bin", FileSizeBytes: 12})
	if err != nil {
		t.Fatalf("CreateReceiveJob returned error: %v", err)
	}
	if _, err := manager.MarkAccepted(receive.ID); err != nil {
		t.Fatalf("MarkAccepted returned error: %v", err)
	}
	handler := NewLocalRouter(manager)

	eventsReq := httptest.NewRequest(http.MethodGet, "/local/v1/transfers/"+receive.ID+"/events", nil)
	eventsRec := httptest.NewRecorder()
	handler.ServeHTTP(eventsRec, eventsReq)
	if eventsRec.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", eventsRec.Code, eventsRec.Body.String())
	}
	var eventsResponse struct {
		Events []JobEvent `json:"events"`
	}
	if err := json.NewDecoder(eventsRec.Body).Decode(&eventsResponse); err != nil {
		t.Fatalf("decode events response: %v", err)
	}
	if len(eventsResponse.Events) < 2 || eventsResponse.Events[0].Type != JobEventCreated || eventsResponse.Events[1].Type != JobEventStatusChanged {
		t.Fatalf("unexpected events: %+v", eventsResponse.Events)
	}

	inboxReq := httptest.NewRequest(http.MethodGet, "/local/v1/inbox", nil)
	inboxRec := httptest.NewRecorder()
	handler.ServeHTTP(inboxRec, inboxReq)
	if inboxRec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d, body = %s", inboxRec.Code, inboxRec.Body.String())
	}
	var inbox struct {
		Jobs []jobResponse `json:"jobs"`
	}
	if err := json.NewDecoder(inboxRec.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox response: %v", err)
	}
	if len(inbox.Jobs) != 1 || inbox.Jobs[0].ID != receive.ID {
		t.Fatalf("unexpected inbox: %+v", inbox.Jobs)
	}
}

func TestLocalAPIRejectsInvalidRequests(t *testing.T) {
	handler := NewLocalRouter(NewJobManager(nil))

	badJSON := httptest.NewRecorder()
	handler.ServeHTTP(badJSON, httptest.NewRequest(http.MethodPost, "/local/v1/transfers", bytes.NewReader([]byte(`{"source_path":123}`))))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d", badJSON.Code)
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/local/v1/transfers/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.Code)
	}

	malformed := httptest.NewRecorder()
	handler.ServeHTTP(malformed, httptest.NewRequest(http.MethodGet, "/local/v1/transfers/a/b", nil))
	if malformed.Code != http.StatusNotFound {
		t.Fatalf("malformed status = %d", malformed.Code)
	}
}
