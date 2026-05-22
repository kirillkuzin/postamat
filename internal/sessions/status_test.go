package sessions_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestTransferSessionStatusTransitionsFollowCanonicalLifecycle(t *testing.T) {
	transfer := mustNewTransferIntent(t)

	if err := transfer.MarkOffered(); err != nil {
		t.Fatalf("MarkOffered returned error: %v", err)
	}
	if transfer.Status != sessions.StatusOffered {
		t.Fatalf("Status after offered = %q, want %q", transfer.Status, sessions.StatusOffered)
	}

	if err := transfer.MarkAccepted(); err != nil {
		t.Fatalf("MarkAccepted returned error: %v", err)
	}
	if transfer.Status != sessions.StatusAccepted {
		t.Fatalf("Status after accepted = %q, want %q", transfer.Status, sessions.StatusAccepted)
	}

	if err := transfer.MarkConnecting(); err != nil {
		t.Fatalf("MarkConnecting returned error: %v", err)
	}
	if transfer.Status != sessions.StatusConnecting {
		t.Fatalf("Status after connecting = %q, want %q", transfer.Status, sessions.StatusConnecting)
	}

	if err := transfer.MarkTransferStarted(); err != nil {
		t.Fatalf("MarkTransferStarted returned error: %v", err)
	}
	if transfer.Status != sessions.StatusTransferring {
		t.Fatalf("Status after transfer started = %q, want %q", transfer.Status, sessions.StatusTransferring)
	}

	completedAt := time.Date(2026, 5, 19, 12, 30, 0, 0, time.UTC)
	if err := transfer.MarkCompleted(completedAt); err != nil {
		t.Fatalf("MarkCompleted returned error: %v", err)
	}
	if transfer.Status != sessions.StatusCompleted {
		t.Fatalf("Status after completed = %q, want %q", transfer.Status, sessions.StatusCompleted)
	}
	if transfer.DownloadCount != 1 {
		t.Fatalf("DownloadCount = %d, want 1", transfer.DownloadCount)
	}
	if transfer.CompletedAt == nil || !transfer.CompletedAt.Equal(completedAt) {
		t.Fatalf("CompletedAt = %v, want %v", transfer.CompletedAt, completedAt)
	}
}

func TestTransferSessionRejectsInvalidStatusTransition(t *testing.T) {
	transfer := mustNewTransferIntent(t)

	err := transfer.MarkConnecting()
	if !errors.Is(err, sessions.ErrInvalidStatusTransition) {
		t.Fatalf("MarkConnecting error = %v, want %v", err, sessions.ErrInvalidStatusTransition)
	}
	if transfer.Status != sessions.StatusCreated {
		t.Fatalf("Status changed to %q, want %q", transfer.Status, sessions.StatusCreated)
	}
}

func TestTransferSessionCanFailFromConnectingOrTransferring(t *testing.T) {
	for _, start := range []struct {
		name string
		move func(*sessions.TransferSession) error
	}{
		{
			name: "connecting",
			move: func(transfer *sessions.TransferSession) error {
				if err := transfer.MarkOffered(); err != nil {
					return err
				}
				if err := transfer.MarkAccepted(); err != nil {
					return err
				}
				return transfer.MarkConnecting()
			},
		},
		{
			name: "transferring",
			move: func(transfer *sessions.TransferSession) error {
				if err := transfer.MarkOffered(); err != nil {
					return err
				}
				if err := transfer.MarkAccepted(); err != nil {
					return err
				}
				if err := transfer.MarkConnecting(); err != nil {
					return err
				}
				return transfer.MarkTransferStarted()
			},
		},
	} {
		t.Run(start.name, func(t *testing.T) {
			transfer := mustNewTransferIntent(t)
			if err := start.move(&transfer); err != nil {
				t.Fatalf("move to %s returned error: %v", start.name, err)
			}
			failedAt := time.Date(2026, 5, 19, 12, 20, 0, 0, time.UTC)

			if err := transfer.MarkFailed("agentd disconnected", failedAt); err != nil {
				t.Fatalf("MarkFailed returned error: %v", err)
			}
			if transfer.Status != sessions.StatusFailed {
				t.Fatalf("Status = %q, want %q", transfer.Status, sessions.StatusFailed)
			}
			if transfer.FailureReason != "agentd disconnected" {
				t.Fatalf("FailureReason = %q, want agentd disconnected", transfer.FailureReason)
			}
			if transfer.FailedAt == nil || !transfer.FailedAt.Equal(failedAt) {
				t.Fatalf("FailedAt = %v, want %v", transfer.FailedAt, failedAt)
			}
		})
	}
}

