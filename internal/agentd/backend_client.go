package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
	ErrPublicTokenRequired    = errors.New("public token is required")
)

type BackendClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	dialer     *websocket.Dialer
}

type CreateBackendTransferInput struct {
	FromAgentID   string
	ToAgentID     string
	BrowserLink   bool
	FileName      string
	FileSizeBytes int64
}

type CreatedBackendTransfer struct {
	TransferID  string `json:"transfer_id"`
	AgentTicket string `json:"agent_ticket"`
	PublicToken string `json:"public_token"`
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
	target := sessions.TargetAgent
	if input.BrowserLink {
		target = sessions.TargetBrowserLink
	}
	payload := map[string]any{
		"from_agent_id":   input.FromAgentID,
		"target":          target,
		"file_name":       input.FileName,
		"file_size_bytes": input.FileSizeBytes,
	}
	if input.ToAgentID != "" {
		payload["to_agent_id"] = input.ToAgentID
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
	PublicBaseURL       string
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
	publicBaseURL       string
	generateTransferKey func() (p2p.TransferKey, error)
	agentAuthToken      string
	live                map[string]*liveWebRTCSession
}

type liveWebRTCSession struct {
	peer       *p2p.RemoteWebRTCPeer
	pendingICE []p2p.ICECandidate
	done       chan error
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
		publicBaseURL:       strings.TrimRight(options.PublicBaseURL, "/"),
		generateTransferKey: generateKey,
		agentAuthToken:      options.AgentAuthToken,
		live:                make(map[string]*liveWebRTCSession),
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
	created, err := l.client.CreateTransfer(ctx, CreateBackendTransferInput{FromAgentID: l.agentID, ToAgentID: input.ToAgentID, BrowserLink: input.BrowserLink, FileName: input.FileName, FileSizeBytes: input.FileSizeBytes})
	if err != nil {
		_, _ = l.jobs.Fail(job.ID, err.Error())
		return Job{}, err
	}
	job, err = l.jobs.AttachTransfer(job.ID, created.TransferID, created.AgentTicket)
	if err != nil {
		return Job{}, err
	}
	if input.BrowserLink {
		if created.PublicToken == "" {
			_, _ = l.jobs.Fail(job.ID, ErrPublicTokenRequired.Error())
			return Job{}, ErrPublicTokenRequired
		}
		transferKey, err := l.generateTransferKey()
		if err != nil {
			_, _ = l.jobs.Fail(job.ID, err.Error())
			return Job{}, err
		}
		job, err = l.jobs.AttachTransferKey(job.ID, transferKey)
		if err != nil {
			return Job{}, err
		}
		browserURL := l.browserURL(created.PublicToken, transferKey)
		job, err = l.jobs.AttachPublicLink(job.ID, created.PublicToken, browserURL)
		if err != nil {
			return Job{}, err
		}
	} else if recipientPublicKey := l.recipientPublicKeys[input.ToAgentID]; len(recipientPublicKey) > 0 {
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

func (l *BackendLoop) browserURL(publicToken string, key p2p.TransferKey) string {
	if publicToken == "" || l.publicBaseURL == "" {
		return ""
	}
	return l.publicBaseURL + "/p/" + url.PathEscape(publicToken) + "#" + p2p.NewBrowserKeyFragment(key).Fragment
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
	var writeMu sync.Mutex
	send := func(reply signaling.Envelope) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(reply)
	}
	for {
		var envelope signaling.Envelope
		if err := conn.ReadJSON(&envelope); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := l.handleEnvelope(ctx, send, envelope); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if envelope.TransferID != "" && envelope.FromAgentID != "" {
				if _, ok := l.jobs.FindByTransferID(envelope.TransferID); ok {
					_ = l.failTransferJob(envelope.TransferID, err.Error())
					_ = sendTransferState(send, signaling.MessageTransferFailed, envelope.TransferID, l.agentID, envelope.FromAgentID, nil)
				}
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
	defer l.closeLiveSession(job.TransferID)
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
	var writeMu sync.Mutex
	send := func(reply signaling.Envelope) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(reply)
	}
	if job.Direction == JobDirectionSend {
		if err := l.sendTransferOffer(send, job); err != nil {
			return err
		}
	}
	readCh := make(chan signaling.Envelope, 1)
	errCh := make(chan error, 1)
	go func() {
		for {
			var envelope signaling.Envelope
			if err := conn.ReadJSON(&envelope); err != nil {
				errCh <- err
				return
			}
			select {
			case readCh <- envelope:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		var doneCh chan error
		if session := l.liveSession(job.TransferID, false); session != nil {
			doneCh = session.done
		}
		select {
		case envelope := <-readCh:
			if envelope.TransferID != "" && envelope.TransferID != job.TransferID {
				return ErrOfferRecipientMismatch
			}
			if err := l.handleEnvelope(ctx, send, envelope); err != nil {
				return err
			}
			switch envelope.Type {
			case signaling.MessageTransferCompleted:
				l.closeLiveSession(job.TransferID)
				return nil
			case signaling.MessageTransferFailed, signaling.MessageTransferDenied, signaling.MessageTransferExpired, signaling.MessageTransferCancelled:
				l.closeLiveSession(job.TransferID)
				return p2p.ErrTransferFailed
			}
		case err := <-doneCh:
			if err != nil {
				l.closeLiveSession(job.TransferID)
				return err
			}
		case err := <-errCh:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

func (l *BackendLoop) sendTransferOffer(send func(signaling.Envelope) error, job Job) error {
	payloadData := transferOfferPayload{
		FileName:      job.FileName,
		FileSizeBytes: job.FileSizeBytes,
	}
	toAgentID := job.ToAgentID
	if job.BrowserLink {
		toAgentID = browserRecipientAgentID(job.TransferID)
	}
	if job.HasTransferKey && !job.BrowserLink {
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
	return send(signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: job.TransferID, FromAgentID: l.agentID, ToAgentID: toAgentID, Payload: payload})
}

func browserRecipientAgentID(transferID string) string {
	return "browser_recipient:" + transferID
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
		if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
			return err
		}
		if err := l.updateTransferJob(envelope.TransferID, l.jobs.MarkAccepted); err != nil && !errors.Is(err, ErrInvalidJobStatus) {
			return err
		}
		return l.startSenderWebRTC(ctx, send, envelope.TransferID, envelope.FromAgentID)
	case signaling.MessageWebRTCOffer:
		return l.handleWebRTCOffer(ctx, send, envelope)
	case signaling.MessageWebRTCAnswer:
		return l.handleWebRTCAnswer(ctx, send, envelope)
	case signaling.MessageWebRTCICE:
		return l.handleWebRTCICE(envelope)
	case signaling.MessageTransferStarted:
		if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
			return err
		}
		if err := l.markTransferStarted(envelope.TransferID); err != nil {
			return err
		}
		return nil
	case signaling.MessageTransferProgress:
		if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
			return err
		}
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
		if job.isTerminal() {
			return nil
		}
		_, err := l.jobs.UpdateProgress(job.ID, payload.ProgressBytes)
		return err
	case signaling.MessageTransferCompleted:
		if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
			return err
		}
		job, ok := l.jobs.FindByTransferID(envelope.TransferID)
		if !ok {
			return ErrJobNotFound
		}
		if job.Direction == JobDirectionReceive {
			return signaling.ErrRouteTargetRequired
		}
		return l.completeTransferJob(envelope.TransferID)
	case signaling.MessageTransferFailed:
		if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
			return err
		}
		l.closeLiveSession(envelope.TransferID)
		return l.failTransferJob(envelope.TransferID, "remote transfer failed")
	case signaling.MessageTransferDenied:
		if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
			return err
		}
		l.closeLiveSession(envelope.TransferID)
		return l.failTransferJob(envelope.TransferID, "remote transfer denied")
	case signaling.MessageTransferExpired:
		l.closeLiveSession(envelope.TransferID)
		return l.failTransferJob(envelope.TransferID, "remote transfer expired")
	case signaling.MessageTransferCancelled:
		l.closeLiveSession(envelope.TransferID)
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
	if payload.KeyEnvelope == nil {
		if err := l.sendOfferDecision(send, envelope, false, "transfer_key_required"); err != nil {
			return err
		}
		if send != nil {
			return nil
		}
		return ErrTransferKeyRequired
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

func (l *BackendLoop) validateIncomingTransferEnvelope(envelope signaling.Envelope) error {
	if envelope.TransferID == "" {
		return ErrTransferIDRequired
	}
	job, ok := l.jobs.FindByTransferID(envelope.TransferID)
	if !ok {
		return ErrJobNotFound
	}
	if envelope.ToAgentID != "" && envelope.ToAgentID != l.agentID {
		return ErrOfferRecipientMismatch
	}
	if envelope.FromAgentID != "" && envelope.FromAgentID != expectedPeerAgentID(job) {
		return ErrOfferRecipientMismatch
	}
	return nil
}

func expectedPeerAgentID(job Job) string {
	if job.Direction == JobDirectionSend {
		if job.BrowserLink {
			return browserRecipientAgentID(job.TransferID)
		}
		return job.ToAgentID
	}
	return job.FromAgentID
}

const maxPendingLiveICECandidates = 64

func (l *BackendLoop) startSenderWebRTC(ctx context.Context, send func(signaling.Envelope) error, transferID string, peerAgentID string) error {
	if send == nil {
		return nil
	}
	job, ok := l.jobs.FindByTransferID(transferID)
	if !ok {
		return ErrJobNotFound
	}
	if job.Direction != JobDirectionSend {
		return ErrTransferJobMismatch
	}
	if !job.HasTransferKey {
		return ErrTransferKeyRequired
	}
	session := l.liveSession(transferID, true)
	if session.peer != nil {
		return nil
	}
	peer, err := p2p.NewRemoteWebRTCOfferPeer("postamat-transfer", func(candidate p2p.ICECandidate) {
		_ = sendWebRTCPayload(send, signaling.MessageWebRTCICE, transferID, l.agentID, peerAgentID, candidate)
	})
	if err != nil {
		return err
	}
	session.peer = peer
	l.flushLiveSessionICE(session)
	offer, err := peer.CreateOffer()
	if err != nil {
		return err
	}
	return sendWebRTCPayload(send, signaling.MessageWebRTCOffer, transferID, l.agentID, peerAgentID, offer)
}

func (l *BackendLoop) handleWebRTCOffer(ctx context.Context, send func(signaling.Envelope) error, envelope signaling.Envelope) error {
	if send == nil {
		return nil
	}
	if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
		return err
	}
	var offer p2p.SessionDescription
	if err := json.Unmarshal(envelope.Payload, &offer); err != nil {
		return err
	}
	job, ok := l.jobs.FindByTransferID(envelope.TransferID)
	if !ok {
		return ErrJobNotFound
	}
	if job.Direction != JobDirectionReceive {
		return ErrTransferJobMismatch
	}
	if !job.HasTransferKey {
		return ErrTransferKeyRequired
	}
	session := l.liveSession(envelope.TransferID, true)
	if session.peer == nil {
		peer, err := p2p.NewRemoteWebRTCAnswerPeer(func(candidate p2p.ICECandidate) {
			_ = sendWebRTCPayload(send, signaling.MessageWebRTCICE, envelope.TransferID, l.agentID, envelope.FromAgentID, candidate)
		})
		if err != nil {
			return err
		}
		session.peer = peer
	}
	answer, err := session.peer.AcceptOfferCreateAnswer(offer)
	if err != nil {
		return err
	}
	l.flushLiveSessionICE(session)
	if err := sendWebRTCPayload(send, signaling.MessageWebRTCAnswer, envelope.TransferID, l.agentID, envelope.FromAgentID, answer); err != nil {
		return err
	}
	go l.runLiveReceiver(ctx, send, job, session)
	return nil
}

func (l *BackendLoop) handleWebRTCAnswer(ctx context.Context, send func(signaling.Envelope) error, envelope signaling.Envelope) error {
	if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
		return err
	}
	var answer p2p.SessionDescription
	if err := json.Unmarshal(envelope.Payload, &answer); err != nil {
		return err
	}
	session := l.liveSession(envelope.TransferID, false)
	if session == nil || session.peer == nil {
		return ErrTransferIDRequired
	}
	if err := session.peer.AcceptAnswer(answer); err != nil {
		return err
	}
	job, ok := l.jobs.FindByTransferID(envelope.TransferID)
	if !ok {
		return ErrJobNotFound
	}
	go l.runLiveSender(ctx, send, job, envelope.FromAgentID, session)
	return nil
}

func (l *BackendLoop) handleWebRTCICE(envelope signaling.Envelope) error {
	if err := l.validateIncomingTransferEnvelope(envelope); err != nil {
		return err
	}
	job, ok := l.jobs.FindByTransferID(envelope.TransferID)
	if !ok {
		return ErrJobNotFound
	}
	if job.isTerminal() {
		return nil
	}
	var candidate p2p.ICECandidate
	if err := json.Unmarshal(envelope.Payload, &candidate); err != nil {
		return err
	}
	session := l.liveSession(envelope.TransferID, false)
	if session == nil || session.peer == nil {
		session = l.liveSession(envelope.TransferID, true)
		if len(session.pendingICE) >= maxPendingLiveICECandidates {
			return p2p.ErrTransferFailed
		}
		session.pendingICE = append(session.pendingICE, candidate)
		return nil
	}
	return session.peer.AddICECandidate(candidate)
}

func (l *BackendLoop) runLiveSender(ctx context.Context, send func(signaling.Envelope) error, job Job, peerAgentID string, session *liveWebRTCSession) {
	session.done <- l.runLiveSenderOnce(ctx, send, job, peerAgentID, session)
}

func (l *BackendLoop) runLiveSenderOnce(ctx context.Context, send func(signaling.Envelope) error, job Job, peerAgentID string, session *liveWebRTCSession) error {
	if !job.HasTransferKey {
		return ErrTransferKeyRequired
	}
	channel, err := session.peer.WaitOutboundDataChannel(ctx, 64*1024)
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, peerAgentID, nil)
		return err
	}
	if err := l.markTransferStarted(job.TransferID); err != nil {
		return err
	}
	_ = sendTransferState(send, signaling.MessageTransferStarted, job.TransferID, l.agentID, peerAgentID, nil)
	source, err := os.Open(job.SourcePath)
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, peerAgentID, nil)
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, peerAgentID, nil)
		return err
	}
	if info.Size() != job.FileSizeBytes {
		err := fmt.Errorf("source size %d does not match declared transfer size %d", info.Size(), job.FileSizeBytes)
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, peerAgentID, nil)
		return err
	}
	cipher, err := p2p.NewChunkCipher(job.TransferKey)
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, peerAgentID, nil)
		return err
	}
	_, err = p2p.StreamReader(ctx, job.TransferID, source, channel, p2p.SenderOptions{
		Encryption: cipher,
		OnProgress: func(progress p2p.Progress) {
			_, _ = l.jobs.UpdateProgress(job.ID, progress.BytesTransferred)
			_ = sendProgress(send, job.TransferID, l.agentID, peerAgentID, progress.BytesTransferred)
		},
	})
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, peerAgentID, nil)
		return err
	}
	return nil
}

