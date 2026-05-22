package signaling

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeEnvelopeRejectsUnsupportedType(t *testing.T) {
	_, err := DecodeEnvelope([]byte(`{"type":"unknown","transfer_id":"tr_1"}`))
	if !errors.Is(err, ErrUnsupportedMessageType) {
		t.Fatalf("expected ErrUnsupportedMessageType, got %v", err)
	}
}

func TestDecodeEnvelopeRequiresAgentForHello(t *testing.T) {
	_, err := DecodeEnvelope([]byte(`{"type":"agent.hello","device_id":"dev_1"}`))
	if !errors.Is(err, ErrAgentIDRequired) {
		t.Fatalf("expected ErrAgentIDRequired, got %v", err)
	}
}

func TestDecodeEnvelopeRequiresTransferForTransferMessages(t *testing.T) {
	for _, messageType := range []MessageType{MessageTransferAccepted, MessageTransferInterrupted, MessageTransferRetryable} {
		raw, err := json.Marshal(Envelope{Type: messageType, AgentID: "agent_b"})
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		_, err = DecodeEnvelope(raw)
		if !errors.Is(err, ErrTransferIDRequired) {
			t.Fatalf("%s expected ErrTransferIDRequired, got %v", messageType, err)
		}
	}
}

func TestDecodeEnvelopeAcceptsWebRTCOfferPayload(t *testing.T) {
	raw := []byte(`{"type":"webrtc.offer","transfer_id":"tr_1","from_agent_id":"agent_a","to_agent_id":"agent_b","payload":{"sdp":"offer"}}`)
	envelope, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("DecodeEnvelope returned error: %v", err)
	}
	if envelope.Type != MessageWebRTCOffer || envelope.TransferID != "tr_1" || envelope.FromAgentID != "agent_a" || envelope.ToAgentID != "agent_b" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	var payload map[string]string
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("payload is not JSON object: %v", err)
	}
	if payload["sdp"] != "offer" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
