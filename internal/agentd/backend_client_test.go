package agentd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/api"
	"github.com/kirillkuzin/postamat/internal/p2p"
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

func TestBackendLoopLiveTwoAgentWebRTCRuntimeTransfersInboxFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	payload := []byte("live backend-routed encrypted webrtc payload")
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "payload.txt")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	service := sessions.NewService(sessions.NewMemoryRepository(), sessions.RandomTokenIssuer{}, nil)
	authenticator, err := api.NewStaticAgentTokenAuthenticator(map[string]string{"agent-b": "token-b"}, "test-pepper")
	if err != nil {
		t.Fatalf("NewStaticAgentTokenAuthenticator: %v", err)
	}
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(service, signaling.NewPresenceRegistry(nil), nil, authenticator))
	defer server.Close()

	recipientPrivate, recipientPublic, err := p2p.GenerateAgentEnvelopeKeyPair()
	if err != nil {
		t.Fatalf("GenerateAgentEnvelopeKeyPair: %v", err)
	}
	transferKey, err := p2p.NewRandomTransferKey()
	if err != nil {
		t.Fatalf("NewRandomTransferKey: %v", err)
	}
	sendJobs := NewJobManager(nil)
	receiveJobs := NewJobManager(nil)
	inbox := NewInbox(t.TempDir(), nil)
	client := NewBackendClient(server.URL, server.Client())
	senderLoop := NewBackendLoop(BackendLoopOptions{
		AgentID:             "agent-a",
		DeviceID:            "dev-a",
		Jobs:                sendJobs,
		Client:              client,
		RecipientPublicKeys: map[string][]byte{"agent-b": recipientPublic},
		GenerateTransferKey: func() (p2p.TransferKey, error) { return transferKey, nil },
	})
	receiverLoop := NewBackendLoop(BackendLoopOptions{
		AgentID:         "agent-b",
		DeviceID:        "dev-b",
		Jobs:            receiveJobs,
		Inbox:           inbox,
		Client:          client,
		AgentAuthToken:  "token-b",
		AgentPrivateKey: recipientPrivate,
	})

	receiverDone := make(chan error, 1)
	go func() { receiverDone <- receiverLoop.RunReceiver(ctx) }()
	waitForAgentOnlineForTest(t, server.URL, "agent-b")

	sendJob, err := senderLoop.CreateSendTransfer(ctx, CreateSendJobInput{SourcePath: sourcePath, ToAgentID: "agent-b", FileName: "payload.txt", FileSizeBytes: int64(len(payload))})
	if err != nil {
		t.Fatalf("CreateSendTransfer: %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- senderLoop.RunTransfer(ctx, sendJob) }()

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("RunTransfer: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for sender transfer: %v", ctx.Err())
	}
	cancel()
	select {
	case err := <-receiverDone:
		if err != nil {
			t.Fatalf("RunReceiver returned error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunReceiver did not stop after cancellation")
	}

	entries := inbox.List()
	if len(entries) != 1 {
		t.Fatalf("inbox entries = %d, want 1: %+v", len(entries), entries)
	}
	written, err := os.ReadFile(entries[0].DestinationPath)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(written) != string(payload) {
		t.Fatalf("destination payload = %q", string(written))
	}
	if _, err := os.Stat(entries[0].DestinationPath + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file should be removed, stat err=%v", err)
	}
	finalSend, ok := sendJobs.FindByTransferID(sendJob.TransferID)
	if !ok {
		t.Fatal("missing final send job")
	}
	finalReceive, ok := receiveJobs.FindByTransferID(sendJob.TransferID)
	if !ok {
		t.Fatal("missing final receive job")
	}
	if finalSend.Status != JobStatusCompleted || finalSend.ProgressBytes != int64(len(payload)) {
		t.Fatalf("send job not completed with full progress: %+v", finalSend)
	}
	if finalReceive.Status != JobStatusCompleted || finalReceive.ProgressBytes != int64(len(payload)) {
		t.Fatalf("receive job not completed with full progress: %+v", finalReceive)
	}
}

func TestLiveSenderResumeManifestUsesRetryableProgress(t *testing.T) {
	payload := []byte("chunk-0000|chunk-0001|chunk-0002")
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer source.Close()
	jobs := NewJobManager(nil)
	job := retryableSendJobForTest(t, jobs, sourcePath, "tr_live_resume", int64(len(payload)), 11)

	resume, err := liveSenderResumeManifest(source, job, 11)
	if err != nil {
		t.Fatalf("liveSenderResumeManifest: %v", err)
	}
	if resume == nil {
		t.Fatal("expected resume manifest for retryable sender progress")
	}
	if resume.TransferID != "tr_live_resume" || resume.NextOffset != 11 || resume.NextSequence != 1 || resume.TotalBytes != int64(len(payload)) {
		t.Fatalf("resume = %+v", *resume)
	}
	wantDigest := sha256HexForTest(payload[:11])
	if resume.SHA256Hex != wantDigest {
		t.Fatalf("resume digest = %s, want %s", resume.SHA256Hex, wantDigest)
	}
	pos, err := source.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("source seek current: %v", err)
	}
	if pos != 0 {
		t.Fatalf("source position = %d, want reset to 0 before StreamReader", pos)
	}
}

