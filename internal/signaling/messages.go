package signaling

import (
	"encoding/json"
	"errors"
)

type MessageType string

const (
	MessageAgentHello          MessageType = "agent.hello"
	MessageAgentPresence       MessageType = "agent.presence"
	MessageTransferOffer       MessageType = "transfer.offer"
	MessageTransferAccepted    MessageType = "transfer.accepted"
	MessageTransferDenied      MessageType = "transfer.denied"
	MessageWebRTCOffer         MessageType = "webrtc.offer"
	MessageWebRTCAnswer        MessageType = "webrtc.answer"
	MessageWebRTCICE           MessageType = "webrtc.ice"
	MessageTransferStarted     MessageType = "transfer.started"
	MessageTransferProgress    MessageType = "transfer.progress"
	MessageTransferInterrupted MessageType = "transfer.interrupted"
	MessageTransferRetryable   MessageType = "transfer.retryable"
	MessageTransferCompleted   MessageType = "transfer.completed"
	MessageTransferFailed      MessageType = "transfer.failed"
	MessageTransferCancelled   MessageType = "transfer.cancelled"
	MessageTransferExpired     MessageType = "transfer.expired"
	MessageAgentDisconnected   MessageType = "agent.disconnected"
	MessagePing                MessageType = "ping"
	MessagePong                MessageType = "pong"
	MessageError               MessageType = "error"
)

var (
	ErrMessageTypeRequired    = errors.New("message type is required")
	ErrUnsupportedMessageType = errors.New("unsupported message type")
	ErrAgentIDRequired        = errors.New("agent id is required")
	ErrDeviceIDRequired       = errors.New("device id is required")
	ErrTransferIDRequired     = errors.New("transfer id is required")
	ErrRouteTargetRequired    = errors.New("route target is required")
)

type Envelope struct {
	Type        MessageType     `json:"type"`
	AgentID     string          `json:"agent_id,omitempty"`
	DeviceID    string          `json:"device_id,omitempty"`
	TransferID  string          `json:"transfer_id,omitempty"`
	FromAgentID string          `json:"from_agent_id,omitempty"`
	ToAgentID   string          `json:"to_agent_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

func DecodeEnvelope(raw []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Envelope{}, err
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func (e Envelope) Validate() error {
	if e.Type == "" {
		return ErrMessageTypeRequired
	}
	if !isSupportedMessageType(e.Type) {
		return ErrUnsupportedMessageType
	}
	switch e.Type {
	case MessageAgentHello:
		if e.AgentID == "" {
			return ErrAgentIDRequired
		}
		if e.DeviceID == "" {
			return ErrDeviceIDRequired
		}
	case MessageAgentPresence, MessageAgentDisconnected:
		if e.AgentID == "" {
			return ErrAgentIDRequired
		}
	case MessageTransferOffer, MessageTransferAccepted, MessageTransferDenied, MessageTransferStarted, MessageTransferProgress, MessageTransferInterrupted, MessageTransferRetryable, MessageTransferCompleted, MessageTransferFailed, MessageTransferCancelled, MessageTransferExpired:
		if e.TransferID == "" {
			return ErrTransferIDRequired
		}
	case MessageWebRTCOffer, MessageWebRTCAnswer, MessageWebRTCICE:
		if e.TransferID == "" {
			return ErrTransferIDRequired
		}
		if e.FromAgentID == "" || e.ToAgentID == "" {
			return ErrRouteTargetRequired
		}
	}
	return nil
}

func isSupportedMessageType(messageType MessageType) bool {
	switch messageType {
	case MessageAgentHello, MessageAgentPresence, MessageTransferOffer, MessageTransferAccepted, MessageTransferDenied, MessageWebRTCOffer, MessageWebRTCAnswer, MessageWebRTCICE, MessageTransferStarted, MessageTransferProgress, MessageTransferInterrupted, MessageTransferRetryable, MessageTransferCompleted, MessageTransferFailed, MessageTransferCancelled, MessageTransferExpired, MessageAgentDisconnected, MessagePing, MessagePong, MessageError:
		return true
	default:
		return false
	}
}
