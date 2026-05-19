package sessions_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/sessions"
)

func TestNewP2PShareAppliesSafeDefaults(t *testing.T) {
	share, err := sessions.NewP2PShare(sessions.CreateP2PShareInput{
		OwnerAgentID:  "agent_1",
		FileName:      "report.pdf",
		FileSizeBytes: 42,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	})

	if err != nil {
		t.Fatalf("NewP2PShare returned error: %v", err)
	}
	if share.Transport != sessions.TransportWebRTCP2P {
		t.Fatalf("Transport = %q, want %q", share.Transport, sessions.TransportWebRTCP2P)
	}
	if share.Status != sessions.StatusWaitingSender {
		t.Fatalf("Status = %q, want %q", share.Status, sessions.StatusWaitingSender)
	}
	if share.MaxDownloads != 1 {
		t.Fatalf("MaxDownloads = %d, want 1", share.MaxDownloads)
	}
	if got, want := share.ExpiresAt.Sub(share.CreatedAt), 30*time.Minute; got != want {
		t.Fatalf("TTL = %s, want %s", got, want)
	}
}

func TestNewP2PShareRejectsInvalidRequiredFieldsWithTypedErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   sessions.CreateP2PShareInput
		wantErr error
	}{
		{
			name: "missing owner agent",
			input: sessions.CreateP2PShareInput{
				OwnerAgentID:  "",
				FileName:      "report.pdf",
				FileSizeBytes: 42,
				Now:           time.Now(),
			},
			wantErr: sessions.ErrOwnerAgentRequired,
		},
		{
			name: "missing file name",
			input: sessions.CreateP2PShareInput{
				OwnerAgentID:  "agent_1",
				FileName:      "",
				FileSizeBytes: 42,
				Now:           time.Now(),
			},
			wantErr: sessions.ErrFileNameRequired,
		},
		{
			name: "negative file size",
			input: sessions.CreateP2PShareInput{
				OwnerAgentID:  "agent_1",
				FileName:      "report.pdf",
				FileSizeBytes: -1,
				Now:           time.Now(),
			},
			wantErr: sessions.ErrFileSizeNegative,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sessions.NewP2PShare(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewP2PShare error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewP2PShareRejectsInvalidPolicyWithTypedErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   sessions.CreateP2PShareInput
		wantErr error
	}{
		{
			name: "negative TTL",
			input: validP2PShareInput(func(input *sessions.CreateP2PShareInput) {
				input.TTL = -time.Second
			}),
			wantErr: sessions.ErrTTLNotPositive,
		},
		{
			name: "negative max downloads",
			input: validP2PShareInput(func(input *sessions.CreateP2PShareInput) {
				input.MaxDownloads = -1
			}),
			wantErr: sessions.ErrMaxDownloadsNotPositive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sessions.NewP2PShare(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewP2PShare error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func validP2PShareInput(mutate func(*sessions.CreateP2PShareInput)) sessions.CreateP2PShareInput {
	input := sessions.CreateP2PShareInput{
		OwnerAgentID:  "agent_1",
		FileName:      "report.pdf",
		FileSizeBytes: 42,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	}
	if mutate != nil {
		mutate(&input)
	}
	return input
}