func TestLiveSenderResumeManifestReturnsNilWithoutRetryableProgress(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer source.Close()
	jobs := NewJobManager(nil)
	job := acceptedSendJobForTest(t, jobs, sourcePath, "tr_no_resume", 7)
	resume, err := liveSenderResumeManifest(source, job, 64*1024)
	if err != nil {
		t.Fatalf("liveSenderResumeManifest: %v", err)
	}
	if resume != nil {
		t.Fatalf("resume = %+v, want nil", *resume)
	}
}

func TestBackendLoopMarkTransferStartedRestartsRetryableJob(t *testing.T) {
	jobs := NewJobManager(nil)
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	job := retryableSendJobForTest(t, jobs, sourcePath, "tr_restart", 7, 3)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-a", Jobs: jobs})
	if err := loop.markTransferStarted("tr_restart"); err != nil {
		t.Fatalf("markTransferStarted retryable: %v", err)
	}
	final, ok := jobs.FindByTransferID(job.TransferID)
	if !ok {
		t.Fatal("missing job")
	}
	if final.Status != JobStatusTransferring {
		t.Fatalf("status = %s, want transferring", final.Status)
	}
	if final.ProgressBytes != 3 {
		t.Fatalf("progress = %d, want preserved retry offset 3", final.ProgressBytes)
	}
}

func TestRetryableRuntimeErrorIncludesPreOpenDataChannelClose(t *testing.T) {
	err := fmt.Errorf("%w: data channel closed before open", p2p.ErrTransferFailed)
	if !isRetryableRuntimeError(err) {
		t.Fatalf("expected pre-open data channel close to be retryable: %v", err)
	}
}

func TestBackendLoopRunTransferReturnsAfterRemoteRetryable(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		var hello signaling.Envelope
		if err := conn.ReadJSON(&hello); err != nil {
			t.Fatalf("read hello: %v", err)
		}
		var offer signaling.Envelope
		if err := conn.ReadJSON(&offer); err != nil {
			t.Fatalf("read offer: %v", err)
		}
		if offer.Type != signaling.MessageTransferOffer {
			t.Fatalf("unexpected offer: %+v", offer)
		}
		payload, err := json.Marshal(map[string]int64{"progress_bytes": 0})
		if err != nil {
			t.Fatalf("marshal retryable payload: %v", err)
		}
		if err := conn.WriteJSON(signaling.Envelope{Type: signaling.MessageTransferRetryable, TransferID: offer.TransferID, FromAgentID: "agent-b", ToAgentID: "agent-a", Payload: payload}); err != nil {
			t.Fatalf("write retryable: %v", err)
		}
		<-req.Context().Done()
	}))
	defer server.Close()

	jobs := NewJobManager(nil)
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	job := acceptedSendJobForTest(t, jobs, sourcePath, "tr_run_retryable", 7)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-a", Jobs: jobs, Client: NewBackendClient(server.URL, server.Client())})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := loop.RunTransfer(ctx, job)
	if !errors.Is(err, p2p.ErrTransferFailed) {
		t.Fatalf("RunTransfer error = %v, want ErrTransferFailed", err)
	}
	final, ok := jobs.FindByTransferID(job.TransferID)
	if !ok {
		t.Fatal("missing job")
	}
	if final.Status != JobStatusRetryable {
		t.Fatalf("job status = %s, want retryable", final.Status)
	}
}

