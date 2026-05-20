package sessions

import (
	"errors"
	"fmt"
	"time"
)

const (
	TransportWebRTCP2P = "webrtc_p2p"

	TargetAgent       = "agent"
	TargetBrowserLink = "browser_link"

	StatusCreated      = "created"
	StatusOffered      = "offered"
	StatusAccepted     = "accepted"
	StatusConnecting   = "connecting"
	StatusTransferring = "transferring"
	StatusCompleted    = "completed"
	StatusFailed       = "failed"
	StatusCancelled    = "cancelled"
	StatusExpired      = "expired"
)

var (
	ErrFromAgentRequired       = errors.New("from agent id is required")
	ErrTargetRequired          = errors.New("target is required")
	ErrUnsupportedTarget       = errors.New("unsupported transfer target")
	ErrTargetAgentRequired     = errors.New("target agent id is required")
	ErrFileNameRequired        = errors.New("file name is required")
	ErrFileSizeNegative        = errors.New("file size must be non-negative")
	ErrTTLNotPositive          = errors.New("ttl must be positive")
	ErrMaxDownloadsNotPositive = errors.New("max downloads must be positive")
	ErrFailureReasonRequired   = errors.New("failure reason is required")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrTerminalSession         = errors.New("terminal session cannot transition")
	ErrSessionNotFound         = errors.New("session not found")
)

const defaultTransferTTL = 30 * time.Minute

type CreateTransferInput struct {
	FromAgentID   string
	ToAgentID     string
	Target        string
	FileName      string
	FileSizeBytes int64
	Now           time.Time
	TTL           time.Duration
	MaxDownloads  int
}

type TransferSession struct {
	ID              string
	Transport       string
	Target          string
	Status          string
	FromAgentID     string
	ToAgentID       string
	PublicTokenHash string
	AgentTicketHash string

	FileName      string
	FileSizeBytes int64
	FileSHA256    string
	MimeType      string

	ExpiresAt     time.Time
	MaxDownloads  int
	DownloadCount int
	PasswordHash  *string

	CreatedAt     time.Time
	CompletedAt   *time.Time
	FailedAt      *time.Time
	CancelledAt   *time.Time
	ExpiredAt     *time.Time
	FailureReason string
}

func NewTransferIntent(input CreateTransferInput) (TransferSession, error) {
	if input.FromAgentID == "" {
		return TransferSession{}, ErrFromAgentRequired
	}
	if input.Target == "" {
		return TransferSession{}, ErrTargetRequired
	}
	switch input.Target {
	case TargetAgent:
		if input.ToAgentID == "" {
			return TransferSession{}, ErrTargetAgentRequired
		}
	case TargetBrowserLink:
		// Browser-link recipients are addressed by public token, not agent id.
		input.ToAgentID = ""
	default:
		return TransferSession{}, ErrUnsupportedTarget
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
		ttl = defaultTransferTTL
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
		Target:        input.Target,
		Status:        StatusCreated,
		FromAgentID:   input.FromAgentID,
		ToAgentID:     input.ToAgentID,
		FileName:      input.FileName,
		FileSizeBytes: input.FileSizeBytes,
		CreatedAt:     now,
		ExpiresAt:     now.Add(ttl),
		MaxDownloads:  maxDownloads,
	}, nil
}

func (s *TransferSession) MarkOffered() error {
	return s.transition(StatusCreated, StatusOffered)
}

func (s *TransferSession) MarkAccepted() error {
	return s.transition(StatusOffered, StatusAccepted)
}

func (s *TransferSession) MarkConnecting() error {
	return s.transition(StatusAccepted, StatusConnecting)
}

func (s *TransferSession) MarkTransferStarted() error {
	return s.transition(StatusConnecting, StatusTransferring)
}

func (s *TransferSession) MarkCompleted(at time.Time) error {
	if err := s.transition(StatusTransferring, StatusCompleted); err != nil {
		return err
	}
	s.DownloadCount++
	s.CompletedAt = cloneTime(at)
	return nil
}

func (s *TransferSession) MarkFailed(reason string, at time.Time) error {
	if s.IsTerminal() {
		return ErrTerminalSession
	}
	if reason == "" {
		return ErrFailureReasonRequired
	}
	s.Status = StatusFailed
	s.FailureReason = reason
	s.FailedAt = cloneTime(at)
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

func (s *TransferSession) Expire(at time.Time) error {
	if s.IsTerminal() {
		return ErrTerminalSession
	}
	s.Status = StatusExpired
	s.ExpiredAt = cloneTime(at)
	return nil
}

func (s TransferSession) IsTerminal() bool {
	return s.Status == StatusCompleted || s.Status == StatusFailed || s.Status == StatusCancelled || s.Status == StatusExpired
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
	if session.FailedAt != nil {
		failedAt := *session.FailedAt
		session.FailedAt = &failedAt
	}
	if session.CancelledAt != nil {
		cancelledAt := *session.CancelledAt
		session.CancelledAt = &cancelledAt
	}
	if session.ExpiredAt != nil {
		expiredAt := *session.ExpiredAt
		session.ExpiredAt = &expiredAt
	}
	return session
}

func cloneTime(at time.Time) *time.Time {
	cloned := at
	return &cloned
}
