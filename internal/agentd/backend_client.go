package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/sessions"
	"github.com/kirillkuzin/postamat/internal/signaling"
)

var (
	ErrBackendURLRequired     = errors.New("backend URL is required")
	ErrBackendClientRequired  = errors.New("backend client is required")
	ErrOfferRecipientMismatch = errors.New("offer recipient does not match local agent")
)

type BackendClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	dialer     *websocket.Dialer
}

type CreateBackendTransferInput struct {
	FromAgentID   string
	ToAgentID     string
	FileName      string
	FileSizeBytes int64
}

type CreatedBackendTransfer struct {
	TransferID  string `json:"transfer_id"`
	AgentTicket string `json:"agent_ticket"`
	Status      string `json:"status"`
}

func NewBackendClient(baseURL string, httpClient *http.Client) *BackendClient {
	parsed, _ := url.Parse(baseURL)
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &BackendClient{baseURL: parsed, httpClient: httpClient, dialer: websocket.DefaultDialer}
}

func (c *BackendClient) CreateTransfer(ctx context.Context, input CreateBackendTransferInput) (CreatedBackendTransfer, error) {
	if c == nil || c.baseURL == nil || c.baseURL.String() == "" {
		return CreatedBackendTransfer{}, ErrBackendURLRequired
	}
	endpoint := c.resolve("/api/v1/transfers")
	payload := map[string]any{
		"from_agent_id":   input.FromAgentID,
		"to_agent_id":     input.ToAgentID,
		"target":          sessions.TargetAgent,
		"file_name":       input.FileName,
		"file_size_bytes": input.FileSizeBytes,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CreatedBackendTransfer{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return CreatedBackendTransfer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CreatedBackendTransfer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CreatedBackendTransfer{}, fmt.Errorf("create backend transfer: status %d", resp.StatusCode)
	}
	var created CreatedBackendTransfer
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return CreatedBackendTransfer{}, err
	}
	if created.TransferID == "" || created.AgentTicket == "" {
		return CreatedBackendTransfer{}, ErrTransferIDRequired
	}
	return created, nil
}

func (c *BackendClient) AgentWebSocketURL(transferID string, ticket string) (string, error) {
	if c == nil || c.baseURL == nil || c.baseURL.String() == "" {
		return "", ErrBackendURLRequired
	}
	endpoint := c.resolve("/api/v1/agent/ws")
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	}
	query := endpoint.Query()
	query.Set("transfer_id", transferID)
	query.Set("ticket", ticket)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (c *BackendClient) DialAgentWebSocket(ctx context.Context, transferID string, ticket string) (*websocket.Conn, error) {
	wsURL, err := c.AgentWebSocketURL(transferID, ticket)
	if err != nil {
		return nil, err
	}
	dialer := c.dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	return conn, err
}

func (c *BackendClient) resolve(path string) url.URL {
	resolved := *c.baseURL
	resolved.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	resolved.RawQuery = ""
	return resolved
}

type BackendLoopOptions struct {
	AgentID  string
	DeviceID string
	Jobs     *JobManager
	Inbox    *Inbox
	Client   *BackendClient
}

type BackendLoop struct {
	mu       sync.Mutex
	agentID  string
	deviceID string
	jobs     *JobManager
	inbox    *Inbox
	client   *BackendClient
}

func NewBackendLoop(options BackendLoopOptions) *BackendLoop {
	jobs := options.Jobs
	if jobs == nil {
		jobs = NewJobManager(nil)
	}
	return &BackendLoop{agentID: options.AgentID, deviceID: options.DeviceID, jobs: jobs, inbox: options.Inbox, client: options.Client}
}

func (l *BackendLoop) CreateSendTransfer(ctx context.Context, input CreateSendJobInput) (Job, error) {
	if l.client == nil {
		return Job{}, ErrBackendClientRequired
	}
	job, err := l.jobs.CreateSendJob(input)
	if err != nil {
		return Job{}, err
	}
	created, err := l.client.CreateTransfer(ctx, CreateBackendTransferInput{FromAgentID: l.agentID, ToAgentID: input.ToAgentID, FileName: input.FileName, FileSizeBytes: input.FileSizeBytes})
	if err != nil {
		_, _ = l.jobs.Fail(job.ID, err.Error())
		return Job{}, err
	}
	job, err = l.jobs.AttachTransfer(job.ID, created.TransferID, created.AgentTicket)
	if err != nil {
		return Job{}, err
	}
	return l.jobs.MarkOffered(job.ID)
}

func (l *BackendLoop) RunTransfer(ctx context.Context, job Job) error {
	if job.TransferID == "" || job.AgentTicket == "" {
		return ErrTransferIDRequired
	}
	conn, err := l.ConnectTransfer(ctx, job.TransferID, job.AgentTicket)
	if err != nil {
		return err
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)
	conn.SetReadLimit(1 << 20)
	if job.Direction == JobDirectionSend {
		if err := l.sendTransferOffer(conn, job); err != nil {
			return err
		}
	}
	for {
		var envelope signaling.Envelope
		if err := conn.ReadJSON(&envelope); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if envelope.TransferID != "" && envelope.TransferID != job.TransferID {
			return ErrOfferRecipientMismatch
		}
		if err := l.HandleEnvelope(ctx, envelope); err != nil {
			return err
		}
	}
}