func TestBackendLoopMarksAcceptedLiveTransferRetryableFromRemoteState(t *testing.T) {
	jobs := NewJobManager(nil)
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	job := acceptedSendJobForTest(t, jobs, sourcePath, "tr_accepted_retry", 7)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-a", Jobs: jobs})

	envelope := signaling.Envelope{Type: signaling.MessageTransferRetryable, TransferID: job.TransferID, FromAgentID: "agent-b", ToAgentID: "agent-a"}
	if err := loop.HandleEnvelope(context.Background(), envelope); err != nil {
		t.Fatalf("HandleEnvelope retryable: %v", err)
	}
	final, ok := jobs.FindByTransferID(job.TransferID)
	if !ok {
		t.Fatal("missing job")
	}
	if final.Status != JobStatusRetryable || final.ProgressBytes != 0 {
		t.Fatalf("job = %+v, want retryable at zero progress", final)
	}
}

func TestBackendLoopUsesRemoteRetryableProgressForSenderResumeOffset(t *testing.T) {
	jobs := NewJobManager(nil)
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	job := acceptedSendJobForTest(t, jobs, sourcePath, "tr_progress_retry", 10)
	job, err := jobs.MarkConnecting(job.ID)
	if err != nil {
		t.Fatalf("MarkConnecting: %v", err)
	}
	job, err = jobs.MarkTransferring(job.ID)
	if err != nil {
		t.Fatalf("MarkTransferring: %v", err)
	}
	if _, err := jobs.UpdateProgress(job.ID, 9); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	payload, err := json.Marshal(map[string]int64{"progress_bytes": 4})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-a", Jobs: jobs})

	envelope := signaling.Envelope{Type: signaling.MessageTransferRetryable, TransferID: job.TransferID, FromAgentID: "agent-b", ToAgentID: "agent-a", Payload: payload}
	if err := loop.HandleEnvelope(context.Background(), envelope); err != nil {
		t.Fatalf("HandleEnvelope retryable: %v", err)
	}
	final, ok := jobs.FindByTransferID(job.TransferID)
	if !ok {
		t.Fatal("missing job")
	}
	if final.Status != JobStatusRetryable || final.ProgressBytes != 4 {
		t.Fatalf("job = %+v, want retryable at receiver durable offset 4", final)
	}
}

func TestLiveReceiverPartialPreparationDiscardsStalePartialForFreshAcceptedJob(t *testing.T) {
	destinationPath := filepath.Join(t.TempDir(), "payload.txt")
	partialPath := destinationPath + ".part"
	if err := os.WriteFile(partialPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale partial: %v", err)
	}
	jobs := NewJobManager(nil)
	job := acceptedReceiveJobForTest(t, jobs, destinationPath, "tr_stale", 10)

	_, resumed, _, err := prepareLiveReceiverPartial(partialPath, job)
	if err != nil {
		t.Fatalf("prepareLiveReceiverPartial: %v", err)
	}
	if resumed {
		t.Fatal("fresh accepted job must not resume from stale .part")
	}
	if _, err := os.Lstat(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale partial should be discarded before fresh receive, lstat err=%v", err)
	}
}

func TestBackendLoopMarksLiveTransferRetryableFromRemoteState(t *testing.T) {
	jobs := NewJobManager(nil)
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	job := retryableSendJobForTest(t, jobs, sourcePath, "tr_remote_retry", 7, 3)
	// Put the job back into an active state to prove the incoming remote retryable
	// signal, not the fixture, performs the transition under test.
	job, err := jobs.MarkConnecting(job.ID)
	if err != nil {
		t.Fatalf("MarkConnecting: %v", err)
	}
	job, err = jobs.MarkTransferring(job.ID)
	if err != nil {
		t.Fatalf("MarkTransferring: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-a", Jobs: jobs})

	envelope := signaling.Envelope{Type: signaling.MessageTransferRetryable, TransferID: "tr_remote_retry", FromAgentID: "agent-b", ToAgentID: "agent-a"}
	if err := loop.HandleEnvelope(context.Background(), envelope); err != nil {
		t.Fatalf("HandleEnvelope retryable: %v", err)
	}
	final, ok := jobs.FindByTransferID("tr_remote_retry")
	if !ok {
		t.Fatal("missing job")
	}
	if final.Status != JobStatusRetryable {
		t.Fatalf("job status = %s, want retryable", final.Status)
	}
}