func (l *BackendLoop) runLiveReceiver(ctx context.Context, send func(signaling.Envelope) error, job Job, session *liveWebRTCSession) {
	defer l.closeLiveSession(job.TransferID)
	session.done <- l.runLiveReceiverOnce(ctx, send, job, session)
}

func (l *BackendLoop) runLiveReceiverOnce(ctx context.Context, send func(signaling.Envelope) error, job Job, session *liveWebRTCSession) error {
	incoming, err := session.peer.WaitIncomingMessages(ctx)
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
		return err
	}
	if err := l.markTransferStarted(job.TransferID); err != nil {
		return err
	}
	_ = sendTransferState(send, signaling.MessageTransferStarted, job.TransferID, l.agentID, job.FromAgentID, nil)
	if job.DestinationPath == "" {
		_ = l.failTransferJob(job.TransferID, ErrDestinationPathRequired.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
		return ErrDestinationPathRequired
	}
	if _, err := os.Lstat(job.DestinationPath); err == nil {
		err = fmt.Errorf("destination exists: %s", job.DestinationPath)
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	partialPath := job.DestinationPath + ".part"
	destination, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
		return err
	}
	partialCommitted := false
	defer func() {
		_ = destination.Close()
		if !partialCommitted {
			_ = os.Remove(partialPath)
		}
	}()
	cipher, err := p2p.NewChunkCipher(job.TransferKey)
	if err != nil {
		_ = l.failTransferJob(job.TransferID, err.Error())
		_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
		return err
	}
	receiver := p2p.NewReceiver(job.TransferID, destination, p2p.ReceiverOptions{
		Encryption:        cipher,
		RequireEncryption: true,
		OnProgress: func(progress p2p.Progress) {
			_, _ = l.jobs.UpdateProgress(job.ID, progress.BytesTransferred)
			_ = sendProgress(send, job.TransferID, l.agentID, job.FromAgentID, progress.BytesTransferred)
		},
	})
	for {
		select {
		case <-ctx.Done():
			err := fmt.Errorf("%w: %v", p2p.ErrTransferFailed, ctx.Err())
			_ = l.failTransferJob(job.TransferID, err.Error())
			_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
			return err
		case <-session.peer.Done():
			err := fmt.Errorf("%w: remote peer closed", p2p.ErrTransferFailed)
			_ = l.failTransferJob(job.TransferID, err.Error())
			_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
			return err
		case message := <-incoming:
			manifest, err := receiver.Accept(message)
			if err != nil {
				_ = l.failTransferJob(job.TransferID, err.Error())
				_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
				return err
			}
			if manifest == nil {
				continue
			}
			if manifest.TotalBytes != job.FileSizeBytes {
				err := fmt.Errorf("manifest total %d does not match declared transfer size %d", manifest.TotalBytes, job.FileSizeBytes)
				_ = l.failTransferJob(job.TransferID, err.Error())
				_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
				return err
			}
			if err := destination.Close(); err != nil {
				return err
			}
			if err := commitDestinationNoReplace(partialPath, job.DestinationPath); err != nil {
				_ = l.failTransferJob(job.TransferID, err.Error())
				_ = sendTransferState(send, signaling.MessageTransferFailed, job.TransferID, l.agentID, job.FromAgentID, nil)
				return err
			}
			partialCommitted = true
			if err := l.completeTransferJob(job.TransferID); err != nil {
				return err
			}
			_ = sendTransferState(send, signaling.MessageTransferCompleted, job.TransferID, l.agentID, job.FromAgentID, nil)
			return nil
		}
	}
}

