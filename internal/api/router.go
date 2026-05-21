package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/sessions"
	"github.com/kirillkuzin/postamat/internal/signaling"
	recipient "github.com/kirillkuzin/postamat/web/recipient"
)

type Router struct {
	service         *sessions.Service
	presence        *signaling.PresenceRegistry
	rooms           *signaling.RoomManager
	recipientAssets fs.FS
	upgrader        websocket.Upgrader
}

func NewRouter(service *sessions.Service) http.Handler {
	presence := signaling.NewPresenceRegistry(nil)
	return NewRouterWithSignaling(service, presence, signaling.NewRoomManager(presence, nil))
}

func NewRouterWithSignaling(service *sessions.Service, presence *signaling.PresenceRegistry, rooms *signaling.RoomManager) http.Handler {
	if presence == nil {
		presence = signaling.NewPresenceRegistry(nil)
	}
	if rooms == nil {
		rooms = signaling.NewRoomManager(presence, nil)
	}
	return &Router{service: service, presence: presence, rooms: rooms, recipientAssets: recipient.DistFS(), upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}}
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.URL.Path == "/api/v1/agent/ws":
		r.handleAgentWebSocket(w, req)
	case strings.HasPrefix(req.URL.Path, "/api/public/transfers/") && strings.HasSuffix(req.URL.Path, "/receiver/ws"):
		r.handleBrowserReceiverWebSocket(w, req)
	case strings.HasPrefix(req.URL.Path, "/api/public/transfers/"):
		r.handlePublicTransfer(w, req)
	case strings.HasPrefix(req.URL.Path, "/p/"):
		r.handleRecipientApp(w, req)
	case strings.HasPrefix(req.URL.Path, "/assets/"):
		r.handleRecipientAsset(w, req)
	case req.URL.Path == "/healthz":
		r.handleHealthz(w, req)
	case req.URL.Path == "/metrics":
		r.handleMetrics(w, req)
	case req.URL.Path == "/api/v1/transfers":
		r.handleTransfers(w, req)
	case strings.HasPrefix(req.URL.Path, "/api/v1/transfers/"):
		r.handleTransfer(w, req)
	case req.URL.Path == "/api/v1/agents":
		r.handleAgents(w, req)
	case strings.HasPrefix(req.URL.Path, "/api/v1/agents/"):
		r.handleAgent(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (r *Router) handleHealthz(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("# HELP postamat_build_info Static build information for the postamat service.\n# TYPE postamat_build_info gauge\npostamat_build_info{service=\"postamat\"} 1\n"))
}