func TestBackendLoopMarksLiveTransferInterruptedFromRemoteState(t *testing.T) {
	jobs := NewJobManager(nil)
	destinationPath := filepath.Join(t.TempDir(), "payload.txt")
	job := acceptedReceiveJobForTest(t, jobs, destinationPath, "tr_remote_interrupt", 7)
	job, err := jobs.MarkConnecting(job.ID)
	if err != nil {
		t.Fatalf("MarkConnecting: %v", err)
	}
	job, err = jobs.MarkTransferring(job.ID)
	if err != nil {
		t.Fatalf("MarkTransferring: %v", err)
	}
	if _, err := jobs.UpdateProgress(job.ID, 3); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-b", Jobs: jobs})

	envelope := signaling.Envelope{Type: signaling.MessageTransferInterrupted, TransferID: "tr_remote_interrupt", FromAgentID: "agent-a", ToAgentID: "agent-b"}
	if err := loop.HandleEnvelope(context.Background(), envelope); err != nil {
		t.Fatalf("HandleEnvelope interrupted: %v", err)
	}
	final, ok := jobs.FindByTransferID("tr_remote_interrupt")
	if !ok {
		t.Fatal("missing job")
	}
	if final.Status != JobStatusRetryable {
		t.Fatalf("job status = %s, want retryable", final.Status)
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
	payload, _, privateKey := keyedAgentOfferPayloadForTest(t, "agent-b", "payload.bin", 99)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: inbox, Client: NewBackendClient("https://postamat.example", nil), AgentPrivateKey: privateKey})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_offer", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: payload}
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
	if err := loop.HandleEnvelope(context.Background(), signaling.Envelope{Type: signaling.MessageTransferCompleted, TransferID: "tr_1"}); !errors.Is(err, signaling.ErrRouteTargetRequired) {
		t.Fatalf("premature completed error = %v, want ErrRouteTargetRequired", err)
	}
	after, err := manager.Get(job.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if after.Status != JobStatusOffered || after.CompletedAt != nil {
		t.Fatalf("premature completed message mutated job: %+v", after)
	}
}

func TestBackendLoopCompletesSendJobWhenRemoteCompletionArrivesBeforeStarted(t *testing.T) {
	manager := NewJobManager(nil)
	job, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/payload.bin", ToAgentID: "agent-b", FileName: "payload.bin", FileSizeBytes: 10})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	job, err = manager.AttachTransfer(job.ID, "tr_1", "ticket_1")
	if err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	if _, err := manager.MarkOffered(job.ID); err != nil {
		t.Fatalf("MarkOffered returned error: %v", err)
	}
	if _, err := manager.MarkAccepted(job.ID); err != nil {
		t.Fatalf("MarkAccepted returned error: %v", err)
	}
	if _, err := manager.UpdateProgress(job.ID, 10); err != nil {
		t.Fatalf("UpdateProgress returned error: %v", err)
	}
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-a", DeviceID: "dev-1", Jobs: manager, Client: NewBackendClient("https://postamat.example", nil)})

	if err := loop.HandleEnvelope(context.Background(), signaling.Envelope{Type: signaling.MessageTransferCompleted, TransferID: "tr_1", FromAgentID: "agent-b", ToAgentID: "agent-a"}); err != nil {
		t.Fatalf("HandleEnvelope returned error: %v", err)
	}
	final, err := manager.Get(job.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if final.Status != JobStatusCompleted || final.CompletedAt == nil {
		t.Fatalf("send job not completed after out-of-order remote completion: %+v", final)
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
	payload, _, privateKey := keyedAgentOfferPayloadForTest(t, "agent-b", "payload.bin", 99)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: inbox, Client: NewBackendClient("https://postamat.example", nil), AgentPrivateKey: privateKey})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_dup", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: payload}
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
	payload, _, privateKey := keyedAgentOfferPayloadForTest(t, "agent-b", "payload.bin", 99)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: inbox, Client: NewBackendClient("https://postamat.example", nil), AgentPrivateKey: privateKey})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_concurrent", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: payload}

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

