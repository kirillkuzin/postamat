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
	"time"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/p2p"
	"github.com/kirillkuzin/postamat/internal/sessions"
	"github.com/kirillkuzin/postamat/internal/signaling"
)

var (
	ErrBackendURLRequired     = errors.New("backend URL is required")
	ErrBackendClientRequired  = errors.New("backend client is required")
	ErrOfferRecipientMismatch = errors.New("offer recipient does not match local agent")
	ErrOfferDeniedByPolicy    = errors.New("offer denied by receive policy")
	ErrAgentAuthTokenRequired = errors.New("agent auth token is required")
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

func (c *BackendClient) CancelTransfer(ctx context.Context, transferID string) error {
	if c == nil || c.baseURL == nil || c.baseURL.String() == "" {
		return ErrBackendURLRequired
	}
	if transferID == "" {
		return ErrTransferIDRequired
	}
	endpoint := c.resolve("/api/v1/transfers/" + url.PathEscape(transferID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cancel backend transfer: status %d", resp.StatusCode)
	}
	return nil
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

func (c *BackendClient) AgentPresenceWebSocketURL(agentID string) (string, error) {
	if c == nil || c.baseURL == nil || c.baseURL.String() == "" {
		return "", ErrBackendURLRequired
	}
	endpoint := c.resolve("/api/v1/agents/ws")
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	}
	query := endpoint.Query()
	query.Set("agent_id", agentID)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (c *BackendClient) DialAgentPresenceWebSocket(ctx context.Context, agentID string, token string) (*websocket.Conn, error) {
	if token == "" {
		return nil, ErrAgentAuthTokenRequired
	}
	wsURL, err := c.AgentPresenceWebSocketURL(agentID)
	if err != nil {
		return nil, err
	}
	dialer := c.dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	return conn, err
}

func (c *BackendClient) resolve(path string) url.URL {
	resolved := *c.baseURL
	resolved.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	resolved.RawQuery = ""
	return resolved
}

type BackendLoopOptions struct {
	AgentID             string
	DeviceID            string
	Jobs                *JobManager
	Inbox               *Inbox
	Client              *BackendClient
	ReceivePolicy       ReceivePolicy
	AgentPrivateKey     []byte
	RecipientPublicKeys map[string][]byte
	GenerateTransferKey func() (p2p.TransferKey, error)
	AgentAuthToken      string
}

type ReceivePolicy struct {
	AllowedFromAgentIDs []string
	MaxFileSizeBytes    int64
	DenyAll             bool
}

type transferOfferPayload struct {
	FileName      string                `json:"file_name"`
	FileSizeBytes int64                 `json:"file_size_bytes"`
	KeyEnvelope   *p2p.AgentKeyEnvelope `json:"key_envelope,omitempty"`
}

type receivePolicyDecision struct {
	Accepted bool
	Reason   string
}

func (p ReceivePolicy) Evaluate(fromAgentID string, offer transferOfferPayload) receivePolicyDecision {
	if p.DenyAll {
		return receivePolicyDecision{Accepted: false, Reason: "receive_policy_denies_all"}
	}
	if len(p.AllowedFromAgentIDs) > 0 {
		allowed := false
		for _, candidate := range p.AllowedFromAgentIDs {
			if candidate == fromAgentID {
				allowed = true
				break
			}
		}
		if !allowed {
			return receivePolicyDecision{Accepted: false, Reason: "sender_not_allowed"}
		}
	}
	if p.MaxFileSizeBytes > 0 && offer.FileSizeBytes > p.MaxFileSizeBytes {
		return receivePolicyDecision{Accepted: false, Reason: "file_size_exceeds_receive_policy"}
	}
	return receivePolicyDecision{Accepted: true}
}

type BackendLoop struct {
	mu                  sync.Mutex
	agentID             string
	deviceID            string
	jobs                *JobManager
	inbox               *Inbox
	client              *BackendClient
	receivePolicy       ReceivePolicy
	agentPrivateKey     []byte
	recipientPublicKeys map[string][]byte
	generateTransferKey func() (p2p.TransferKey, error)
	agentAuthToken      string
}

func NewBackendLoop(options BackendLoopOptions) *BackendLoop {
	jobs := options.Jobs
	if jobs == nil {
		jobs = NewJobManager(nil)
	}
	generateKey := options.GenerateTransferKey
	if generateKey == nil {
		generateKey = p2p.NewRandomTransferKey
	}
	return &BackendLoop{
		agentID:             options.AgentID,
		deviceID:            options.DeviceID,
		jobs:                jobs,
		inbox:               options.Inbox,
		client:              options.Client,
		receivePolicy:       options.ReceivePolicy,
		agentPrivateKey:     append([]byte(nil), options.AgentPrivateKey...),
		recipientPublicKeys: clonePublicKeys(options.RecipientPublicKeys),
		generateTransferKey: generateKey,
		agentAuthToken:      options.AgentAuthToken,
	}
}

func clonePublicKeys(keys map[string][]byte) map[string][]byte {
	if len(keys) == 0 {
		return nil
	}
	cloned := make(map[string][]byte, len(keys))
	for agentID, key := range keys {
		cloned[agentID] = append([]byte(nil), key...)
	}
	return cloned
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
	if recipientPublicKey := l.recipientPublicKeys[input.ToAgentID]; len(recipientPublicKey) > 0 {
		transferKey, err := l.generateTransferKey()
		if err != nil {
			_, _ = l.jobs.Fail(job.ID, err.Error())
			return Job{}, err
		}
		if _, err := p2p.WrapTransferKeyForAgent(input.ToAgentID, recipientPublicKey, transferKey); err != nil {
			_, _ = l.jobs.Fail(job.ID, err.Error())
			return Job{}, err
		}
		job, err = l.jobs.AttachTransferKey(job.ID, transferKey)
		if err != nil {
			return Job{}, err
		}
	}
	return l.jobs.MarkOffered(job.ID)
}

func (l *BackendLoop) CancelTransfer(ctx context.Context, jobIDOrTransferID string) (Job, error) {
	job, err := l.jobs.Get(jobIDOrTransferID)
	if err != nil {
		if !errors.Is(err, ErrJobNotFound) {
			return Job{}, err
		}
		found, ok := l.jobs.FindByTransferID(jobIDOrTransferID)
		if !ok {
			return Job{}, ErrJobNotFound
		}
		job = found
	}
	if job.isTerminal() {
		return Job{}, ErrJobTerminal
	}
	if job.TransferID != "" && l.client != nil {
		if err := l.client.CancelTransfer(ctx, job.TransferID); err != nil {
			return Job{}, err
		}
	}
	return l.jobs.Cancel(job.ID)
}

func (l *BackendLoop) ConnectAgentPresence(ctx context.Context) (*websocket.Conn, error) {
	if l.client == nil {
		return nil, ErrBackendClientRequired
	}
	conn, err := l.client.DialAgentPresenceWebSocket(ctx, l.agentID, l.agentAuthToken)
	if err != nil {
		return nil, err
	}
	hello := signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: l.agentID, DeviceID: l.deviceID}
	if err := conn.WriteJSON(hello); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (l *BackendLoop) RunReceiver(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if l.agentAuthToken == "" {
		return ErrAgentAuthTokenRequired
	}
	backoff := 100 * time.Millisecond
	for {
		if err := l.runReceiverOnce(ctx); err != nil && ctx.Err() != nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
		if backoff < 2*time.Second {
			backoff *= 2
			if backoff > 2*time.Second {
				backoff = 2 * time.Second
			}
		}
	}
}

func (l *BackendLoop) runReceiverOnce(ctx context.Context) error {
	conn, err := l.ConnectAgentPresence(ctx)
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
	for {
		var envelope signaling.Envelope
		if err := conn.ReadJSON(&envelope); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := l.handleEnvelope(ctx, func(reply signaling.Envelope) error { return conn.WriteJSON(reply) }, envelope); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
	}
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
		if err := l.handleEnvelope(ctx, func(reply signaling.Envelope) error { return conn.WriteJSON(reply) }, envelope); err != nil {
			return err
		}
	}
}

func (l *BackendLoop) sendTransferOffer(conn *websocket.Conn, job Job) error {
	payloadData := transferOfferPayload{
		FileName:      job.FileName,
		FileSizeBytes: job.FileSizeBytes,
	}
	if job.HasTransferKey {
		recipientPublicKey := l.recipientPublicKeys[job.ToAgentID]
		if len(recipientPublicKey) == 0 {
			return ErrTransferKeyRequired
		}
		envelope, err := p2p.WrapTransferKeyForAgent(job.ToAgentID, recipientPublicKey, job.TransferKey)
		if err != nil {
			return err
		}
		payloadData.KeyEnvelope = &envelope
	}
	payload, err := json.Marshal(payloadData)
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
	return l.handleEnvelope(ctx, nil, envelope)
}

func (l *BackendLoop) handleEnvelope(ctx context.Context, send func(signaling.Envelope) error, envelope signaling.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch envelope.Type {
	case signaling.MessageTransferOffer:
		return l.handleTransferOffer(send, envelope)
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

func (l *BackendLoop) handleTransferOffer(send func(signaling.Envelope) error, envelope signaling.Envelope) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if envelope.ToAgentID != l.agentID {
		return ErrOfferRecipientMismatch
	}
	if _, ok := l.jobs.FindByTransferID(envelope.TransferID); ok {
		return l.sendOfferDecision(send, envelope, true, "")
	}
	var payload transferOfferPayload
	if len(envelope.Payload) > 0 {
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return err
		}
	}
	decision := l.receivePolicy.Evaluate(envelope.FromAgentID, payload)
	if !decision.Accepted {
		if err := l.sendOfferDecision(send, envelope, false, decision.Reason); err != nil {
			return err
		}
		if send != nil {
			return nil
		}
		return ErrOfferDeniedByPolicy
	}
	var transferKey p2p.TransferKey
	hasTransferKey := false
	if payload.KeyEnvelope != nil {
		if len(l.agentPrivateKey) == 0 {
			return ErrTransferKeyRequired
		}
		opened, err := p2p.OpenAgentKeyEnvelope(l.agentID, l.agentPrivateKey, *payload.KeyEnvelope)
		if err != nil {
			return err
		}
		transferKey = opened
		hasTransferKey = true
	}
	destinationPath := ""
	if l.inbox != nil {
		entry, err := l.inbox.Reserve(InboxOffer{TransferID: envelope.TransferID, FromAgentID: envelope.FromAgentID, FileName: payload.FileName, FileSizeBytes: payload.FileSizeBytes})
		if err != nil {
			return err
		}
		destinationPath = entry.DestinationPath
	}
	job, err := l.jobs.CreateReceiveJob(CreateReceiveJobInput{TransferID: envelope.TransferID, FromAgentID: envelope.FromAgentID, FileName: payload.FileName, FileSizeBytes: payload.FileSizeBytes, DestinationPath: destinationPath})
	if err != nil {
		return err
	}
	if hasTransferKey {
		job, err = l.jobs.AttachTransferKey(job.ID, transferKey)
		if err != nil {
			return err
		}
	}
	if _, err := l.jobs.MarkAccepted(job.ID); err != nil {
		return err
	}
	return l.sendOfferDecision(send, envelope, true, "")
}

func (l *BackendLoop) sendOfferDecision(send func(signaling.Envelope) error, offer signaling.Envelope, accepted bool, reason string) error {
	if send == nil {
		return nil
	}
	decision := signaling.Envelope{Type: signaling.MessageTransferAccepted, TransferID: offer.TransferID, FromAgentID: l.agentID, ToAgentID: offer.FromAgentID}
	if !accepted {
		decision.Type = signaling.MessageTransferDenied
		if reason != "" {
			payload, err := json.Marshal(map[string]string{"reason": reason})
			if err != nil {
				return err
			}
			decision.Payload = payload
		}
	}
	return send(decision)
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