func (r *Router) handleTransfers(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.listTransfers(w, req)
	case http.MethodPost:
		r.createTransfer(w, req)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *Router) handleTransfer(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimPrefix(req.URL.Path, "/api/v1/transfers/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, req)
		return
	}

	switch req.Method {
	case http.MethodGet:
		transfer, err := r.service.Get(req.Context(), id)
		if err != nil {
			writeError(w, statusForSessionError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, newTransferResponse(transfer, "", ""))
	case http.MethodDelete:
		transfer, err := r.service.Cancel(req.Context(), id)
		if err != nil {
			writeError(w, statusForSessionError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, newTransferResponse(transfer, "", ""))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *Router) handleAgents(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if status := req.URL.Query().Get("status"); status != "" && status != "online" {
		writeError(w, http.StatusBadRequest, "unsupported status filter")
		return
	}
	agents := r.presence.OnlineAgents()
	response := struct {
		Agents []agentPresenceResponse `json:"agents"`
	}{Agents: make([]agentPresenceResponse, 0, len(agents))}
	for _, agent := range agents {
		response.Agents = append(response.Agents, newAgentPresenceResponse(agent))
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) handleAgent(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	agentID := strings.TrimPrefix(req.URL.Path, "/api/v1/agents/")
	if agentID == "" || strings.Contains(agentID, "/") {
		http.NotFound(w, req)
		return
	}
	presence, ok := r.presence.Agent(agentID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]string{"agent_id": agentID, "status": "offline"})
		return
	}
	writeJSON(w, http.StatusOK, newAgentPresenceResponse(presence))
}

type agentPresenceResponse struct {
	AgentID  string `json:"agent_id"`
	Status   string `json:"status"`
	Devices  int    `json:"devices"`
	LastSeen string `json:"last_seen,omitempty"`
}

func newAgentPresenceResponse(presence signaling.AgentPresence) agentPresenceResponse {
	status := "offline"
	if presence.Online {
		status = "online"
	}
	return agentPresenceResponse{AgentID: presence.AgentID, Status: status, Devices: len(presence.Devices), LastSeen: presence.LastSeen.Format(time.RFC3339)}
}

func (r *Router) listTransfers(w http.ResponseWriter, req *http.Request) {
	status := req.URL.Query().Get("status")
	if status != "" && status != "active" {
		writeError(w, http.StatusBadRequest, "unsupported status filter")
		return
	}

	transfers, err := r.service.ListActive(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := struct {
		Transfers []transferResponse `json:"transfers"`
	}{
		Transfers: make([]transferResponse, 0, len(transfers)),
	}
	for _, transfer := range transfers {
		response.Transfers = append(response.Transfers, newTransferResponse(transfer, "", ""))
	}
	writeJSON(w, http.StatusOK, response)
}

type createTransferRequest struct {
	FromAgentID   string `json:"from_agent_id"`
	ToAgentID     string `json:"to_agent_id"`
	Target        string `json:"target"`
	FileName      string `json:"file_name"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	TTLSeconds    int64  `json:"ttl_seconds"`
	MaxDownloads  int    `json:"max_downloads"`
}

type transferResponse struct {
	TransferID    string `json:"transfer_id"`
	Status        string `json:"status"`
	Target        string `json:"target"`
	Transport     string `json:"transport"`
	FromAgentID   string `json:"from_agent_id"`
	ToAgentID     string `json:"to_agent_id,omitempty"`
	FileName      string `json:"file_name"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	ExpiresAt     string `json:"expires_at"`
	PublicToken   string `json:"public_token,omitempty"`
	AgentTicket   string `json:"agent_ticket,omitempty"`
}

func (r *Router) createTransfer(w http.ResponseWriter, req *http.Request) {
	var payload createTransferRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	input := sessions.CreateTransferInput{
		FromAgentID:   payload.FromAgentID,
		ToAgentID:     payload.ToAgentID,
		Target:        payload.Target,
		FileName:      payload.FileName,
		FileSizeBytes: payload.FileSizeBytes,
		MaxDownloads:  payload.MaxDownloads,
	}
	if payload.TTLSeconds > 0 {
		input.TTL = time.Duration(payload.TTLSeconds) * time.Second
	}

	created, err := r.service.CreateTransfer(req.Context(), input)
	if err != nil {
		writeError(w, statusForSessionError(err), err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, newTransferResponse(created.Transfer, created.PublicToken, created.AgentTicket))
}

func newTransferResponse(transfer sessions.TransferSession, publicToken string, agentTicket string) transferResponse {
	return transferResponse{
		TransferID:    transfer.ID,
		Status:        transfer.Status,
		Target:        transfer.Target,
		Transport:     transfer.Transport,
		FromAgentID:   transfer.FromAgentID,
		ToAgentID:     transfer.ToAgentID,
		FileName:      transfer.FileName,
		FileSizeBytes: transfer.FileSizeBytes,
		ExpiresAt:     transfer.ExpiresAt.Format(time.RFC3339),
		PublicToken:   publicToken,
		AgentTicket:   agentTicket,
	}
}

func statusForSessionError(err error) int {
	switch {
	case errors.Is(err, sessions.ErrSessionNotFound):
		return http.StatusNotFound
	case errors.Is(err, sessions.ErrFromAgentRequired),
		errors.Is(err, sessions.ErrTargetRequired),
		errors.Is(err, sessions.ErrUnsupportedTarget),
		errors.Is(err, sessions.ErrTargetAgentRequired),
		errors.Is(err, sessions.ErrFileNameRequired),
		errors.Is(err, sessions.ErrFileSizeNegative),
		errors.Is(err, sessions.ErrTTLNotPositive),
		errors.Is(err, sessions.ErrMaxDownloadsNotPositive),
		errors.Is(err, sessions.ErrInvalidStatusTransition),
		errors.Is(err, sessions.ErrTerminalSession):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
