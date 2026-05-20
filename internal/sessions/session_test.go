package sessions_test

import (
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestNewP2PShareAppliesSafeDefaults(t *testing.T) {
	share, err := sessions.NewP2PShare(sessions.CreateP2PShareInput{
		OwnerAgentID:  "agent_1",
		FileName:      "report.pdf",
		FileSizeBytes: 42,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	})

	if err != nil {
		t.Fatalf("NewP2PShare returned error: %v", err)
	}
	if share.Transport != sessions.TransportWebRTCP2P {
		t.Fatalf("Transport = %q, want %q", share.Transport, sessions.TransportWebRTCP2P)
	}
	if share.Status != sessions.StatusWaitingSender {
		t.Fatalf("Status = %q, want %q", share.Status, sessions.StatusWaitingSender)
	}
	if share.MaxDownloads != 1 {
		t.Fatalf("MaxDownloads = %d, want 1", share.MaxDownloads)
	}
	if got, want := share.ExpiresAt.Sub(share.CreatedAt), 30*time.Minute; got != want {
		t.Fatalf("TTL = %s, want %s", got, want)
	}
}

func TestNewP2PShareRejectsInvalidFileMetadata(t *testing.T) {
	_, err := sessions.NewP2PShare(sessions.CreateP2PShareInput{
		OwnerAgentID:  "agent_1",
		FileName:      "",
		FileSizeBytes: 42,
		Now:           time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for empty file name")
	}

	_, err = sessions.NewP2PShare(sessions.CreateP2PShareInput{
		OwnerAgentID:  "agent_1",
		FileName:      "report.pdf",
		FileSizeBytes: -1,
		Now:           time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for negative file size")
	}
}
