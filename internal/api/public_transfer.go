package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

type publicTransferResponse struct {
	TransferID       string `json:"transfer_id"`
	Status           string `json:"status"`
	FromAgentID      string `json:"from_agent_id"`
	FileName         string `json:"file_name"`
	FileSizeBytes    int64  `json:"file_size_bytes"`
	MimeType         string `json:"mime_type,omitempty"`
	FileSHA256       string `json:"file_sha256,omitempty"`
	ExpiresAt        string `json:"expires_at"`
	PasswordRequired bool   `json:"password_required"`
}

type receiverTicketRequest struct {
	Consent  bool   `json:"consent"`
	Password string `json:"password,omitempty"`
}

type receiverTicketResponse struct {
	TransferID      string `json:"transfer_id"`
	ReceiverTicket  string `json:"receiver_ticket"`
	ExpiresAt       string `json:"expires_at"`
	WebSocketURL    string `json:"websocket_url"`
	BrowserAgentID  string `json:"browser_agent_id"`
	BrowserDeviceID string `json:"browser_device_id,omitempty"`
}

func (r *Router) handlePublicTransfer(w http.ResponseWriter, req *http.Request) {
	publicToken, action, ok := publicTransferPath(req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}

	switch {
	case action == "" && req.Method == http.MethodGet:
		r.handlePublicTransferMetadata(w, req, publicToken)
	case action == "receiver-ticket" && req.Method == http.MethodPost:
		r.handleReceiverTicket(w, req, publicToken)
	case action == "":
		w.WriteHeader(http.StatusMethodNotAllowed)
	case action == "receiver-ticket":
		w.WriteHeader(http.StatusMethodNotAllowed)
	default:
		http.NotFound(w, req)
	}
}

func (r *Router) handlePublicTransferMetadata(w http.ResponseWriter, req *http.Request, publicToken string) {
	transfer, err := r.service.VerifyPublicToken(req.Context(), publicToken)
	if err != nil {
		writeError(w, statusForSessionError(err), "transfer not found")
		return
	}
	if transfer.Target != sessions.TargetBrowserLink {
		writeError(w, http.StatusNotFound, "transfer not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, newPublicTransferResponse(transfer))
}

func (r *Router) handleReceiverTicket(w http.ResponseWriter, req *http.Request, publicToken string) {
	var payload receiverTicketRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	issued, err := r.service.IssueBrowserReceiverTicket(req.Context(), publicToken, sessions.ReceiverConsent{Accepted: payload.Consent, Password: payload.Password})
	if err != nil {
		writeError(w, statusForReceiverTicketError(err), err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, receiverTicketResponse{
		TransferID:     issued.Transfer.ID,
		ReceiverTicket: issued.ReceiverTicket,
		ExpiresAt:      issued.ExpiresAt.Format(time.RFC3339),
		WebSocketURL:   "/api/public/transfers/{public_token}/receiver/ws",
		BrowserAgentID: browserRecipientAgentID(issued.Transfer.ID),
	})
}

func newPublicTransferResponse(transfer sessions.TransferSession) publicTransferResponse {
	return publicTransferResponse{
		TransferID:       transfer.ID,
		Status:           transfer.Status,
		FromAgentID:      transfer.FromAgentID,
		FileName:         transfer.FileName,
		FileSizeBytes:    transfer.FileSizeBytes,
		MimeType:         transfer.MimeType,
		FileSHA256:       transfer.FileSHA256,
		ExpiresAt:        transfer.ExpiresAt.Format(time.RFC3339),
		PasswordRequired: transfer.PasswordHash != nil,
	}
}

func publicTransferPath(path string) (publicToken string, action string, ok bool) {
	const prefix = "/api/public/transfers/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" || strings.HasPrefix(rest, "/") || strings.HasSuffix(rest, "/") {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		return parts[0], "", parts[0] != ""
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func statusForReceiverTicketError(err error) int {
	switch {
	case errors.Is(err, sessions.ErrReceiverConsentRequired):
		return http.StatusBadRequest
	case errors.Is(err, sessions.ErrReceiverPasswordRequired), errors.Is(err, sessions.ErrReceiverPasswordInvalid):
		return http.StatusForbidden
	case errors.Is(err, sessions.ErrSessionNotFound):
		return http.StatusNotFound
	default:
		return statusForSessionError(err)
	}
}
