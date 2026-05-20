package sessions_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestServiceCreateTransferGeneratesIdentifiersAndTickets(t *testing.T) {
	service := newTestService()

	created, err := service.CreateTransfer(context.Background(), validTransferInput(nil))
	if err != nil {
		t.Fatalf("CreateTransfer returned error: %v", err)
	}
	if created.Transfer.ID != "transfer_1" {
		t.Fatalf("Transfer.ID = %q, want transfer_1", created.Transfer.ID)
	}
	if created.AgentTicket != "agent_ticket_1" {
		t.Fatalf("AgentTicket = %q, want agent_ticket_1", created.AgentTicket)
	}
	if created.Transfer.AgentTicketHash != "agent_ticket_hash_1" {
		t.Fatalf("AgentTicketHash = %q, want agent_ticket_hash_1", created.Transfer.AgentTicketHash)
	}
	if created.PublicToken != "" {
		t.Fatalf("PublicToken = %q, want empty for agent target", created.PublicToken)
	}
	if created.Transfer.Status != sessions.StatusCreated {
		t.Fatalf("Status = %q, want %q", created.Transfer.Status, sessions.StatusCreated)
	}
}

func TestServiceCreateBrowserLinkTransferGeneratesPublicToken(t *testing.T) {
	service := newTestService()

	created, err := service.CreateTransfer(context.Background(), validTransferInput(func(input *sessions.CreateTransferInput) {
		input.Target = sessions.TargetBrowserLink
		input.ToAgentID = ""
	}))
	if err != nil {
		t.Fatalf("CreateTransfer returned error: %v", err)
	}
	if created.PublicToken != "public_token_1" {
		t.Fatalf("PublicToken = %q, want public_token_1", created.PublicToken)
	}
	if created.Transfer.PublicTokenHash != "public_token_hash_1" {
		t.Fatalf("PublicTokenHash = %q, want public_token_hash_1", created.Transfer.PublicTokenHash)
	}
}

func TestServiceGetReturnsExistingTransfer(t *testing.T) {
	service := newTestService()
	created, err := service.CreateTransfer(context.Background(), validTransferInput(nil))
	if err != nil {
		t.Fatalf("CreateTransfer returned error: %v", err)
	}

	got, err := service.Get(context.Background(), created.Transfer.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != created.Transfer.ID {
		t.Fatalf("Get ID = %q, want %q", got.ID, created.Transfer.ID)
	}
}

func TestServiceListActiveExcludesTerminalTransfers(t *testing.T) {
	service := newTestService()
	first, err := service.CreateTransfer(context.Background(), validTransferInput(nil))
	if err != nil {
		t.Fatalf("CreateTransfer first returned error: %v", err)
	}
	second, err := service.CreateTransfer(context.Background(), validTransferInput(func(input *sessions.CreateTransferInput) {
		input.FileName = "second.pdf"
	}))
	if err != nil {
		t.Fatalf("CreateTransfer second returned error: %v", err)
	}
	if _, err := service.Cancel(context.Background(), first.Transfer.ID); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	active, err := service.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive returned error: %v", err)
	}
	if len(active) != 1 || active[0].ID != second.Transfer.ID {
		t.Fatalf("active transfers = %#v, want only %q", active, second.Transfer.ID)
	}
}

func TestServiceCanMarkFailedAndExpired(t *testing.T) {
	service := newTestService()
	created, err := service.CreateTransfer(context.Background(), validTransferInput(nil))
	if err != nil {
		t.Fatalf("CreateTransfer returned error: %v", err)
	}

	failed, err := service.MarkFailed(context.Background(), created.Transfer.ID, "agentd disconnected")
	if err != nil {
		t.Fatalf("MarkFailed returned error: %v", err)
	}
	if failed.Status != sessions.StatusFailed {
		t.Fatalf("failed status = %q, want %q", failed.Status, sessions.StatusFailed)
	}
	if failed.FailureReason != "agentd disconnected" {
		t.Fatalf("FailureReason = %q, want agentd disconnected", failed.FailureReason)
	}

	expiring, err := service.CreateTransfer(context.Background(), validTransferInput(func(input *sessions.CreateTransferInput) {
		input.FileName = "expire.pdf"
	}))
	if err != nil {
		t.Fatalf("CreateTransfer expiring returned error: %v", err)
	}
	expired, err := service.Expire(context.Background(), expiring.Transfer.ID)
	if err != nil {
		t.Fatalf("Expire returned error: %v", err)
	}
	if expired.Status != sessions.StatusExpired {
		t.Fatalf("expired status = %q, want %q", expired.Status, sessions.StatusExpired)
	}
}

func TestServiceCancelChangesStatus(t *testing.T) {
	service := newTestService()
	created, err := service.CreateTransfer(context.Background(), validTransferInput(nil))
	if err != nil {
		t.Fatalf("CreateTransfer returned error: %v", err)
	}

	cancelled, err := service.Cancel(context.Background(), created.Transfer.ID)
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

	agentTicket := issuer.NewAgentTicket()
	if agentTicket.Raw == "" || agentTicket.Stored == "" {
		t.Fatalf("agent ticket fields must be non-empty: %#v", agentTicket)
	}
	if strings.Contains(agentTicket.Stored, agentTicket.Raw) {
		t.Fatalf("stored agent ticket %q must not contain raw ticket %q", agentTicket.Stored, agentTicket.Raw)
	}
}

func newTestService() *sessions.Service {
	return sessions.NewService(sessions.NewMemoryRepository(), &fakeTokenIssuer{}, func() time.Time {
		return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	})
}

type fakeTokenIssuer struct {
	transferID int
	public     int
	agent      int
}

func (f *fakeTokenIssuer) NewTransferID() string {
	f.transferID++
	return "transfer_" + string(rune('0'+f.transferID))
}

func (f *fakeTokenIssuer) NewPublicToken() sessions.StoredToken {
	f.public++
	return sessions.StoredToken{
		Raw:    "public_token_" + string(rune('0'+f.public)),
		Stored: "public_token_hash_" + string(rune('0'+f.public)),
	}
}

func (f *fakeTokenIssuer) NewAgentTicket() sessions.StoredToken {
	f.agent++
	return sessions.StoredToken{
		Raw:    "agent_ticket_" + string(rune('0'+f.agent)),
		Stored: "agent_ticket_hash_" + string(rune('0'+f.agent)),
	}
}
