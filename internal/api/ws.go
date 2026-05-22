package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/sessions"
	"github.com/kirillkuzin/postamat/internal/signaling"
)

type websocketPeer struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

type websocketAuth struct {
	AgentID        string
	AllowedAgents  map[string]struct{}
	AllowedTargets map[string]struct{}
	DeviceID       string
	TransferID     string
	Browser        bool
	AgentScoped    bool
}

func (p *websocketPeer) Send(envelope signaling.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn.WriteJSON(envelope)
}

func (r *Router) handleAgentWebSocket(w http.ResponseWriter, req *http.Request) {
	transferID := req.URL.Query().Get("transfer_id")
	ticket := req.URL.Query().Get("ticket")
	if transferID == "" || ticket == "" {
		writeError(w, http.StatusUnauthorized, "transfer_id and ticket are required")
		return
	}
	transfer, err := r.service.VerifyAgentTicket(req.Context(), transferID, ticket)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid ticket")
		return
	}
	allowedTargets := map[string]struct{}{}
	if transfer.ToAgentID != "" {
		allowedTargets[transfer.ToAgentID] = struct{}{}
	} else {
		allowedTargets[browserRecipientAgentID(transfer.ID)] = struct{}{}
	}
	// Agent tickets are issued to the sender that created the transfer. The
	// receiver uses the authenticated always-on agent socket, so do not let a
	// ticket holder self-assert the receiver role on this transfer-scoped socket.
	r.handleSignalingWebSocket(w, req, websocketAuth{AgentID: transfer.FromAgentID, AllowedTargets: allowedTargets, TransferID: transfer.ID})
}

func (r *Router) handleAuthenticatedAgentWebSocket(w http.ResponseWriter, req *http.Request) {
	agentID := req.URL.Query().Get("agent_id")
	if agentID == "" {
		writeError(w, http.StatusUnauthorized, "agent_id is required")
		return
	}
	if r.agentAuth == nil {
		writeError(w, http.StatusUnauthorized, "agent auth is not configured")
		return
	}
	token, ok := bearerToken(req.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "bearer token is required")
		return
	}
	if err := r.agentAuth.VerifyAgentToken(req.Context(), agentID, token); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid agent token")
		return
	}
	r.handleSignalingWebSocket(w, req, websocketAuth{AgentID: agentID, AgentScoped: true})
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token, token != ""
}

func (r *Router) handleBrowserReceiverWebSocket(w http.ResponseWriter, req *http.Request) {
	publicToken, ok := receiverTokenFromPath(req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}
	ticket := req.URL.Query().Get("ticket")
	if ticket == "" {
		writeError(w, http.StatusUnauthorized, "ticket is required")
		return
	}
	transfer, err := r.service.VerifyBrowserReceiverTicket(req.Context(), publicToken, ticket)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid public token")
		return
	}
	browserAgentID := browserRecipientAgentID(transfer.ID)
	r.handleSignalingWebSocket(w, req, websocketAuth{AgentID: browserAgentID, AllowedTargets: map[string]struct{}{transfer.FromAgentID: {}, browserAgentID: {}}, TransferID: transfer.ID, Browser: true})
}

func browserRecipientAgentID(transferID string) string {
	return "browser_recipient:" + transferID
}

func receiverTokenFromPath(path string) (string, bool) {
	const prefix = "/api/public/transfers/"
	const suffix = "/receiver/ws"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	token := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if token == "" || strings.Contains(token, "/") {
		return "", false
	}
	return token, true
}

func (r *Router) handleSignalingWebSocket(w http.ResponseWriter, req *http.Request, auth websocketAuth) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)

	peer := &websocketPeer{conn: conn}
	agentID, deviceID, ok := r.readHello(conn, auth)
	if !ok {
		return
	}
	auth.AgentID = agentID
	r.presence.Register(signaling.DevicePresence{AgentID: agentID, DeviceID: deviceID, Capabilities: []string{"webrtc_datachannel"}})
	if auth.AgentScoped {
		r.rooms.AttachPeer(agentID, deviceID, peer)
	} else {
		r.rooms.AttachTransferPeer(agentID, deviceID, auth.TransferID, peer)
	}
	_ = peer.Send(signaling.Envelope{Type: signaling.MessageAgentPresence, AgentID: agentID, DeviceID: deviceID})
	defer r.rooms.DetachPeer(agentID, deviceID, peer)

	for {
		var envelope signaling.Envelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return
		}
		if envelope.Type == signaling.MessagePing {
			_ = peer.Send(signaling.Envelope{Type: signaling.MessagePong, AgentID: agentID, DeviceID: deviceID})
			continue
		}
		if envelope.Type == signaling.MessagePong {
			continue
		}
		if envelope.Type == signaling.MessageAgentPresence {
			r.presence.Touch(agentID, deviceID)
			continue
		}
		if envelope.TransferID != "" {
			if _, err := r.service.VerifyTransferUsable(req.Context(), envelope.TransferID); err != nil {
				_ = peer.Send(errorEnvelope("transfer is no longer active"))
				return
			}
		}
		if err := r.authorizeOutboundEnvelope(req.Context(), auth, envelope); err != nil {
			_ = peer.Send(errorEnvelope(err.Error()))
			continue
		}
		if err := r.rooms.Route(envelope); err != nil {
			_ = peer.Send(errorEnvelope(err.Error()))
		}
	}
}

