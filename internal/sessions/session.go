package sessions

import (
	"errors"
	"fmt"
	"time"
)

const (
	TransportWebRTCP2P = "webrtc_p2p"

	StatusWaitingSender   = "waiting_sender"
	StatusWaitingReceiver = "waiting_receiver"
	StatusSignaling       = "signaling"
	StatusConnected       = "connected"
	StatusTransferring    = "transferring"
	StatusCompleted       = "completed"
	StatusCancelled       = "cancelled"
)

var (
	ErrOwnerAgentRequired      = errors.New("owner agent id is required")
	ErrFileNameRequired        = errors.New("file name is required")
	ErrFileSizeNegative        = errors.New("file size must be non-negative")
	ErrTTLNotPositive          = errors.New("ttl must be positive")
	ErrMaxDownloadsNotPositive = errors.New("max downloads must be positive")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrTerminalSession         = errors.New("terminal session cannot transition")
	ErrSessionNotFound         = errors.New("session not found")
)

const defaultP2PTTL = 30 * time.Minute

type CreateP2PShareInput struct {
	OwnerAgentID  string
	FileName      string
	FileSizeBytes int64
	Now           time.Time
	TTL           time.Duration
	MaxDownloads  int
}

type TransferSession struct {
	ID               string
	Transport        string
	Status           string
	OwnerAgentID     string
	SenderAgentID    string
	PublicTokenHash  string
	SenderTicketHash string

	FileName      string
	FileSizeBytes int64
	FileSHA256    string
	MimeType      string

	ExpiresAt     time.Time
	MaxDownloads  int
	DownloadCount int
	PasswordHash  *string

	CreatedAt   time.Time
	CompletedAt *time.Time
	CancelledAt *time.Time
}

func NewP2PShare(input CreateP2PShareInput) (TransferSession, error) {
	if input.OwnerAgentID == "" {
		return TransferSession{}, ErrOwnerAgentRequired
	}
	if input.FileName == "" {
		return TransferSession{}, ErrFileNameRequired
	}
	if input.FileSizeBytes < 0 {
		return TransferSession{}, ErrFileSizeNegative
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ttl := input.TTL
	if ttl == 0 {
		ttl = defaultP2PTTL
	}
	if ttl < 0 {
		return TransferSession{}, ErrTTLNotPositive
	}

	maxDownloads := input.MaxDownloads
	if maxDownloads == 0 {
		maxDownloads = 1
	}
	if maxDownloads < 0 {
		return TransferSession{}, ErrMaxDownloadsNotPositive
	}

	return TransferSession{
		Transport:     TransportWebRTCP2P,
		Status:        StatusWaitingSender,
		OwnerAgentID:  input.OwnerAgentID,
		SenderAgentID: input.OwnerAgentID,
		FileName:      input.FileName,
		FileSizeBytes: input.FileSizeBytes,
		CreatedAt:     now,
		ExpiresAt:     now.Add(ttl),
		MaxDownloads:  maxDownloads,
	}, nil
}

func (s *TransferSession) MarkSenderReady() error {
	return s.transition(StatusWaitingSender, StatusWaitingReceiver)
}

func (s *TransferSession) MarkSignalingStarted() error {
	return s.transition(StatusWaitingReceiver, StatusSignaling)
}

func (s *TransferSession) MarkConnected() error {
	return s.transition(StatusSignaling, StatusConnected)
}

func (s *TransferSession) MarkTransferStarted() error {
	return s.transition(StatusConnected, StatusTransferring)
}

func (s *TransferSession) MarkCompleted(at time.Time) error {
	if err := s.transition(StatusTransferring, StatusCompleted); err != nil {
		return err
	}
	s.DownloadCount++
	s.CompletedAt = cloneTime(at)
	return nil
}

func (s *TransferSession) Cancel(at time.Time) error {
	if s.IsTerminal() {
		return ErrTerminalSession
	}
	s.Status = StatusCancelled
	s.CancelledAt = cloneTime(at)
	return nil
}

func (s TransferSession) IsTerminal() bool {
	return s.Status == StatusCompleted || s.Status == StatusCancelled
}

func (s *TransferSession) transition(from, to string) error {
	if s.IsTerminal() {
		return ErrTerminalSession
	}
	if s.Status != from {
		return fmt.Errorf("%w: %s to %s", ErrInvalidStatusTransition, s.Status, to)
	}
	s.Status = to
	return nil
}

func cloneSession(session TransferSession) TransferSession {
	if session.PasswordHash != nil {
		passwordHash := *session.PasswordHash
		session.PasswordHash = &passwordHash
	}
	if session.CompletedAt != nil {
		completedAt := *session.CompletedAt
		session.CompletedAt = &completedAt
	}
	if session.CancelledAt != nil {
		cancelledAt := *session.CancelledAt
		session.CancelledAt = &cancelledAt
	}
	return session
}

func cloneTime(at time.Time) *time.Time {
	cloned := at
	return &cloned
}