func TestTransferSessionInterruptedCanBecomeRetryableAndReconnect(t *testing.T) {
	transfer := mustNewTransferIntent(t)
	if err := transfer.MarkOffered(); err != nil {
		t.Fatalf("MarkOffered returned error: %v", err)
	}
	if err := transfer.MarkAccepted(); err != nil {
		t.Fatalf("MarkAccepted returned error: %v", err)
	}
	if err := transfer.MarkConnecting(); err != nil {
		t.Fatalf("MarkConnecting returned error: %v", err)
	}
	if err := transfer.MarkTransferStarted(); err != nil {
		t.Fatalf("MarkTransferStarted returned error: %v", err)
	}

	interruptedAt := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	if err := transfer.MarkInterrupted("network dropped", interruptedAt); err != nil {
		t.Fatalf("MarkInterrupted returned error: %v", err)
	}
	if transfer.Status != sessions.StatusInterrupted || transfer.InterruptedAt == nil || transfer.FailureReason != "network dropped" || transfer.IsTerminal() {
		t.Fatalf("interrupted transfer state = %+v", transfer)
	}

	if err := transfer.MarkRetryable(); err != nil {
		t.Fatalf("MarkRetryable returned error: %v", err)
	}
	if transfer.Status != sessions.StatusRetryable || transfer.IsTerminal() {
		t.Fatalf("retryable transfer state = %+v", transfer)
	}
	if err := transfer.MarkConnecting(); err != nil {
		t.Fatalf("retryable transfer did not reconnect: %v", err)
	}
	if transfer.Status != sessions.StatusConnecting {
		t.Fatalf("status after retry connect = %q, want %q", transfer.Status, sessions.StatusConnecting)
	}
}

func TestTransferSessionRejectsInvalidInterruptedRetryableTransitions(t *testing.T) {
	transfer := mustNewTransferIntent(t)
	if err := transfer.MarkInterrupted("", time.Now()); !errors.Is(err, sessions.ErrFailureReasonRequired) {
		t.Fatalf("MarkInterrupted empty reason error = %v, want ErrFailureReasonRequired", err)
	}
	if err := transfer.MarkInterrupted("too early", time.Now()); !errors.Is(err, sessions.ErrInvalidStatusTransition) {
		t.Fatalf("MarkInterrupted created transfer error = %v, want ErrInvalidStatusTransition", err)
	}
	if err := transfer.MarkRetryable(); !errors.Is(err, sessions.ErrInvalidStatusTransition) {
		t.Fatalf("MarkRetryable created transfer error = %v, want ErrInvalidStatusTransition", err)
	}
	if err := transfer.Cancel(time.Now()); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if err := transfer.MarkInterrupted("terminal", time.Now()); !errors.Is(err, sessions.ErrTerminalSession) {
		t.Fatalf("MarkInterrupted terminal transfer error = %v, want ErrTerminalSession", err)
	}
}

func TestTransferSessionCancelAndExpireMoveNonTerminalSessionToTerminalState(t *testing.T) {
	cancelled := mustNewTransferIntent(t)
	cancelledAt := time.Date(2026, 5, 19, 12, 10, 0, 0, time.UTC)
	if err := cancelled.Cancel(cancelledAt); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if cancelled.Status != sessions.StatusCancelled {
		t.Fatalf("cancelled status = %q, want %q", cancelled.Status, sessions.StatusCancelled)
	}
	if cancelled.CancelledAt == nil || !cancelled.CancelledAt.Equal(cancelledAt) {
		t.Fatalf("CancelledAt = %v, want %v", cancelled.CancelledAt, cancelledAt)
	}

	expired := mustNewTransferIntent(t)
	expiredAt := time.Date(2026, 5, 19, 12, 31, 0, 0, time.UTC)
	if err := expired.Expire(expiredAt); err != nil {
		t.Fatalf("Expire returned error: %v", err)
	}
	if expired.Status != sessions.StatusExpired {
		t.Fatalf("expired status = %q, want %q", expired.Status, sessions.StatusExpired)
	}
	if expired.ExpiredAt == nil || !expired.ExpiredAt.Equal(expiredAt) {
		t.Fatalf("ExpiredAt = %v, want %v", expired.ExpiredAt, expiredAt)
	}
}

func TestTransferSessionTerminalSessionsCannotTransition(t *testing.T) {
	transfer := mustNewTransferIntent(t)
	if err := transfer.MarkFailed("agentd disconnected", time.Now()); err != nil {
		t.Fatalf("MarkFailed returned error: %v", err)
	}

	err := transfer.MarkOffered()
	if !errors.Is(err, sessions.ErrTerminalSession) {
		t.Fatalf("MarkOffered error = %v, want %v", err, sessions.ErrTerminalSession)
	}
}

func mustNewTransferIntent(t *testing.T) sessions.TransferSession {
	t.Helper()

	transfer, err := sessions.NewTransferIntent(validTransferInput(nil))
	if err != nil {
		t.Fatalf("NewTransferIntent returned error: %v", err)
	}
	return transfer
}