func (l *BackendLoop) liveSession(transferID string, create bool) *liveWebRTCSession {
	if transferID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	session := l.live[transferID]
	if session == nil && create {
		session = &liveWebRTCSession{done: make(chan error, 1)}
		l.live[transferID] = session
	}
	return session
}

func (l *BackendLoop) closeLiveSession(transferID string) {
	l.mu.Lock()
	session := l.live[transferID]
	delete(l.live, transferID)
	l.mu.Unlock()
	if session != nil && session.peer != nil {
		session.peer.Close()
	}
}

func (l *BackendLoop) flushLiveSessionICE(session *liveWebRTCSession) {
	if session == nil || session.peer == nil {
		return
	}
	pending := append([]p2p.ICECandidate(nil), session.pendingICE...)
	session.pendingICE = nil
	for _, candidate := range pending {
		_ = session.peer.AddICECandidate(candidate)
	}
}

func sendWebRTCPayload(send func(signaling.Envelope) error, messageType signaling.MessageType, transferID string, fromAgentID string, toAgentID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return sendTransferState(send, messageType, transferID, fromAgentID, toAgentID, data)
}

func sendProgress(send func(signaling.Envelope) error, transferID string, fromAgentID string, toAgentID string, progressBytes int64) error {
	payload, err := json.Marshal(map[string]int64{"progress_bytes": progressBytes})
	if err != nil {
		return err
	}
	return sendTransferState(send, signaling.MessageTransferProgress, transferID, fromAgentID, toAgentID, payload)
}

