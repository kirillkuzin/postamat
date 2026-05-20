package sessions

import (
	"context"
	"fmt"
	"time"

	"github.com/kirillkuzin/postamat/internal/auth"
)

type StoredToken struct {
	Raw    string
	Stored string
}

type TokenIssuer interface {
	NewTransferID() string
	NewPublicToken() StoredToken
	NewAgentTicket() StoredToken
}

type Service struct {
	repo   Repository
	tokens TokenIssuer
	now    func() time.Time
}

type CreateTransferResult struct {
	Transfer    TransferSession
	PublicToken string
	AgentTicket string
}

func NewService(repo Repository, tokens TokenIssuer, now func() time.Time) *Service {
	if tokens == nil {
		tokens = RandomTokenIssuer{}
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: repo, tokens: tokens, now: now}
}

func (s *Service) CreateTransfer(ctx context.Context, input CreateTransferInput) (CreateTransferResult, error) {
	if input.Now.IsZero() {
		input.Now = s.now()
	}
	transfer, err := NewTransferIntent(input)
	if err != nil {
		return CreateTransferResult{}, err
	}

	agentTicket := s.tokens.NewAgentTicket()
	transfer.ID = s.tokens.NewTransferID()
	transfer.AgentTicketHash = agentTicket.Stored

	result := CreateTransferResult{
		Transfer:    cloneSession(transfer),
		AgentTicket: agentTicket.Raw,
	}
	if transfer.Target == TargetBrowserLink {
		publicToken := s.tokens.NewPublicToken()
		transfer.PublicTokenHash = publicToken.Stored
		result.PublicToken = publicToken.Raw
		result.Transfer = cloneSession(transfer)
	}

	if err := s.repo.Save(ctx, transfer); err != nil {
		return CreateTransferResult{}, err
	}

	return result, nil
}

func (s *Service) Get(ctx context.Context, id string) (TransferSession, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) ListActive(ctx context.Context) ([]TransferSession, error) {
	return s.repo.ListActive(ctx)
}

func (s *Service) Cancel(ctx context.Context, id string) (TransferSession, error) {
	return s.repo.Update(ctx, id, func(session *TransferSession) error {
		return session.Cancel(s.now())
	})
}

func (s *Service) MarkFailed(ctx context.Context, id string, reason string) (TransferSession, error) {
	return s.repo.Update(ctx, id, func(session *TransferSession) error {
		return session.MarkFailed(reason, s.now())
	})
}

func (s *Service) Expire(ctx context.Context, id string) (TransferSession, error) {
	return s.repo.Update(ctx, id, func(session *TransferSession) error {
		return session.Expire(s.now())
	})
}

type RandomTokenIssuer struct {
	Pepper string
}

func (RandomTokenIssuer) NewTransferID() string {
	raw, err := auth.GenerateToken("tr", 16)
	if err != nil {
		panic(fmt.Sprintf("generate transfer id: %v", err))
	}
	return raw
}

func (i RandomTokenIssuer) NewPublicToken() StoredToken {
	raw, err := auth.GenerateToken("pt", 32)
	if err != nil {
		panic(fmt.Sprintf("generate public token: %v", err))
	}
	return StoredToken{Raw: raw, Stored: i.StorePublicToken(raw)}
}

func (i RandomTokenIssuer) StorePublicToken(raw string) string {
	stored, err := auth.HashToken(raw, i.pepper())
	if err != nil {
		panic(fmt.Sprintf("hash public token: %v", err))
	}
	return stored
}

func (i RandomTokenIssuer) NewAgentTicket() StoredToken {
	raw, err := auth.GenerateToken("at", 32)
	if err != nil {
		panic(fmt.Sprintf("generate agent ticket: %v", err))
	}
	stored, err := auth.HashToken(raw, i.pepper())
	if err != nil {
		panic(fmt.Sprintf("hash agent ticket: %v", err))
	}
	return StoredToken{Raw: raw, Stored: stored}
}

func (i RandomTokenIssuer) pepper() string {
	if i.Pepper != "" {
		return i.Pepper
	}
	return "postamat-development-token-pepper"
}
