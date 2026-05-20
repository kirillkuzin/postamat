package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewTransferEventRedactsSensitivePayloadFields(t *testing.T) {
	now := time.Date(2026, 5, 20, 18, 0, 0, 0, time.UTC)
	event, err := NewTransferEvent(TransferEventInput{
		TransferID:   "tr_123",
		EventType:    EventTransferCreated,
		ActorAgentID: "agent-a",
		Role:         RoleSenderAgent,
		Payload: map[string]any{
			"file_name": "report.pdf",
			"token":     "raw-secret",
			"password":  "pw",
			"nested": map[string]any{
				"ticket": "raw-ticket",
				"safe":   "value",
			},
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("NewTransferEvent returned error: %v", err)
	}
	if event.TransferID != "tr_123" || event.EventType != EventTransferCreated || event.CreatedAt != now {
		t.Fatalf("unexpected event metadata: %#v", event)
	}
	payload := string(event.RedactedPayload)
	if containsAny(payload, "raw-secret", "raw-ticket", "pw") {
		t.Fatalf("redacted payload leaked sensitive values: %s", payload)
	}
	if !containsAny(payload, "[REDACTED]") || !containsAny(payload, "report.pdf", "value") {
		t.Fatalf("payload redaction lost expected safe fields: %s", payload)
	}
}

func TestNewTransferEventValidatesRequiredFields(t *testing.T) {
	tests := []struct {
		name  string
		input TransferEventInput
		err   error
	}{
		{name: "transfer", input: TransferEventInput{EventType: EventTransferCreated, Role: RoleSenderAgent}, err: ErrTransferIDRequired},
		{name: "event", input: TransferEventInput{TransferID: "tr_123", Role: RoleSenderAgent}, err: ErrEventTypeRequired},
		{name: "unsupported event", input: TransferEventInput{TransferID: "tr_123", EventType: "transfer.typo", Role: RoleSenderAgent}, err: ErrEventTypeUnsupported},
		{name: "role", input: TransferEventInput{TransferID: "tr_123", EventType: EventTransferCreated}, err: ErrRoleRequired},
		{name: "unsupported role", input: TransferEventInput{TransferID: "tr_123", EventType: EventTransferCreated, Role: "admin"}, err: ErrRoleUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewTransferEvent(tc.input)
			if err != tc.err {
				t.Fatalf("expected %v, got %v", tc.err, err)
			}
		})
	}
}

func TestTransferEventRedactedPayloadIsJSON(t *testing.T) {
	event, err := NewTransferEvent(TransferEventInput{TransferID: "tr_123", EventType: EventTransferFailed, Role: RoleReceivingAgent, Payload: map[string]any{"reason": "network"}})
	if err != nil {
		t.Fatalf("NewTransferEvent returned error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(event.RedactedPayload, &decoded); err != nil {
		t.Fatalf("redacted payload is not JSON: %v", err)
	}
	if decoded["reason"] != "network" {
		t.Fatalf("unexpected decoded payload: %#v", decoded)
	}
}

func containsAny(s string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(s, value) {
			return true
		}
	}
	return false
}