func (r *Router) authorizeOutboundEnvelope(ctx context.Context, auth websocketAuth, envelope signaling.Envelope) error {
	if envelope.FromAgentID == "" || envelope.FromAgentID != auth.AgentID {
		return signaling.ErrAgentIDRequired
	}
	if envelope.ToAgentID == "" {
		return signaling.ErrRouteTargetRequired
	}
	if !auth.AgentScoped {
		if envelope.TransferID != auth.TransferID {
			return sessions.ErrSessionNotFound
		}
		if !auth.allowsTarget(envelope.ToAgentID) {
			return signaling.ErrRouteTargetRequired
		}
		transfer, err := r.service.VerifyTransferUsable(ctx, envelope.TransferID)
		if err != nil {
			return err
		}
		if transfer.Target == sessions.TargetAgent && auth.AgentID == transfer.FromAgentID && envelope.ToAgentID == transfer.ToAgentID {
			return authorizeTransferRole(envelope.Type, true)
		}
		return nil
	}
	transfer, err := r.service.VerifyTransferUsable(ctx, envelope.TransferID)
	if err != nil {
		return err
	}
	if transfer.Target != sessions.TargetAgent {
		return signaling.ErrRouteTargetRequired
	}
	if auth.AgentID == transfer.FromAgentID && envelope.ToAgentID == transfer.ToAgentID {
		return authorizeTransferRole(envelope.Type, true)
	}
	if auth.AgentID == transfer.ToAgentID && envelope.ToAgentID == transfer.FromAgentID {
		return authorizeTransferRole(envelope.Type, false)
	}
	return signaling.ErrRouteTargetRequired
}

func authorizeTransferRole(messageType signaling.MessageType, fromSender bool) error {
	switch messageType {
	case signaling.MessageTransferOffer, signaling.MessageWebRTCOffer:
		if fromSender {
			return nil
		}
	case signaling.MessageTransferAccepted, signaling.MessageTransferDenied, signaling.MessageWebRTCAnswer:
		if !fromSender {
			return nil
		}
	case signaling.MessageWebRTCICE, signaling.MessageTransferStarted, signaling.MessageTransferProgress, signaling.MessageTransferInterrupted, signaling.MessageTransferRetryable, signaling.MessageTransferFailed, signaling.MessageTransferCancelled, signaling.MessageTransferExpired:
		return nil
	case signaling.MessageTransferCompleted:
		if !fromSender {
			return nil
		}
	}
	return signaling.ErrRouteTargetRequired
}

func errorEnvelope(message string) signaling.Envelope {
	payload, _ := json.Marshal(map[string]string{"error": message})
	return signaling.Envelope{Type: signaling.MessageError, Payload: payload}
}

func (r *Router) readHello(conn *websocket.Conn, auth websocketAuth) (string, string, bool) {
	var hello signaling.Envelope
	if err := conn.ReadJSON(&hello); err != nil {
		return "", "", false
	}
	if auth.Browser {
		hello.AgentID = auth.AgentID
	}
	if err := hello.Validate(); err != nil || hello.Type != signaling.MessageAgentHello || !auth.allowsAgent(hello.AgentID) {
		_ = conn.WriteJSON(errorEnvelope("invalid hello"))
		return "", "", false
	}
	return hello.AgentID, hello.DeviceID, true
}

func (auth websocketAuth) allowsAgent(agentID string) bool {
	if auth.AgentID != "" {
		return agentID == auth.AgentID
	}
	_, ok := auth.AllowedAgents[agentID]
	return ok
}

func (auth websocketAuth) allowsTarget(agentID string) bool {
	_, ok := auth.AllowedTargets[agentID]
	return ok
}
