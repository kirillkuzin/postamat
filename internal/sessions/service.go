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
	NewShareID() string
	NewPublicToken() StoredToken
	NewSenderTicket() StoredToken
}

type Service struct {
	repo   Repository
	tokens TokenIssuer
	now    func() time.Time
}

type CreateP2PShareResult struct {
	Share        TransferSession
	PublicToken  string
	SenderTicket string
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

func (s *Service) CreateP2PShare(ctx context.Context, input CreateP2PShareInput) (CreateP2PShareResult, error) {
	if input.Now.IsZero() {
		input.Now = s.now()
	}
	session, err := NewP2PShare(input)
	if err != nil {
		return CreateP2PShareResult{}, err
	}

	publicToken := s.tokens.NewPublicToken()
	senderTicket := s.tokens.NewSenderTicket()
	session.ID = s.tokens.NewShareID()
	session.PublicTokenHash = publicToken.Stored
	session.SenderTicketHash = senderTicket.Stored

	if err := s.repo.Save(ctx, session); err != nil {
		return CreateP2PShareResult{}, err
	}

	return CreateP2PShareResult{
		Share:        cloneSession(session),
		PublicToken:  publicToken.Raw,
		SenderTicket: senderTicket.Raw,
	}, nil
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

type RandomTokenIssuer struct{}

func (RandomTokenIssuer) NewShareID() string {
	return "sh_" + randomHex(16)
}

func (RandomTokenIssuer) NewPublicToken() StoredToken {
	raw := "pt_" + randomHex(32)
	return StoredToken{Raw: raw, Stored: hashToken(raw)}
}

func (RandomTokenIssuer) NewSenderTicket() StoredToken {
	raw := "st_" + randomHex(32)
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
