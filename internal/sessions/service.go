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
	NewReceiverTicket(transferID string) StoredToken
}

type TokenVerifier interface {
	VerifyStoredToken(raw string, stored string) (bool, error)
}

type Service struct {
	repo     Repository
	tokens   TokenIssuer
	verifier TokenVerifier
	now      func() time.Time
}

type CreateTransferResult struct {
	Transfer    TransferSession
	PublicToken string
	AgentTicket string
}

type ReceiverConsent struct {
	Accepted bool
	Password string
}

type BrowserReceiverTicket struct {
	Transfer       TransferSession
	ReceiverTicket string
	ExpiresAt      time.Time
}

const receiverTicketTTL = 5 * time.Minute

func NewService(repo Repository, tokens TokenIssuer, now func() time.Time) *Service {
	if tokens == nil {
		tokens = RandomTokenIssuer{}
	}
	verifier, ok := tokens.(TokenVerifier)
	if !ok {
		verifier = RandomTokenIssuer{}
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: repo, tokens: tokens, verifier: verifier, now: now}
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

func (s *Service) VerifyAgentTicket(ctx context.Context, transferID string, rawTicket string) (TransferSession, error) {
	transfer, err := s.repo.Get(ctx, transferID)
	if err != nil {
		return TransferSession{}, err
	}
	if !s.isTransferUsable(transfer) {
		return TransferSession{}, auth.ErrTicketExpired
	}
	ok, err := s.verifier.VerifyStoredToken(rawTicket, transfer.AgentTicketHash)
	if err != nil || !ok {
		return TransferSession{}, auth.ErrTicketInvalid
	}
	return transfer, nil
}

func (s *Service) VerifyPublicToken(ctx context.Context, rawToken string) (TransferSession, error) {
	transfers, err := s.repo.ListActive(ctx)
	if err != nil {
		return TransferSession{}, err
	}
	for _, transfer := range transfers {
		if transfer.PublicTokenHash == "" || transfer.Target != TargetBrowserLink || !s.isTransferUsable(transfer) {
			continue
		}
		ok, err := s.verifier.VerifyStoredToken(rawToken, transfer.PublicTokenHash)
		if err == nil && ok {
			return transfer, nil
		}
	}
	return TransferSession{}, ErrSessionNotFound
}

func (s *Service) IssueBrowserReceiverTicket(ctx context.Context, rawPublicToken string, consent ReceiverConsent) (BrowserReceiverTicket, error) {
	if !consent.Accepted {
		return BrowserReceiverTicket{}, ErrReceiverConsentRequired
	}
	transfer, err := s.VerifyPublicToken(ctx, rawPublicToken)
	if err != nil {
		return BrowserReceiverTicket{}, err
	}
	if transfer.PasswordHash != nil {
		if consent.Password == "" {
			return BrowserReceiverTicket{}, ErrReceiverPasswordRequired
		}
		ok, err := s.verifier.VerifyStoredToken(consent.Password, *transfer.PasswordHash)
		if err != nil || !ok {
			return BrowserReceiverTicket{}, ErrReceiverPasswordInvalid
		}
	}
	now := s.now()
	expiresAt := now.Add(receiverTicketTTL)
	if transfer.ExpiresAt.Before(expiresAt) {
		expiresAt = transfer.ExpiresAt
	}
	storedTicket := s.tokens.NewReceiverTicket(transfer.ID)
	updated, err := s.repo.Update(ctx, transfer.ID, func(session *TransferSession) error {
		if session.Target != TargetBrowserLink || !s.isTransferUsable(*session) {
			return ErrSessionNotFound
		}
		session.ReceiverTicketHash = storedTicket.Stored
		session.ReceiverTicketExpiresAt = cloneTime(expiresAt)
		return nil
	})
	if err != nil {
		return BrowserReceiverTicket{}, err
	}
	return BrowserReceiverTicket{Transfer: updated, ReceiverTicket: storedTicket.Raw, ExpiresAt: expiresAt}, nil
}

func (s *Service) VerifyBrowserReceiverTicket(ctx context.Context, rawPublicToken string, rawTicket string) (TransferSession, error) {
	transfer, err := s.VerifyPublicToken(ctx, rawPublicToken)
	if err != nil {
		return TransferSession{}, err
	}
	if transfer.ReceiverTicketHash == "" || transfer.ReceiverTicketExpiresAt == nil {
		return TransferSession{}, ErrReceiverTicketNotIssued
	}
	if !s.now().Before(*transfer.ReceiverTicketExpiresAt) {
		return TransferSession{}, auth.ErrTicketExpired
	}
	ok, err := s.verifier.VerifyStoredToken(receiverTicketBindingRaw(rawTicket, transfer.ID), transfer.ReceiverTicketHash)
	if err != nil || !ok {
		return TransferSession{}, auth.ErrTicketInvalid
	}
	return transfer, nil
}

func (s *Service) ListActive(ctx context.Context) ([]TransferSession, error) {
	return s.repo.ListActive(ctx)
}

func (s *Service) VerifyTransferUsable(ctx context.Context, transferID string) (TransferSession, error) {
	transfer, err := s.repo.Get(ctx, transferID)
	if err != nil {
		return TransferSession{}, err
	}
	if !s.isTransferUsable(transfer) {
		return TransferSession{}, ErrSessionNotFound
	}
	return transfer, nil
}

func (s *Service) isTransferUsable(transfer TransferSession) bool {
	if isTerminalStatus(transfer.Status) {
		return false
	}
	return s.now().Before(transfer.ExpiresAt)
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusExpired:
		return true
	default:
		return false
	}
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

func (i RandomTokenIssuer) VerifyStoredToken(raw string, stored string) (bool, error) {
	return auth.VerifyToken(raw, stored, i.pepper())
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

func (i RandomTokenIssuer) NewReceiverTicket(transferID string) StoredToken {
	raw, err := auth.GenerateToken("rt", 32)
	if err != nil {
		panic(fmt.Sprintf("generate receiver ticket: %v", err))
	}
	stored, err := auth.HashToken(receiverTicketBindingRaw(raw, transferID), i.pepper())
	if err != nil {
		panic(fmt.Sprintf("hash receiver ticket: %v", err))
	}
	return StoredToken{Raw: raw, Stored: stored}
}

func receiverTicketBindingRaw(raw string, transferID string) string {
	return "browser_recipient:" + transferID + ":" + raw
}

func (i RandomTokenIssuer) pepper() string {
	if i.Pepper != "" {
		return i.Pepper
	}
	return "postamat-development-token-pepper"
}
