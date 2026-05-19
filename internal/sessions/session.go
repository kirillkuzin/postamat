package sessions

import (
	"errors"
	"time"
)

const (
	TransportWebRTCP2P = "webrtc_p2p"

	StatusWaitingSender = "waiting_sender"
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
		return TransferSession{}, errors.New("owner agent id is required")
	}
	if input.FileName == "" {
		return TransferSession{}, errors.New("file name is required")
	}
	if input.FileSizeBytes < 0 {
		return TransferSession{}, errors.New("file size must be non-negative")
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ttl := input.TTL
	if ttl == 0 {
		ttl = defaultP2PTTL
	}

	maxDownloads := input.MaxDownloads
	if maxDownloads == 0 {
		maxDownloads = 1
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