func (l *BackendLoop) sendTransferOffer(conn *websocket.Conn, job Job) error {
	payload, err := json.Marshal(map[string]any{
		"file_name":       job.FileName,
		"file_size_bytes": job.FileSizeBytes,
	})
	if err != nil {
		return err
	}
	return conn.WriteJSON(signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: job.TransferID, FromAgentID: l.agentID, ToAgentID: job.ToAgentID, Payload: payload})
}

func (l *BackendLoop) ConnectTransfer(ctx context.Context, transferID string, ticket string) (*websocket.Conn, error) {
	if l.client == nil {
		return nil, ErrBackendClientRequired
	}
	conn, err := l.client.DialAgentWebSocket(ctx, transferID, ticket)
	if err != nil {
		return nil, err
	}
	hello := signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: l.agentID, DeviceID: l.deviceID, TransferID: transferID}
	if err := conn.WriteJSON(hello); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (l *BackendLoop) SendHeartbeat(conn *websocket.Conn) error {
	return conn.WriteJSON(signaling.Envelope{Type: signaling.MessagePing, AgentID: l.agentID, DeviceID: l.deviceID})
}

func (l *BackendLoop) HandleEnvelope(ctx context.Context, envelope signaling.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch envelope.Type {
	case signaling.MessageTransferOffer:
		l.mu.Lock()
		defer l.mu.Unlock()
		if envelope.ToAgentID != l.agentID {
			return ErrOfferRecipientMismatch
		}
		if _, ok := l.jobs.FindByTransferID(envelope.TransferID); ok {
			return nil
		}
		var payload struct {
			FileName      string `json:"file_name"`
			FileSizeBytes int64  `json:"file_size_bytes"`
		}
		if len(envelope.Payload) > 0 {
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return err
			}
		}
		destinationPath := ""
		if l.inbox != nil {
			entry, err := l.inbox.Reserve(InboxOffer{TransferID: envelope.TransferID, FromAgentID: envelope.FromAgentID, FileName: payload.FileName, FileSizeBytes: payload.FileSizeBytes})
			if err != nil {
				return err
			}
			destinationPath = entry.DestinationPath
		}
		_, err := l.jobs.CreateReceiveJob(CreateReceiveJobInput{TransferID: envelope.TransferID, FromAgentID: envelope.FromAgentID, FileName: payload.FileName, FileSizeBytes: payload.FileSizeBytes, DestinationPath: destinationPath})
		return err
	case signaling.MessageTransferAccepted:
		return l.updateTransferJob(envelope.TransferID, l.jobs.MarkAccepted)
	case signaling.MessageTransferStarted:
		if err := l.updateTransferJob(envelope.TransferID, l.jobs.MarkConnecting); err != nil {
			return err
		}
		return l.updateTransferJob(envelope.TransferID, l.jobs.MarkTransferring)
	case signaling.MessageTransferProgress:
		if envelope.TransferID == "" {
			return ErrTransferIDRequired
		}
		var payload struct {
			ProgressBytes int64 `json:"progress_bytes"`
		}
		if len(envelope.Payload) > 0 {
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return err
			}
		}
		job, ok := l.jobs.FindByTransferID(envelope.TransferID)
		if !ok {
			return ErrJobNotFound
		}
		_, err := l.jobs.UpdateProgress(job.ID, payload.ProgressBytes)
		return err
	case signaling.MessageTransferCompleted:
		return l.updateTransferJob(envelope.TransferID, l.jobs.Complete)
	case signaling.MessageTransferFailed:
		return l.failTransferJob(envelope.TransferID, "remote transfer failed")
	case signaling.MessageTransferDenied:
		return l.failTransferJob(envelope.TransferID, "remote transfer denied")
	case signaling.MessageTransferExpired:
		return l.failTransferJob(envelope.TransferID, "remote transfer expired")
	case signaling.MessageTransferCancelled:
		return l.updateTransferJob(envelope.TransferID, l.jobs.Cancel)
	default:
		return nil
	}
}

func (l *BackendLoop) updateTransferJob(transferID string, update func(string) (Job, error)) error {
	if transferID == "" {
		return ErrTransferIDRequired
	}
	job, ok := l.jobs.FindByTransferID(transferID)
	if !ok {
		return ErrJobNotFound
	}
	_, err := update(job.ID)
	return err
}

func (l *BackendLoop) failTransferJob(transferID string, reason string) error {
	if transferID == "" {
		return ErrTransferIDRequired
	}
	job, ok := l.jobs.FindByTransferID(transferID)
	if !ok {
		return ErrJobNotFound
	}
	_, err := l.jobs.Fail(job.ID, reason)
	return err
}
