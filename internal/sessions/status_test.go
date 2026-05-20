package sessions_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestTransferSessionStatusTransitionsFollowP2PLifecycle(t *testing.T) {
	session := mustNewP2PShare(t)

	if err := session.MarkSenderReady(); err != nil {
		t.Fatalf("MarkSenderReady returned error: %v", err)
	}
	if session.Status != sessions.StatusWaitingReceiver {
		t.Fatalf("Status after sender ready = %q, want %q", session.Status, sessions.StatusWaitingReceiver)
	}

	if err := session.MarkSignalingStarted(); err != nil {
		t.Fatalf("MarkSignalingStarted returned error: %v", err)
	}
	if session.Status != sessions.StatusSignaling {
		t.Fatalf("Status after signaling started = %q, want %q", session.Status, sessions.StatusSignaling)
	}

	if err := session.MarkConnected(); err != nil {
		t.Fatalf("MarkConnected returned error: %v", err)
	}
	if session.Status != sessions.StatusConnected {
		t.Fatalf("Status after connected = %q, want %q", session.Status, sessions.StatusConnected)
	}

	if err := session.MarkTransferStarted(); err != nil {
		t.Fatalf("MarkTransferStarted returned error: %v", err)
	}
	if session.Status != sessions.StatusTransferring {
		t.Fatalf("Status after transfer started = %q, want %q", session.Status, sessions.StatusTransferring)
	}

	completedAt := time.Date(2026, 5, 19, 12, 30, 0, 0, time.UTC)
	if err := session.MarkCompleted(completedAt); err != nil {
		t.Fatalf("MarkCompleted returned error: %v", err)
	}
	if session.Status != sessions.StatusCompleted {
		t.Fatalf("Status after completed = %q, want %q", session.Status, sessions.StatusCompleted)
	}
	if session.DownloadCount != 1 {
		t.Fatalf("DownloadCount = %d, want 1", session.DownloadCount)
	}
	if session.CompletedAt == nil || !session.CompletedAt.Equal(completedAt) {
		t.Fatalf("CompletedAt = %v, want %v", session.CompletedAt, completedAt)
	}
}

func TestTransferSessionRejectsInvalidStatusTransition(t *testing.T) {
	session := mustNewP2PShare(t)

	err := session.MarkConnected()
	if !errors.Is(err, sessions.ErrInvalidStatusTransition) {
		t.Fatalf("MarkConnected error = %v, want %v", err, sessions.ErrInvalidStatusTransition)
	}
	if session.Status != sessions.StatusWaitingSender {
		t.Fatalf("Status changed to %q, want %q", session.Status, sessions.StatusWaitingSender)
	}
}

func TestTransferSessionCancelMovesNonTerminalSessionToCancelled(t *testing.T) {
	session := mustNewP2PShare(t)
	cancelledAt := time.Date(2026, 5, 19, 12, 10, 0, 0, time.UTC)

	if err := session.Cancel(cancelledAt); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if session.Status != sessions.StatusCancelled {
		t.Fatalf("Status = %q, want %q", session.Status, sessions.StatusCancelled)
	}
	if session.CancelledAt == nil || !session.CancelledAt.Equal(cancelledAt) {
		t.Fatalf("CancelledAt = %v, want %v", session.CancelledAt, cancelledAt)
	}
}

func TestTransferSessionTerminalSessionsCannotTransition(t *testing.T) {
	session := mustNewP2PShare(t)
	if err := session.Cancel(time.Now()); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	err := session.MarkSenderReady()
	if !errors.Is(err, sessions.ErrTerminalSession) {
		t.Fatalf("MarkSenderReady error = %v, want %v", err, sessions.ErrTerminalSession)
	}
}

func mustNewP2PShare(t *testing.T) sessions.TransferSession {
	t.Helper()

	session, err := sessions.NewP2PShare(sessions.CreateP2PShareInput{
		OwnerAgentID:  "agent_1",
		FileName:      "report.pdf",
		FileSizeBytes: 42,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewP2PShare returned error: %v", err)
	}
	return session
}
