package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
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

type RandomTokenIssuer struct{}

func (RandomTokenIssuer) NewTransferID() string {
	return "tr_" + randomHex(16)
}

func (RandomTokenIssuer) NewPublicToken() StoredToken {
	raw := "pt_" + randomHex(32)
	return StoredToken{Raw: raw, Stored: hashToken(raw)}
}

func (RandomTokenIssuer) NewAgentTicket() StoredToken {
	raw := "at_" + randomHex(32)
	return StoredToken{Raw: raw, Stored: hashToken(raw)}
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("generate random token: %v", err))
	}
	return hex.EncodeToString(buf)
}