func TestBackendLoopDeniesOfferByReceivePolicyBeforeInboxReservation(t *testing.T) {
	manager := NewJobManager(nil)
	inbox := NewInbox(t.TempDir(), nil)
	loop := NewBackendLoop(BackendLoopOptions{
		AgentID:       "agent-b",
		DeviceID:      "dev-1",
		Jobs:          manager,
		Inbox:         inbox,
		Client:        NewBackendClient("https://postamat.example", nil),
		ReceivePolicy: ReceivePolicy{AllowedFromAgentIDs: []string{"trusted-agent"}, MaxFileSizeBytes: 10},
	})
	envelope := signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_denied", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: json.RawMessage(`{"file_name":"payload.bin","file_size_bytes":99}`)}

	if err := loop.HandleEnvelope(context.Background(), envelope); !errors.Is(err, ErrOfferDeniedByPolicy) {
		t.Fatalf("HandleEnvelope error = %v, want ErrOfferDeniedByPolicy", err)
	}
	if _, ok := manager.FindByTransferID("tr_denied"); ok {
		t.Fatal("policy-denied offer created a receive job")
	}
	if entries := inbox.List(); len(entries) != 0 {
		t.Fatalf("policy-denied offer reserved inbox entries: %+v", entries)
	}
}

func TestBackendLoopOfferIncludesWrappedTransferKeyOnly(t *testing.T) {
	transferKey := mustAgentdTestTransferKey(t)
	_, recipientPublic, err := p2p.GenerateAgentEnvelopeKeyPair()
	if err != nil {
		t.Fatalf("generate recipient key pair: %v", err)
	}
	messages := make(chan signaling.Envelope, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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

	manager := NewJobManager(nil)
	loop := NewBackendLoop(BackendLoopOptions{
		AgentID:             "agent-a",
		DeviceID:            "dev-1",
		Jobs:                manager,
		Client:              NewBackendClient(server.URL, server.Client()),
		RecipientPublicKeys: map[string][]byte{"agent-b": recipientPublic},
		GenerateTransferKey: func() (p2p.TransferKey, error) { return transferKey, nil },
	})
	job, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/report.pdf", ToAgentID: "agent-b", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	job, err = manager.AttachTransfer(job.ID, "tr_key", "ticket-key")
	if err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	job, err = manager.AttachTransferKey(job.ID, transferKey)
	if err != nil {
		t.Fatalf("AttachTransferKey returned error: %v", err)
	}
	job, err = manager.MarkOffered(job.ID)
	if err != nil {
		t.Fatalf("MarkOffered returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- loop.RunTransfer(ctx, job) }()
	defer func() {
		cancel()
		select {
		case <-runErr:
		case <-time.After(2 * time.Second):
			t.Fatal("RunTransfer did not stop after cancellation")
		}
	}()

	readSignalingMessage(t, messages, "hello")
	offer := readSignalingMessage(t, messages, "offer")
	if offer.Type != signaling.MessageTransferOffer {
		t.Fatalf("expected transfer offer, got %+v", offer)
	}
	if len(offer.Payload) == 0 {
		t.Fatalf("offer payload missing key envelope")
	}
	assertJSONDoesNotExposeTransferKey(t, offer.Payload, transferKey)
	var payload transferOfferPayload
	if err := json.Unmarshal(offer.Payload, &payload); err != nil {
		t.Fatalf("decode offer payload: %v", err)
	}
	if payload.KeyEnvelope == nil {
		t.Fatalf("offer payload missing key envelope: %s", offer.Payload)
	}
}

func TestBackendLoopOpensOfferKeyBeforeInboxReservation(t *testing.T) {
	transferKey := mustAgentdTestTransferKey(t)
	recipientPrivate, recipientPublic, err := p2p.GenerateAgentEnvelopeKeyPair()
	if err != nil {
		t.Fatalf("generate recipient key pair: %v", err)
	}
	envelope, err := p2p.WrapTransferKeyForAgent("agent-b", recipientPublic, transferKey)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	payload, err := json.Marshal(transferOfferPayload{FileName: "payload.bin", FileSizeBytes: 99, KeyEnvelope: &envelope})
	if err != nil {
		t.Fatalf("marshal offer payload: %v", err)
	}

	manager := NewJobManager(nil)
	inbox := NewInbox(t.TempDir(), nil)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: inbox, Client: NewBackendClient("https://postamat.example", nil), AgentPrivateKey: recipientPrivate})
	if err := loop.HandleEnvelope(context.Background(), signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_key", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: payload}); err != nil {
		t.Fatalf("HandleEnvelope returned error: %v", err)
	}
	job, ok := manager.FindByTransferID("tr_key")
	if !ok {
		t.Fatal("expected receive job")
	}
	if !job.HasTransferKey || job.TransferKey != transferKey {
		t.Fatalf("receive job did not keep unwrapped transfer key")
	}

	deniedManager := NewJobManager(nil)
	deniedInbox := NewInbox(t.TempDir(), nil)
	deniedLoop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: deniedManager, Inbox: deniedInbox, Client: NewBackendClient("https://postamat.example", nil)})
	if err := deniedLoop.HandleEnvelope(context.Background(), signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_no_key", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: payload}); !errors.Is(err, ErrTransferKeyRequired) {
		t.Fatalf("HandleEnvelope without private key error = %v, want ErrTransferKeyRequired", err)
	}
	if _, ok := deniedManager.FindByTransferID("tr_no_key"); ok {
		t.Fatal("invalid key offer created a receive job")
	}
	if entries := deniedInbox.List(); len(entries) != 0 {
		t.Fatalf("invalid key offer reserved inbox entries: %+v", entries)
	}
}

