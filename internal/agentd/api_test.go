package agentd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/signaling"
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

func TestLocalAPICreateDelegatesToBackendLoopWhenConfigured(t *testing.T) {
	var backendPayload map[string]any
	messages := make(chan signaling.Envelope, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/transfers":
			if req.Method != http.MethodPost {
				t.Fatalf("unexpected backend method %s for %s", req.Method, req.URL.Path)
			}
			if err := json.NewDecoder(req.Body).Decode(&backendPayload); err != nil {
				t.Fatalf("decode backend request: %v", err)
			}
			writeJSONForTest(w, http.StatusCreated, map[string]any{"transfer_id": "tr_backend", "agent_ticket": "ticket_backend"})
		case "/api/v1/agent/ws":
			conn, err := upgrader.Upgrade(w, req, nil)
			if err != nil {
				t.Fatalf("upgrade backend ws: %v", err)
			}
			defer conn.Close()
			for i := 0; i < 2; i++ {
				var envelope signaling.Envelope
				if err := conn.ReadJSON(&envelope); err != nil {
					t.Fatalf("read backend signaling message %d: %v", i, err)
				}
				messages <- envelope
			}
		default:
			t.Fatalf("unexpected backend request %s %s", req.Method, req.URL.Path)
		}
	}))
	defer backend.Close()

	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient(backend.URL, backend.Client())})
	handler := NewLocalRouterWithBackend(manager, loop)
	createBody := []byte(`{"source_path":"/tmp/report.pdf","to_agent_id":"agent-b","file_name":"report.pdf","file_size_bytes":42}`)

	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/local/v1/transfers", bytes.NewReader(createBody)))

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created jobResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.TransferID != "tr_backend" || created.Status != string(JobStatusOffered) {
		t.Fatalf("backend transfer not attached/offered: %+v", created)
	}
	if backendPayload["from_agent_id"] != "agent-a" || backendPayload["to_agent_id"] != "agent-b" || backendPayload["file_name"] != "report.pdf" || backendPayload["file_size_bytes"] != float64(42) {
		t.Fatalf("unexpected backend payload: %+v", backendPayload)
	}
	gotHello := readSignalingMessage(t, messages, "hello")
	if gotHello.Type != signaling.MessageAgentHello || gotHello.AgentID != "agent-a" || gotHello.DeviceID != "dev-1" || gotHello.TransferID != "tr_backend" {
		t.Fatalf("unexpected backend hello: %+v", gotHello)
	}
	gotOffer := readSignalingMessage(t, messages, "offer")
	if gotOffer.Type != signaling.MessageTransferOffer || gotOffer.TransferID != "tr_backend" || gotOffer.FromAgentID != "agent-a" || gotOffer.ToAgentID != "agent-b" {
		t.Fatalf("unexpected backend offer: %+v", gotOffer)
	}
}

func TestLocalAPICancelPropagatesToBackendWhenConfigured(t *testing.T) {
	cancelled := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete && req.URL.Path == "/api/v1/transfers/tr_cancel" {
			cancelled <- "tr_cancel"
			writeJSONForTest(w, http.StatusOK, map[string]any{"transfer_id": "tr_cancel", "status": "cancelled"})
			return
		}
		t.Fatalf("unexpected backend request %s %s", req.Method, req.URL.Path)
	}))
	defer backend.Close()

	manager := NewJobManager(nil)
	job, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	if _, err := manager.AttachTransfer(job.ID, "tr_cancel", "ticket"); err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient(backend.URL, backend.Client())})
	handler := NewLocalRouterWithBackend(manager, loop)

	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, httptest.NewRequest(http.MethodPost, "/local/v1/transfers/tr_cancel/cancel", nil))
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", cancelRec.Code, cancelRec.Body.String())
	}
	select {
	case got := <-cancelled:
		if got != "tr_cancel" {
			t.Fatalf("backend cancelled %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backend cancel was not called")
	}
}

func TestLocalAPICancelDoesNotCallBackendForTerminalJob(t *testing.T) {
	called := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		t.Fatalf("backend should not be called for terminal local job: %s %s", req.Method, req.URL.Path)
	}))
	defer backend.Close()

	manager := NewJobManager(nil)
	job, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	if _, err := manager.AttachTransfer(job.ID, "tr_terminal", "ticket"); err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	if _, err := manager.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient(backend.URL, backend.Client())})
	handler := NewLocalRouterWithBackend(manager, loop)

	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, httptest.NewRequest(http.MethodPost, "/local/v1/transfers/tr_terminal/cancel", nil))
	if cancelRec.Code != http.StatusBadRequest {
		t.Fatalf("terminal cancel status = %d, body = %s", cancelRec.Code, cancelRec.Body.String())
	}
	if called {
		t.Fatal("backend cancel called for terminal local job")
	}
}

func readSignalingMessage(t *testing.T, messages <-chan signaling.Envelope, label string) signaling.Envelope {
	t.Helper()
	select {
	case got := <-messages:
		return got
	case <-time.After(2 * time.Second):
		t.Fatalf("backend signaling loop did not receive %s", label)
		return signaling.Envelope{}
	}
}

func TestLocalAPIResolvesTransferIDForStatusCancelAndEvents(t *testing.T) {
	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	job, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	if _, err := manager.AttachTransfer(job.ID, "tr_backend", "ticket"); err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	handler := NewLocalRouter(manager)

	getReq := httptest.NewRequest(http.MethodGet, "/local/v1/transfers/tr_backend", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get by transfer id status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	var status jobResponse
	if err := json.NewDecoder(getRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode get by transfer id: %v", err)
	}
	if status.ID != job.ID || status.TransferID != "tr_backend" {
		t.Fatalf("unexpected transfer-id status response: %+v", status)
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "/local/v1/transfers/tr_backend/events", nil)
	eventsRec := httptest.NewRecorder()
	handler.ServeHTTP(eventsRec, eventsReq)
	if eventsRec.Code != http.StatusOK {
		t.Fatalf("events by transfer id status = %d, body = %s", eventsRec.Code, eventsRec.Body.String())
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/local/v1/transfers/tr_backend/cancel", nil)
	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel by transfer id status = %d, body = %s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelled jobResponse
	if err := json.NewDecoder(cancelRec.Body).Decode(&cancelled); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if cancelled.Status != string(JobStatusCancelled) {
		t.Fatalf("cancelled status = %q", cancelled.Status)
	}
}

func TestLocalAPIEscapedTransferIDsDoNotChangeRouteShape(t *testing.T) {
	manager := NewJobManager(nil)
	job, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	if _, err := manager.AttachTransfer(job.ID, "tr/with/slash", "ticket"); err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	handler := NewLocalRouter(manager)

	getReq := httptest.NewRequest(http.MethodGet, "/local/v1/transfers/tr%2Fwith%2Fslash", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("escaped transfer id status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	injectedReq := httptest.NewRequest(http.MethodGet, "/local/v1/transfers/"+job.ID+"%2Fevents", nil)
	injectedRec := httptest.NewRecorder()
	handler.ServeHTTP(injectedRec, injectedReq)
	if injectedRec.Code != http.StatusNotFound {
		t.Fatalf("escaped slash should remain inside id and not route to events endpoint; status = %d, body = %s", injectedRec.Code, injectedRec.Body.String())
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
