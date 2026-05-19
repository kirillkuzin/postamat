package sessions_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestMemoryRepositorySaveGetListAndUpdate(t *testing.T) {
	repo := sessions.NewMemoryRepository()
	ctx := context.Background()
	session := mustNewP2PShare(t)
	session.ID = "share_1"

	if err := repo.Save(ctx, session); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := repo.Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != session.ID {
		t.Fatalf("Get ID = %q, want %q", got.ID, session.ID)
	}

	updated, err := repo.Update(ctx, session.ID, func(s *sessions.TransferSession) error {
		return s.MarkSenderReady()
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.Status != sessions.StatusWaitingReceiver {
		t.Fatalf("updated status = %q, want %q", updated.Status, sessions.StatusWaitingReceiver)
	}

	active, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if len(active) != 1 || active[0].ID != session.ID {
		t.Fatalf("active = %#v, want one session %q", active, session.ID)
	}
}

func TestMemoryRepositoryReturnsNotFoundForUnknownSession(t *testing.T) {
	repo := sessions.NewMemoryRepository()

	_, err := repo.Get(context.Background(), "missing")
	if !errors.Is(err, sessions.ErrSessionNotFound) {
		t.Fatalf("Get error = %v, want %v", err, sessions.ErrSessionNotFound)
	}

	_, err = repo.Update(context.Background(), "missing", func(*sessions.TransferSession) error { return nil })
	if !errors.Is(err, sessions.ErrSessionNotFound) {
		t.Fatalf("Update error = %v, want %v", err, sessions.ErrSessionNotFound)
	}
}

func TestMemoryRepositoryUsesDefensiveCopies(t *testing.T) {
	repo := sessions.NewMemoryRepository()
	ctx := context.Background()
	session := mustNewP2PShare(t)
	session.ID = "share_1"
	passwordHash := "stored_hash"
	session.PasswordHash = &passwordHash

	if err := repo.Save(ctx, session); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	passwordHash = "mutated_after_save"

	got, err := repo.Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.PasswordHash == nil || *got.PasswordHash != "stored_hash" {
		t.Fatalf("stored password hash = %v, want stored_hash", got.PasswordHash)
	}

	*got.PasswordHash = "mutated_after_get"
	again, err := repo.Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("Get again returned error: %v", err)
	}
	if again.PasswordHash == nil || *again.PasswordHash != "stored_hash" {
		t.Fatalf("stored password hash after external mutation = %v, want stored_hash", again.PasswordHash)
	}
}

func TestMemoryRepositoryIsConcurrentSafe(t *testing.T) {
	repo := sessions.NewMemoryRepository()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := mustNewP2PShare(t)
			session.ID = "share_" + string(rune('A'+i))
			if err := repo.Save(ctx, session); err != nil {
				t.Errorf("Save returned error: %v", err)
			}
			_, _ = repo.Get(ctx, session.ID)
			_, _ = repo.ListActive(ctx)
		}(i)
	}
	wg.Wait()

	active, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if len(active) != 50 {
		t.Fatalf("active count = %d, want 50", len(active))
	}
}

func TestMemoryRepositoryListActiveExcludesTerminalSessions(t *testing.T) {
	repo := sessions.NewMemoryRepository()
	ctx := context.Background()
	active := mustNewP2PShare(t)
	active.ID = "active"
	cancelled := mustNewP2PShare(t)
	cancelled.ID = "cancelled"
	if err := cancelled.Cancel(time.Now()); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	if err := repo.Save(ctx, active); err != nil {
		t.Fatalf("Save active returned error: %v", err)
	}
	if err := repo.Save(ctx, cancelled); err != nil {
		t.Fatalf("Save cancelled returned error: %v", err)
	}

	got, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if len(got) != 1 || got[0].ID != active.ID {
		t.Fatalf("active sessions = %#v, want only %q", got, active.ID)
	}
}
