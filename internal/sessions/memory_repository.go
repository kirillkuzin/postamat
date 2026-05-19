package sessions

import (
	"context"
	"sort"
	"sync"
)

type Repository interface {
	Save(context.Context, TransferSession) error
	Get(context.Context, string) (TransferSession, error)
	ListActive(context.Context) ([]TransferSession, error)
	Update(context.Context, string, func(*TransferSession) error) (TransferSession, error)
}

type MemoryRepository struct {
	mu       sync.RWMutex
	sessions map[string]TransferSession
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sessions: make(map[string]TransferSession),
	}
}

func (r *MemoryRepository) Save(ctx context.Context, session TransferSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.ID] = cloneSession(session)
	return nil
}

func (r *MemoryRepository) Get(ctx context.Context, id string) (TransferSession, error) {
	if err := ctx.Err(); err != nil {
		return TransferSession{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[id]
	if !ok {
		return TransferSession{}, ErrSessionNotFound
	}
	return cloneSession(session), nil
}

func (r *MemoryRepository) ListActive(ctx context.Context) ([]TransferSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	active := make([]TransferSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if !session.IsTerminal() {
			active = append(active, cloneSession(session))
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].CreatedAt.Before(active[j].CreatedAt)
	})
	return active, nil
}

func (r *MemoryRepository) Update(ctx context.Context, id string, update func(*TransferSession) error) (TransferSession, error) {
	if err := ctx.Err(); err != nil {
		return TransferSession{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	if !ok {
		return TransferSession{}, ErrSessionNotFound
	}
	updated := cloneSession(session)
	if err := update(&updated); err != nil {
		return TransferSession{}, err
	}
	r.sessions[id] = cloneSession(updated)
	return cloneSession(updated), nil
}
