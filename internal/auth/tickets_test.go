package auth

import (
	"testing"
	"time"
)

func TestIssueTicketCreatesShortLivedTransferAndRoleBoundTicket(t *testing.T) {
	now := time.Date(2026, 5, 20, 17, 0, 0, 0, time.UTC)
	issuer := TicketIssuer{Pepper: "server-pepper", Now: func() time.Time { return now }}

	issued, err := issuer.Issue(TicketRequest{
		TransferID: "tr_123",
		Role:       TicketRoleReceivingAgent,
		TTL:        2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}
	if issued.Raw == "" || issued.Hash == "" {
		t.Fatalf("expected raw ticket and stored hash: %#v", issued)
	}
	if issued.TransferID != "tr_123" || issued.Role != TicketRoleReceivingAgent {
		t.Fatalf("ticket binding changed: %#v", issued)
	}
	if !issued.ExpiresAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("expires_at = %s", issued.ExpiresAt)
	}
	if issued.Hash == issued.Raw {
		t.Fatal("stored ticket hash must not equal raw ticket")
	}

	claims, err := VerifyTicket(VerifyTicketRequest{
		Raw:        issued.Raw,
		Hash:       issued.Hash,
		Pepper:     "server-pepper",
		TransferID: "tr_123",
		Role:       TicketRoleReceivingAgent,
		ExpiresAt:  issued.ExpiresAt,
		Now:        now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("VerifyTicket returned error: %v", err)
	}
	if claims.TransferID != "tr_123" || claims.Role != TicketRoleReceivingAgent {
		t.Fatalf("unexpected ticket claims: %#v", claims)
	}
}

func TestIssueTicketValidatesBindingAndTTL(t *testing.T) {
	issuer := TicketIssuer{Pepper: "pepper"}
	tests := []struct {
		name string
		req  TicketRequest
		err  error
	}{
		{name: "transfer id", req: TicketRequest{Role: TicketRoleSenderAgent, TTL: time.Minute}, err: ErrTicketTransferRequired},
		{name: "role", req: TicketRequest{TransferID: "tr_123", TTL: time.Minute}, err: ErrTicketRoleRequired},
		{name: "unsupported role", req: TicketRequest{TransferID: "tr_123", Role: "admin", TTL: time.Minute}, err: ErrTicketRoleUnsupported},
		{name: "ttl", req: TicketRequest{TransferID: "tr_123", Role: TicketRoleSenderAgent}, err: ErrTicketTTLNotPositive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := issuer.Issue(tc.req)
			if err != tc.err {
				t.Fatalf("expected %v, got %v", tc.err, err)
			}
		})
	}
}

func TestVerifyTicketRejectsWrongBindingAndExpiredTickets(t *testing.T) {
	now := time.Date(2026, 5, 20, 17, 30, 0, 0, time.UTC)
	issuer := TicketIssuer{Pepper: "pepper", Now: func() time.Time { return now }}
	issued, err := issuer.Issue(TicketRequest{TransferID: "tr_123", Role: TicketRoleBrowserRecipient, TTL: time.Minute})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}
	tests := []struct {
		name string
		req  VerifyTicketRequest
		err  error
	}{
		{name: "wrong transfer", req: VerifyTicketRequest{Raw: issued.Raw, Hash: issued.Hash, Pepper: "pepper", TransferID: "tr_other", Role: TicketRoleBrowserRecipient, ExpiresAt: issued.ExpiresAt, Now: now}, err: ErrTicketBindingMismatch},
		{name: "wrong role", req: VerifyTicketRequest{Raw: issued.Raw, Hash: issued.Hash, Pepper: "pepper", TransferID: "tr_123", Role: TicketRoleReceivingAgent, ExpiresAt: issued.ExpiresAt, Now: now}, err: ErrTicketBindingMismatch},
		{name: "expired", req: VerifyTicketRequest{Raw: issued.Raw, Hash: issued.Hash, Pepper: "pepper", TransferID: "tr_123", Role: TicketRoleBrowserRecipient, ExpiresAt: issued.ExpiresAt, Now: now.Add(2 * time.Minute)}, err: ErrTicketExpired},
		{name: "zero now is not accepted", req: VerifyTicketRequest{Raw: issued.Raw, Hash: issued.Hash, Pepper: "pepper", TransferID: "tr_123", Role: TicketRoleBrowserRecipient, ExpiresAt: issued.ExpiresAt}, err: ErrTicketNowRequired},
		{name: "wrong raw", req: VerifyTicketRequest{Raw: "wrong", Hash: issued.Hash, Pepper: "pepper", TransferID: "tr_123", Role: TicketRoleBrowserRecipient, ExpiresAt: issued.ExpiresAt, Now: now}, err: ErrTicketInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyTicket(tc.req)
			if err != tc.err {
				t.Fatalf("expected %v, got %v", tc.err, err)
			}
		})
	}
}