func TestBackendLoopWritesAcceptedOrDeniedDecisionOverWebSocket(t *testing.T) {
	tests := []struct {
		name     string
		policy   ReceivePolicy
		fileSize int64
		wantType signaling.MessageType
		wantJob  bool
	}{
		{name: "accepted", policy: ReceivePolicy{AllowedFromAgentIDs: []string{"agent-a"}, MaxFileSizeBytes: 64}, fileSize: 42, wantType: signaling.MessageTransferAccepted, wantJob: true},
		{name: "denied", policy: ReceivePolicy{AllowedFromAgentIDs: []string{"agent-a"}, MaxFileSizeBytes: 10}, fileSize: 42, wantType: signaling.MessageTransferDenied, wantJob: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := make(chan signaling.Envelope, 3)
			offerPayload, _, privateKey := keyedAgentOfferPayloadForTest(t, "agent-b", "payload.bin", tt.fileSize)
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				conn, err := upgrader.Upgrade(w, req, nil)
				if err != nil {
					t.Fatalf("upgrade: %v", err)
				}
				defer conn.Close()
				var hello signaling.Envelope
				if err := conn.ReadJSON(&hello); err != nil {
					t.Fatalf("read hello: %v", err)
				}
				messages <- hello
				if err := conn.WriteJSON(signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_policy", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: offerPayload}); err != nil {
					t.Fatalf("write offer: %v", err)
				}
				var decision signaling.Envelope
				if err := conn.ReadJSON(&decision); err != nil {
					t.Fatalf("read decision: %v", err)
				}
				messages <- decision
			}))
			defer server.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager := NewJobManager(nil)
			loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-1", Jobs: manager, Inbox: NewInbox(t.TempDir(), nil), Client: NewBackendClient(server.URL, server.Client()), ReceivePolicy: tt.policy, AgentPrivateKey: privateKey})
			runErr := make(chan error, 1)
			go func() {
				runErr <- loop.RunTransfer(ctx, Job{Direction: JobDirectionReceive, TransferID: "tr_policy", AgentTicket: "ticket-b"})
			}()

			hello := readSignalingMessage(t, messages, "hello")
			if hello.Type != signaling.MessageAgentHello || hello.AgentID != "agent-b" || hello.TransferID != "tr_policy" {
				t.Fatalf("unexpected hello: %+v", hello)
			}
			decision := readSignalingMessage(t, messages, "decision")
			if decision.Type != tt.wantType || decision.TransferID != "tr_policy" || decision.FromAgentID != "agent-b" || decision.ToAgentID != "agent-a" {
				t.Fatalf("unexpected decision: %+v", decision)
			}
			_, ok := manager.FindByTransferID("tr_policy")
			if ok != tt.wantJob {
				t.Fatalf("receive job presence = %v, want %v", ok, tt.wantJob)
			}
			cancel()
			select {
			case err := <-runErr:
				if err != nil && ctx.Err() == nil {
					t.Fatalf("RunTransfer returned error: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("RunTransfer did not stop after context cancellation")
			}
		})
	}
}

func TestBackendClientBuildsAuthenticatedAgentPresenceWebSocketURL(t *testing.T) {
	client := NewBackendClient("https://postamat.example/base", nil)
	url, err := client.AgentPresenceWebSocketURL("agent b")
	if err != nil {
		t.Fatalf("AgentPresenceWebSocketURL returned error: %v", err)
	}
	if url != "wss://postamat.example/base/api/v1/agents/ws?agent_id=agent+b" {
		t.Fatalf("url = %q", url)
	}
}

