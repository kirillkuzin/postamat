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
	transfer := mustNewTransferIntent(t)
	transfer.ID = "transfer_1"

	if err := repo.Save(ctx, transfer); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := repo.Get(ctx, transfer.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != transfer.ID {
		t.Fatalf("Get ID = %q, want %q", got.ID, transfer.ID)
	}

	updated, err := repo.Update(ctx, transfer.ID, func(s *sessions.TransferSession) error {
		return s.MarkOffered()
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.Status != sessions.StatusOffered {
		t.Fatalf("updated status = %q, want %q", updated.Status, sessions.StatusOffered)
	}

	active, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if len(active) != 1 || active[0].ID != transfer.ID {
		t.Fatalf("active = %#v, want one transfer %q", active, transfer.ID)
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
	transfer := mustNewTransferIntent(t)
	transfer.ID = "transfer_1"
	passwordHash := "stored_hash"
	transfer.PasswordHash = &passwordHash

	if err := repo.Save(ctx, transfer); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	passwordHash = "mutated_after_save"

	got, err := repo.Get(ctx, transfer.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.PasswordHash == nil || *got.PasswordHash != "stored_hash" {
		t.Fatalf("stored password hash = %v, want stored_hash", got.PasswordHash)
	}

	*got.PasswordHash = "mutated_after_get"
	again, err := repo.Get(ctx, transfer.ID)
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
			transfer := mustNewTransferIntent(t)
			transfer.ID = "transfer_" + string(rune('A'+i))
			if err := repo.Save(ctx, transfer); err != nil {
				t.Errorf("Save returned error: %v", err)
			}
			_, _ = repo.Get(ctx, transfer.ID)
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
	active := mustNewTransferIntent(t)
	active.ID = "active"
	cancelled := mustNewTransferIntent(t)
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
		t.Fatalf("active transfers = %#v, want only %q", got, active.ID)
	}
}
