package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/sessions"
	"github.com/kirillkuzin/postamat/internal/signaling"
)

func TestBackendClientCreatesTransfer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/api/v1/transfers" {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["from_agent_id"] != "agent-a" || payload["to_agent_id"] != "agent-b" || payload["target"] != sessions.TargetAgent {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		writeJSONForTest(w, http.StatusCreated, map[string]any{
			"transfer_id":     "tr_123",
			"agent_ticket":    "ticket_123",
			"status":          sessions.StatusCreated,
			"target":          sessions.TargetAgent,
			"transport":       sessions.TransportWebRTCP2P,
			"from_agent_id":   "agent-a",
			"to_agent_id":     "agent-b",
			"file_name":       "report.pdf",
			"file_size_bytes": 42,
			"expires_at":      time.Now().UTC().Format(time.RFC3339),
		})
	}))
	defer server.Close()

	client := NewBackendClient(server.URL, server.Client())
	created, err := client.CreateTransfer(context.Background(), CreateBackendTransferInput{FromAgentID: "agent-a", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateTransfer returned error: %v", err)
	}
	if created.TransferID != "tr_123" || created.AgentTicket != "ticket_123" {
		t.Fatalf("unexpected created transfer: %+v", created)
	}
}

func TestBackendClientBuildsAgentWebSocketURL(t *testing.T) {
	client := NewBackendClient("https://postamat.example", nil)
	url, err := client.AgentWebSocketURL("tr_123", "ticket 123")
	if err != nil {
		t.Fatalf("AgentWebSocketURL returned error: %v", err)
	}
	if url != "wss://postamat.example/api/v1/agent/ws?ticket=ticket+123&transfer_id=tr_123" {
		t.Fatalf("url = %q", url)
	}
}

func TestBackendLoopCreatesBackendTransferAndAttachesJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSONForTest(w, http.StatusCreated, map[string]any{"transfer_id": "tr_created", "agent_ticket": "ticket_created"})
	}))
	defer server.Close()

	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient(server.URL, server.Client())})

	job, err := loop.CreateSendTransfer(context.Background(), CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateSendTransfer returned error: %v", err)
	}
	if job.TransferID != "tr_created" || job.AgentTicket != "ticket_created" || job.Status != JobStatusOffered {
		t.Fatalf("backend data not attached/offered: %+v", job)
	}
}

func TestBackendLoopRequiresBackendClient(t *testing.T) {
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: NewJobManager(nil)})
	_, err := loop.CreateSendTransfer(context.Background(), CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if !errors.Is(err, ErrBackendClientRequired) {
		t.Fatalf("CreateSendTransfer error = %v, want ErrBackendClientRequired", err)
	}
	if _, err := loop.ConnectTransfer(context.Background(), "tr_1", "ticket_1"); !errors.Is(err, ErrBackendClientRequired) {
		t.Fatalf("ConnectTransfer error = %v, want ErrBackendClientRequired", err)
	}
}

func TestBackendLoopSendsHelloAndHeartbeatOverWebSocket(t *testing.T) {
	messages := make(chan signaling.Envelope, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			var envelope signaling.Envelope
			if err := conn.ReadJSON(&envelope); err != nil {
				t.Fatalf("read json: %v", err)
			}
			messages <- envelope
		}
	}))
	defer server.Close()

	client := NewBackendClient("http"+strings.TrimPrefix(server.URL, "http"), server.Client())
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: NewJobManager(nil), Client: client})
	conn, err := loop.ConnectTransfer(context.Background(), "tr_1", "ticket_1")
	if err != nil {
		t.Fatalf("ConnectTransfer returned error: %v", err)
	}
	defer conn.Close()
	if err := loop.SendHeartbeat(conn); err != nil {
		t.Fatalf("SendHeartbeat returned error: %v", err)
	}

	first := <-messages
	second := <-messages
	if first.Type != signaling.MessageAgentHello || first.AgentID != "agent-a" || first.DeviceID != "dev-1" {
		t.Fatalf("unexpected hello: %+v", first)
	}
	if second.Type != signaling.MessagePing || second.AgentID != "agent-a" || second.DeviceID != "dev-1" {
		t.Fatalf("unexpected heartbeat: %+v", second)
	}
}

func TestBackendLoopHandlesIncomingOffer(t *testing.T) {
	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	inbox := NewInbox(t.TempDir(), func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: inbox, Client: NewBackendClient("https://postamat.example", nil)})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_offer", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: json.RawMessage(`{"file_name":"payload.bin","file_size_bytes":99}`)}
	if err := loop.HandleEnvelope(context.Background(), envelope); err != nil {
		t.Fatalf("HandleEnvelope returned error: %v", err)
	}
	job, ok := manager.FindByTransferID("tr_offer")
	if !ok {
		t.Fatal("expected receive job for incoming offer")
	}
	if job.Direction != JobDirectionReceive || job.FromAgentID != "agent-a" || job.FileName != "payload.bin" || job.FileSizeBytes != 99 || job.DestinationPath == "" {
		t.Fatalf("unexpected receive job: %+v", job)
	}
	entries := inbox.List()
	if len(entries) != 1 || entries[0].TransferID != "tr_offer" {
		t.Fatalf("expected inbox reservation for offer: %+v", entries)
	}
}