func TestBackendLoopRunReceiverSendsHelloOnAlwaysOnSocket(t *testing.T) {
	messages := make(chan signaling.Envelope, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/agents/ws" || req.URL.Query().Get("agent_id") != "agent-b" {
			t.Fatalf("unexpected websocket request: %s", req.URL.String())
		}
		if req.Header.Get("Authorization") != "Bearer token-b" {
			t.Fatalf("missing bearer token: %q", req.Header.Get("Authorization"))
		}
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		var hello signaling.Envelope
		if err := conn.ReadJSON(&hello); err != nil {
			t.Fatalf("read hello: %v", err)
		}
		messages <- hello
		<-req.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-b", Client: NewBackendClient(server.URL, server.Client()), AgentAuthToken: "token-b"})
	runErr := make(chan error, 1)
	go func() { runErr <- loop.RunReceiver(ctx) }()

	hello := readSignalingMessage(t, messages, "hello")
	if hello.Type != signaling.MessageAgentHello || hello.AgentID != "agent-b" || hello.DeviceID != "dev-b" || hello.TransferID != "" {
		t.Fatalf("unexpected always-on hello: %+v", hello)
	}
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("RunReceiver returned error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunReceiver did not stop after context cancellation")
	}
}

func TestBackendLoopRunReceiverAcceptsOfferOverAlwaysOnSocket(t *testing.T) {
	messages := make(chan signaling.Envelope, 2)
	payload, _, privateKey := keyedAgentOfferPayloadForTest(t, "agent-b", "payload.bin", 99)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		var hello signaling.Envelope
		if err := conn.ReadJSON(&hello); err != nil {
			t.Fatalf("read hello: %v", err)
		}
		messages <- hello
		if err := conn.WriteJSON(signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_offer", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: payload}); err != nil {
			t.Fatalf("write offer: %v", err)
		}
		var decision signaling.Envelope
		if err := conn.ReadJSON(&decision); err != nil {
			t.Fatalf("read decision: %v", err)
		}
		messages <- decision
		<-req.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewJobManager(nil)
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-b", Jobs: manager, Inbox: NewInbox(t.TempDir(), nil), Client: NewBackendClient(server.URL, server.Client()), AgentAuthToken: "token-b", AgentPrivateKey: privateKey})
	runErr := make(chan error, 1)
	go func() { runErr <- loop.RunReceiver(ctx) }()

	readSignalingMessage(t, messages, "hello")
	decision := readSignalingMessage(t, messages, "decision")
	if decision.Type != signaling.MessageTransferAccepted || decision.TransferID != "tr_offer" || decision.FromAgentID != "agent-b" || decision.ToAgentID != "agent-a" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	job, ok := manager.FindByTransferID("tr_offer")
	if !ok || job.Direction != JobDirectionReceive || job.Status != JobStatusAccepted || job.DestinationPath == "" {
		t.Fatalf("expected accepted receive job with inbox destination, got %+v ok=%v", job, ok)
	}
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("RunReceiver returned error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunReceiver did not stop after context cancellation")
	}
}

func TestBackendLoopRunReceiverIgnoresBadStateMessageAndContinues(t *testing.T) {
	messages := make(chan signaling.Envelope, 2)
	payload, _, privateKey := keyedAgentOfferPayloadForTest(t, "agent-b", "payload.bin", 99)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		var hello signaling.Envelope
		if err := conn.ReadJSON(&hello); err != nil {
			t.Fatalf("read hello: %v", err)
		}
		messages <- hello
		if err := conn.WriteJSON(signaling.Envelope{Type: signaling.MessageTransferAccepted, TransferID: "tr_missing", FromAgentID: "agent-a", ToAgentID: "agent-b"}); err != nil {
			t.Fatalf("write bad accepted: %v", err)
		}
		if err := conn.WriteJSON(signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: "tr_offer", FromAgentID: "agent-a", ToAgentID: "agent-b", Payload: payload}); err != nil {
			t.Fatalf("write offer: %v", err)
		}
		var decision signaling.Envelope
		if err := conn.ReadJSON(&decision); err != nil {
			t.Fatalf("read decision: %v", err)
		}
		messages <- decision
		<-req.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-b", Jobs: NewJobManager(nil), Inbox: NewInbox(t.TempDir(), nil), Client: NewBackendClient(server.URL, server.Client()), AgentAuthToken: "token-b", AgentPrivateKey: privateKey})
	runErr := make(chan error, 1)
	go func() { runErr <- loop.RunReceiver(ctx) }()

	readSignalingMessage(t, messages, "hello")
	decision := readSignalingMessage(t, messages, "decision")
	if decision.Type != signaling.MessageTransferAccepted || decision.TransferID != "tr_offer" {
		t.Fatalf("receiver did not continue to accept later offer: %+v", decision)
	}
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("RunReceiver returned error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunReceiver did not stop after context cancellation")
	}
}

