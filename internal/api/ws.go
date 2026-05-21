package api

import (
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
	allowedAgents := map[string]struct{}{transfer.FromAgentID: {}}
	allowedTargets := map[string]struct{}{transfer.FromAgentID: {}}
	if transfer.ToAgentID != "" {
		allowedAgents[transfer.ToAgentID] = struct{}{}
		allowedTargets[transfer.ToAgentID] = struct{}{}
	} else {
		allowedTargets[browserRecipientAgentID(transfer.ID)] = struct{}{}
	}
	r.handleSignalingWebSocket(w, req, websocketAuth{AllowedAgents: allowedAgents, AllowedTargets: allowedTargets, TransferID: transfer.ID})
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
	r.rooms.AttachTransferPeer(agentID, deviceID, auth.TransferID, peer)
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
		if envelope.TransferID == auth.TransferID {
			if _, err := r.service.VerifyTransferUsable(req.Context(), auth.TransferID); err != nil {
				_ = peer.Send(errorEnvelope("transfer is no longer active"))
				return
			}
		}
		if err := authorizeOutboundEnvelope(auth, envelope); err != nil {
			_ = peer.Send(errorEnvelope(err.Error()))
			continue
		}
		if err := r.rooms.Route(envelope); err != nil {
			_ = peer.Send(errorEnvelope(err.Error()))
		}
	}
}

func authorizeOutboundEnvelope(auth websocketAuth, envelope signaling.Envelope) error {
	if envelope.TransferID != auth.TransferID {
		return sessions.ErrSessionNotFound
	}
	if envelope.FromAgentID == "" || envelope.FromAgentID != auth.AgentID {
		return signaling.ErrAgentIDRequired
	}
	if envelope.ToAgentID == "" || !auth.allowsTarget(envelope.ToAgentID) {
		return signaling.ErrRouteTargetRequired
	}
	return nil
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
