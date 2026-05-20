package sessions_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestNewTransferIntentCreatesAgentTargetWithSafeDefaults(t *testing.T) {
	transfer, err := sessions.NewTransferIntent(sessions.CreateTransferInput{
		FromAgentID:   "agent_a",
		ToAgentID:     "agent_b",
		Target:        sessions.TargetAgent,
		FileName:      "report.pdf",
		FileSizeBytes: 42,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewTransferIntent returned error: %v", err)
	}
	if transfer.Transport != sessions.TransportWebRTCP2P {
		t.Fatalf("Transport = %q, want %q", transfer.Transport, sessions.TransportWebRTCP2P)
	}
	if transfer.Target != sessions.TargetAgent {
		t.Fatalf("Target = %q, want %q", transfer.Target, sessions.TargetAgent)
	}
	if transfer.Status != sessions.StatusCreated {
		t.Fatalf("Status = %q, want %q", transfer.Status, sessions.StatusCreated)
	}
	if transfer.FromAgentID != "agent_a" || transfer.ToAgentID != "agent_b" {
		t.Fatalf("agents = from %q to %q, want agent_a to agent_b", transfer.FromAgentID, transfer.ToAgentID)
	}
	if got, want := transfer.ExpiresAt.Sub(transfer.CreatedAt), 30*time.Minute; got != want {
		t.Fatalf("TTL = %s, want %s", got, want)
	}
	if transfer.MaxDownloads != 1 {
		t.Fatalf("MaxDownloads = %d, want 1", transfer.MaxDownloads)
	}
}

func TestNewTransferIntentCreatesBrowserLinkTarget(t *testing.T) {
	transfer, err := sessions.NewTransferIntent(sessions.CreateTransferInput{
		FromAgentID:   "agent_a",
		Target:        sessions.TargetBrowserLink,
		FileName:      "report.pdf",
		FileSizeBytes: 42,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewTransferIntent returned error: %v", err)
	}
	if transfer.Target != sessions.TargetBrowserLink {
		t.Fatalf("Target = %q, want %q", transfer.Target, sessions.TargetBrowserLink)
	}
	if transfer.ToAgentID != "" {
		t.Fatalf("ToAgentID = %q, want empty for browser link", transfer.ToAgentID)
	}
}

func TestNewTransferIntentRejectsInvalidRequiredFieldsWithTypedErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   sessions.CreateTransferInput
		wantErr error
	}{
		{
			name: "missing from agent",
			input: sessions.CreateTransferInput{
				FromAgentID:   "",
				ToAgentID:     "agent_b",
				Target:        sessions.TargetAgent,
				FileName:      "report.pdf",
				FileSizeBytes: 42,
			},
			wantErr: sessions.ErrFromAgentRequired,
		},
		{
			name: "missing target",
			input: validTransferInput(func(input *sessions.CreateTransferInput) {
				input.Target = ""
			}),
			wantErr: sessions.ErrTargetRequired,
		},
		{
			name: "unsupported target",
			input: validTransferInput(func(input *sessions.CreateTransferInput) {
				input.Target = "fax"
			}),
			wantErr: sessions.ErrUnsupportedTarget,
		},
		{
			name: "agent target missing to agent",
			input: validTransferInput(func(input *sessions.CreateTransferInput) {
				input.ToAgentID = ""
			}),
			wantErr: sessions.ErrTargetAgentRequired,
		},
		{
			name: "missing file name",
			input: validTransferInput(func(input *sessions.CreateTransferInput) {
				input.FileName = ""
			}),
			wantErr: sessions.ErrFileNameRequired,
		},
		{
			name: "negative file size",
			input: validTransferInput(func(input *sessions.CreateTransferInput) {
				input.FileSizeBytes = -1
			}),
			wantErr: sessions.ErrFileSizeNegative,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sessions.NewTransferIntent(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewTransferIntent error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewTransferIntentRejectsInvalidPolicyWithTypedErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   sessions.CreateTransferInput
		wantErr error
	}{
		{
			name: "negative TTL",
			input: validTransferInput(func(input *sessions.CreateTransferInput) {
				input.TTL = -time.Second
			}),
			wantErr: sessions.ErrTTLNotPositive,
		},
		{
			name: "negative max downloads",
			input: validTransferInput(func(input *sessions.CreateTransferInput) {
				input.MaxDownloads = -1
			}),
			wantErr: sessions.ErrMaxDownloadsNotPositive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sessions.NewTransferIntent(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewTransferIntent error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func validTransferInput(mutate func(*sessions.CreateTransferInput)) sessions.CreateTransferInput {
	input := sessions.CreateTransferInput{
		FromAgentID:   "agent_a",
		ToAgentID:     "agent_b",
		Target:        sessions.TargetAgent,
		FileName:      "report.pdf",
		FileSizeBytes: 42,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	}
	if mutate != nil {
		mutate(&input)
	}
	return input
}
