package sessions_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestServiceCreateP2PShareGeneratesIdentifiersAndTickets(t *testing.T) {
	service := newTestService()

	created, err := service.CreateP2PShare(context.Background(), sessions.CreateP2PShareInput{
		OwnerAgentID:  "agent_1",
		FileName:      "report.pdf",
		FileSizeBytes: 42,
	})
	if err != nil {
		t.Fatalf("CreateP2PShare returned error: %v", err)
	}
	if created.Share.ID != "share_1" {
		t.Fatalf("Share.ID = %q, want share_1", created.Share.ID)
	}
	if created.PublicToken != "public_token_1" {
		t.Fatalf("PublicToken = %q, want public_token_1", created.PublicToken)
	}
	if created.SenderTicket != "sender_ticket_1" {
		t.Fatalf("SenderTicket = %q, want sender_ticket_1", created.SenderTicket)
	}
	if created.Share.PublicTokenHash != "public_token_hash_1" {
		t.Fatalf("PublicTokenHash = %q, want public_token_hash_1", created.Share.PublicTokenHash)
	}
	if created.Share.Status != sessions.StatusWaitingSender {
		t.Fatalf("Status = %q, want %q", created.Share.Status, sessions.StatusWaitingSender)
	}
}

func TestServiceGetReturnsExistingShare(t *testing.T) {
	service := newTestService()
	created, err := service.CreateP2PShare(context.Background(), validP2PShareInput(nil))
	if err != nil {
		t.Fatalf("CreateP2PShare returned error: %v", err)
	}

	got, err := service.Get(context.Background(), created.Share.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != created.Share.ID {
		t.Fatalf("Get ID = %q, want %q", got.ID, created.Share.ID)
	}
}

func TestServiceListActiveExcludesTerminalSessions(t *testing.T) {
	service := newTestService()
	first, err := service.CreateP2PShare(context.Background(), validP2PShareInput(nil))
	if err != nil {
		t.Fatalf("CreateP2PShare first returned error: %v", err)
	}
	second, err := service.CreateP2PShare(context.Background(), validP2PShareInput(func(input *sessions.CreateP2PShareInput) {
		input.FileName = "second.pdf"
	}))
	if err != nil {
		t.Fatalf("CreateP2PShare second returned error: %v", err)
	}
	if _, err := service.Cancel(context.Background(), first.Share.ID); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	active, err := service.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if len(active) != 1 || active[0].ID != second.Share.ID {
		t.Fatalf("active shares = %#v, want only %q", active, second.Share.ID)
	}
}

func TestServiceCancelChangesStatus(t *testing.T) {
	service := newTestService()
	created, err := service.CreateP2PShare(context.Background(), validP2PShareInput(nil))
	if err != nil {
		t.Fatalf("CreateP2PShare returned error: %v", err)
	}

	cancelled, err := service.Cancel(context.Background(), created.Share.ID)
	if err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if cancelled.Status != sessions.StatusCancelled {
		t.Fatalf("Status = %q, want %q", cancelled.Status, sessions.StatusCancelled)
	}
}

func TestServiceUnknownIDReturnsNotFound(t *testing.T) {
	service := newTestService()

	_, err := service.Get(context.Background(), "missing")
	if !errors.Is(err, sessions.ErrSessionNotFound) {
		t.Fatalf("Get error = %v, want %v", err, sessions.ErrSessionNotFound)
	}

	_, err = service.Cancel(context.Background(), "missing")
	if !errors.Is(err, sessions.ErrSessionNotFound) {
		t.Fatalf("Cancel error = %v, want %v", err, sessions.ErrSessionNotFound)
	}
}

func TestDefaultTokenIssuerDoesNotStoreRawToken(t *testing.T) {
	issuer := sessions.RandomTokenIssuer{}

	publicToken := issuer.NewPublicToken()
	if publicToken.Raw == "" || publicToken.Stored == "" {
		t.Fatalf("public token fields must be non-empty: %#v", publicToken)
	}
	if strings.Contains(publicToken.Stored, publicToken.Raw) {
		t.Fatalf("stored public token %q must not contain raw token %q", publicToken.Stored, publicToken.Raw)
	}

	senderTicket := issuer.NewSenderTicket()
	if senderTicket.Raw == "" || senderTicket.Stored == "" {
		t.Fatalf("sender ticket fields must be non-empty: %#v", senderTicket)
	}
	if strings.Contains(senderTicket.Stored, senderTicket.Raw) {
		t.Fatalf("stored sender ticket %q must not contain raw ticket %q", senderTicket.Stored, senderTicket.Raw)
	}
}

func newTestService() *sessions.Service {
	return sessions.NewService(sessions.NewMemoryRepository(), &fakeTokenIssuer{}, func() time.Time {
		return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	})
}

type fakeTokenIssuer struct {
	shareID int
	public  int
	sender  int
}

func (f *fakeTokenIssuer) NewShareID() string {
	f.shareID++
	return "share_" + string(rune('0'+f.shareID))
}

func (f *fakeTokenIssuer) NewPublicToken() sessions.StoredToken {
	f.public++
	return sessions.StoredToken{
		Raw:    "public_token_" + string(rune('0'+f.public)),
		Stored: "public_token_hash_" + string(rune('0'+f.public)),
	}
}

func (f *fakeTokenIssuer) NewSenderTicket() sessions.StoredToken {
	f.sender++
	return sessions.StoredToken{
		Raw:    "sender_ticket_" + string(rune('0'+f.sender)),
		Stored: "sender_ticket_hash_" + string(rune('0'+f.sender)),
	}
}
