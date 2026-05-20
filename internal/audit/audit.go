package audit

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type EventType string

const (
	EventTransferCreated   EventType = "transfer.created"
	EventTransferOffered   EventType = "transfer.offered"
	EventTransferAccepted  EventType = "transfer.accepted"
	EventTransferStarted   EventType = "transfer.started"
	EventTransferCompleted EventType = "transfer.completed"
	EventTransferFailed    EventType = "transfer.failed"
	EventTransferCancelled EventType = "transfer.cancelled"
	EventTransferExpired   EventType = "transfer.expired"
)

type Role string

const (
	RoleSenderAgent      Role = "sender_agent"
	RoleReceivingAgent   Role = "receiving_agent"
	RoleBrowserRecipient Role = "browser_recipient"
)

var (
	ErrTransferIDRequired   = errors.New("transfer id is required")
	ErrEventTypeRequired    = errors.New("event type is required")
	ErrRoleRequired         = errors.New("role is required")
	ErrRoleUnsupported      = errors.New("unsupported audit role")
	ErrEventTypeUnsupported = errors.New("unsupported audit event type")
)

type TransferEventInput struct {
	TransferID   string
	EventType    EventType
	ActorAgentID string
	Role         Role
	Payload      map[string]any
	Now          time.Time
}

type TransferEvent struct {
	ID              int64
	TransferID      string
	EventType       EventType
	ActorAgentID    string
	Role            Role
	RedactedPayload json.RawMessage
	CreatedAt       time.Time
}

func NewTransferEvent(input TransferEventInput) (TransferEvent, error) {
	if input.TransferID == "" {
		return TransferEvent{}, ErrTransferIDRequired
	}
	if input.EventType == "" {
		return TransferEvent{}, ErrEventTypeRequired
	}
	if !isSupportedEventType(input.EventType) {
		return TransferEvent{}, ErrEventTypeUnsupported
	}
	if input.Role == "" {
		return TransferEvent{}, ErrRoleRequired
	}
	if !isSupportedRole(input.Role) {
		return TransferEvent{}, ErrRoleUnsupported
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	redacted, err := json.Marshal(redactPayload(payload))
	if err != nil {
		return TransferEvent{}, err
	}
	return TransferEvent{
		TransferID:      input.TransferID,
		EventType:       input.EventType,
		ActorAgentID:    input.ActorAgentID,
		Role:            input.Role,
		RedactedPayload: redacted,
		CreatedAt:       now,
	}, nil
}

func isSupportedEventType(eventType EventType) bool {
	switch eventType {
	case EventTransferCreated, EventTransferOffered, EventTransferAccepted, EventTransferStarted, EventTransferCompleted, EventTransferFailed, EventTransferCancelled, EventTransferExpired:
		return true
	default:
		return false
	}
}

func isSupportedRole(role Role) bool {
	switch role {
	case RoleSenderAgent, RoleReceivingAgent, RoleBrowserRecipient:
		return true
	default:
		return false
	}
}

func redactPayload(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, nested := range typed {
			if isSensitiveKey(key) {
				redacted[key] = "[REDACTED]"
				continue
			}
			redacted[key] = redactPayload(nested)
		}
		return redacted
	case map[string]string:
		redacted := make(map[string]any, len(typed))
		for key, nested := range typed {
			if isSensitiveKey(key) {
				redacted[key] = "[REDACTED]"
				continue
			}
			redacted[key] = nested
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, nested := range typed {
			redacted[i] = redactPayload(nested)
		}
		return redacted
	case []map[string]any:
		redacted := make([]any, len(typed))
		for i, nested := range typed {
			redacted[i] = redactPayload(nested)
		}
		return redacted
	case []map[string]string:
		redacted := make([]any, len(typed))
		for i, nested := range typed {
			redacted[i] = redactPayload(nested)
		}
		return redacted
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "token") || strings.Contains(lower, "ticket") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "key")
}
