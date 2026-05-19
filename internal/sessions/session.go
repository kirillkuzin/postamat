package sessions

import (
	"errors"
	"time"
)

const (
	TransportWebRTCP2P = "webrtc_p2p"

	StatusWaitingSender = "waiting_sender"
)

var (
	ErrOwnerAgentRequired      = errors.New("owner agent id is required")
	ErrFileNameRequired        = errors.New("file name is required")
	ErrFileSizeNegative        = errors.New("file size must be non-negative")
	ErrTTLNotPositive          = errors.New("ttl must be positive")
	ErrMaxDownloadsNotPositive = errors.New("max downloads must be positive")
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
	ID              string
	Transport       string
	Status          string
	OwnerAgentID    string
	SenderAgentID   string
	PublicTokenHash string

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