func sendTransferState(send func(signaling.Envelope) error, messageType signaling.MessageType, transferID string, fromAgentID string, toAgentID string, payload json.RawMessage) error {
	if send == nil {
		return nil
	}
	return send(signaling.Envelope{Type: messageType, TransferID: transferID, FromAgentID: fromAgentID, ToAgentID: toAgentID, Payload: payload})
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

func (l *BackendLoop) markTransferStarted(transferID string) error {
	if transferID == "" {
		return ErrTransferIDRequired
	}
	job, ok := l.jobs.FindByTransferID(transferID)
	if !ok {
		return ErrJobNotFound
	}
	switch job.Status {
	case JobStatusOffered:
		if _, err := l.jobs.MarkAccepted(job.ID); err != nil {
			return err
		}
		fallthrough
	case JobStatusAccepted:
		if _, err := l.jobs.MarkConnecting(job.ID); err != nil {
			return err
		}
		_, err := l.jobs.MarkTransferring(job.ID)
		return err
	case JobStatusConnecting:
		_, err := l.jobs.MarkTransferring(job.ID)
		return err
	case JobStatusTransferring, JobStatusCompleted:
		return nil
	default:
		return ErrInvalidJobStatus
	}
}

func (l *BackendLoop) completeTransferJob(transferID string) error {
	if transferID == "" {
		return ErrTransferIDRequired
	}
	job, ok := l.jobs.FindByTransferID(transferID)
	if !ok {
		return ErrJobNotFound
	}
	if job.Status == JobStatusCompleted {
		return nil
	}
	if job.Status != JobStatusTransferring {
		return ErrInvalidJobStatus
	}
	if job.Direction == JobDirectionSend && job.ProgressBytes != job.FileSizeBytes {
		return ErrProgressOutOfRange
	}
	_, err := l.jobs.Complete(job.ID)
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
	if job.isTerminal() {
		return nil
	}
	_, err := l.jobs.Fail(job.ID, reason)
	return err
}