func TestBackendLoopRunReceiverReconnectsAfterDroppedAlwaysOnSocket(t *testing.T) {
	hellos := make(chan signaling.Envelope, 2)
	var mu sync.Mutex
	connections := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		var hello signaling.Envelope
		if err := conn.ReadJSON(&hello); err != nil {
			t.Fatalf("read hello: %v", err)
		}
		hellos <- hello
		mu.Lock()
		connections++
		current := connections
		mu.Unlock()
		if current == 1 {
			return
		}
		<-req.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := NewBackendLoop(BackendLoopOptions{AgentID: "agent-b", DeviceID: "dev-b", Client: NewBackendClient(server.URL, server.Client()), AgentAuthToken: "token-b"})
	runErr := make(chan error, 1)
	go func() { runErr <- loop.RunReceiver(ctx) }()

	first := readSignalingMessage(t, hellos, "first hello")
	second := readSignalingMessage(t, hellos, "reconnected hello")
	if first.Type != signaling.MessageAgentHello || second.Type != signaling.MessageAgentHello {
		t.Fatalf("unexpected reconnect hellos: first=%+v second=%+v", first, second)
	}
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("RunReceiver returned error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunReceiver did not stop after context cancellation")
	}
}

func mustAgentdTestTransferKey(t *testing.T) p2p.TransferKey {
	t.Helper()
	key, err := p2p.TransferKeyFromBytes(bytes.Repeat([]byte{0x42}, p2p.TransferKeySize))
	if err != nil {
		t.Fatalf("transfer key: %v", err)
	}
	return key
}

func assertJSONDoesNotExposeTransferKey(t *testing.T, payload []byte, key p2p.TransferKey) {
	t.Helper()
	secret := key.Bytes()
	if bytes.Contains(payload, secret) || bytes.Contains(payload, []byte(hex.EncodeToString(secret))) || bytes.Contains(payload, []byte(base64.StdEncoding.EncodeToString(secret))) || bytes.Contains(payload, []byte(base64.RawURLEncoding.EncodeToString(secret))) {
		t.Fatalf("payload exposes raw transfer key: %s", payload)
	}
}

func keyedAgentOfferPayloadForTest(t *testing.T, recipientAgentID string, fileName string, fileSize int64) ([]byte, p2p.TransferKey, []byte) {
	t.Helper()
	transferKey := mustAgentdTestTransferKey(t)
	recipientPrivate, recipientPublic, err := p2p.GenerateAgentEnvelopeKeyPair()
	if err != nil {
		t.Fatalf("GenerateAgentEnvelopeKeyPair: %v", err)
	}
	envelope, err := p2p.WrapTransferKeyForAgent(recipientAgentID, recipientPublic, transferKey)
	if err != nil {
		t.Fatalf("WrapTransferKeyForAgent: %v", err)
	}
	payload, err := json.Marshal(transferOfferPayload{FileName: fileName, FileSizeBytes: fileSize, KeyEnvelope: &envelope})
	if err != nil {
		t.Fatalf("marshal transfer offer payload: %v", err)
	}
	return payload, transferKey, recipientPrivate
}

func waitForAgentOnlineForTest(t *testing.T, baseURL string, agentID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, err := http.Get(baseURL + "/api/v1/agents/" + agentID)
		if err == nil {
			var payload struct {
				Status  string `json:"status"`
				Devices int    `json:"devices"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&payload)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && payload.Status == "online" && payload.Devices > 0 {
				return
			}
		} else if response != nil {
			_ = response.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent %s did not become online before transfer", agentID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeJSONForTest(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