func TestBackendLoopRejectsMisroutedOffer(t *testing.T) {
	manager := NewJobManager(nil)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient("https://postamat.example", nil)})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_wrong", FromAgentID: "agent-a", ToAgentID: "agent-c", Payload: json.RawMessage(`{"file_name":"payload.bin","file_size_bytes":99}`)}
	if err := loop.HandleEnvelope(context.Background(), envelope); !errors.Is(err, ErrOfferRecipientMismatch) {
		t.Fatalf("HandleEnvelope error = %v, want ErrOfferRecipientMismatch", err)
	}
	if _, ok := manager.FindByTransferID("tr_wrong"); ok {
		t.Fatal("misrouted offer created a receive job")
	}
}

func TestBackendLoopRejectsEmptyTransferIDForStateMessages(t *testing.T) {
	manager := NewJobManager(nil)
	queued, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient("https://postamat.example", nil)})
	for _, messageType := range []signaling.MessageType{signaling.MessageTransferAccepted, signaling.MessageTransferProgress, signaling.MessageTransferCompleted, signaling.MessageTransferFailed, signaling.MessageTransferDenied, signaling.MessageTransferCancelled, signaling.MessageTransferExpired} {
		t.Run(string(messageType), func(t *testing.T) {
			err := loop.HandleEnvelope(context.Background(), signaling.Envelope{Type: messageType})
			if !errors.Is(err, ErrTransferIDRequired) {
				t.Fatalf("HandleEnvelope error = %v, want ErrTransferIDRequired", err)
			}
		})
	}
	after, err := manager.Get(queued.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if after.Status != JobStatusQueued || after.ProgressBytes != 0 {
		t.Fatalf("malformed state message mutated queued job: %+v", after)
	}
}

func TestBackendLoopRejectsPrematureCompletion(t *testing.T) {
	manager := NewJobManager(nil)
	job, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "payload.bin", FileSizeBytes: 10})
	if err != nil {
		t.Fatalf("CreateReceiveJob returned error: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient("https://postamat.example", nil)})
	if err := loop.HandleEnvelope(context.Background(), signaling.Envelope{Type: signaling.MessageTransferCompleted, TransferID: "tr_1"}); !errors.Is(err, ErrInvalidJobStatus) {
		t.Fatalf("premature completed error = %v, want ErrInvalidJobStatus", err)
	}
	after, err := manager.Get(job.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if after.Status != JobStatusOffered || after.CompletedAt != nil {
		t.Fatalf("premature completed message mutated job: %+v", after)
	}
}

func TestBackendLoopHandlesRemoteTerminalMessages(t *testing.T) {
	tests := []struct {
		name       string
		message    signaling.MessageType
		wantStatus JobStatus
	}{
		{name: "failed", message: signaling.MessageTransferFailed, wantStatus: JobStatusFailed},
		{name: "denied", message: signaling.MessageTransferDenied, wantStatus: JobStatusFailed},
		{name: "expired", message: signaling.MessageTransferExpired, wantStatus: JobStatusFailed},
		{name: "cancelled", message: signaling.MessageTransferCancelled, wantStatus: JobStatusCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewJobManager(nil)
			job, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr_" + tt.name, FromAgentID: "agent-a", FileName: "payload.bin", FileSizeBytes: 10})
			if err != nil {
				t.Fatalf("CreateReceiveJob returned error: %v", err)
			}
			loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient("https://postamat.example", nil)})
			if err := loop.HandleEnvelope(context.Background(), signaling.Envelope{Type: tt.message, TransferID: job.TransferID}); err != nil {
				t.Fatalf("HandleEnvelope returned error: %v", err)
			}
			after, err := manager.Get(job.ID)
			if err != nil {
				t.Fatalf("Get returned error: %v", err)
			}
			if after.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", after.Status, tt.wantStatus)
			}
		})
	}
}

func TestBackendLoopIgnoresDuplicateOfferForSameTransfer(t *testing.T) {
	manager := NewJobManager(nil)
	inbox := NewInbox(t.TempDir(), nil)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: inbox, Client: NewBackendClient("https://postamat.example", nil)})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_dup", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: json.RawMessage(`{"file_name":"payload.bin","file_size_bytes":99}`)}
	if err := loop.HandleEnvelope(context.Background(), envelope); err != nil {
		t.Fatalf("first HandleEnvelope returned error: %v", err)
	}
	first, ok := manager.FindByTransferID("tr_dup")
	if !ok {
		t.Fatal("expected receive job")
	}
	if err := loop.HandleEnvelope(context.Background(), envelope); err != nil {
		t.Fatalf("duplicate HandleEnvelope returned error: %v", err)
	}
	jobs := manager.List()
	if len(jobs) != 1 || jobs[0].ID != first.ID {
		t.Fatalf("duplicate offer should be idempotent, jobs = %+v", jobs)
	}
	if entries := inbox.List(); len(entries) != 1 {
		t.Fatalf("duplicate offer should not reserve inbox twice: %+v", entries)
	}
}

func TestBackendLoopHandlesConcurrentDuplicateOffersIdempotently(t *testing.T) {
	manager := NewJobManager(nil)
	inbox := NewInbox(t.TempDir(), nil)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: inbox, Client: NewBackendClient("https://postamat.example", nil)})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_concurrent", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: json.RawMessage(`{"file_name":"payload.bin","file_size_bytes":99}`)}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- loop.HandleEnvelope(context.Background(), envelope)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("duplicate concurrent offer returned error: %v", err)
		}
	}
	if jobs := manager.List(); len(jobs) != 1 {
		t.Fatalf("expected one receive job, got %+v", jobs)
	}
	if entries := inbox.List(); len(entries) != 1 {
		t.Fatalf("expected one inbox reservation, got %+v", entries)
	}
}

func writeJSONForTest(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
