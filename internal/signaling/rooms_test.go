package signaling

import (
	"errors"
	"testing"
	"time"
)

type recordedPeer struct {
	messages []Envelope
}

func (p *recordedPeer) Send(envelope Envelope) error {
	p.messages = append(p.messages, envelope)
	return nil
}

func TestRoomManagerRoutesTransferOfferToOnlineReceivingAgent(t *testing.T) {
	registry := NewPresenceRegistry(func() time.Time { return time.Now().UTC() })
	rooms := NewRoomManager(registry, nil)
	receiver := &recordedPeer{}
	rooms.AttachPeer("agent_b", "dev_b", receiver)
	registry.Register(DevicePresence{AgentID: "agent_b", DeviceID: "dev_b"})

	err := rooms.Route(Envelope{Type: MessageTransferOffer, TransferID: "tr_1", FromAgentID: "agent_a", ToAgentID: "agent_b"})
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if len(receiver.messages) != 1 || receiver.messages[0].Type != MessageTransferOffer || receiver.messages[0].TransferID != "tr_1" {
		t.Fatalf("offer not routed to receiver: %+v", receiver.messages)
	}
}

func TestRoomManagerReturnsOfflineWhenTargetAgentHasNoPeer(t *testing.T) {
	rooms := NewRoomManager(NewPresenceRegistry(nil), nil)
	err := rooms.Route(Envelope{Type: MessageTransferOffer, TransferID: "tr_1", FromAgentID: "agent_a", ToAgentID: "agent_b"})
	if !errors.Is(err, ErrAgentOffline) {
		t.Fatalf("expected ErrAgentOffline, got %v", err)
	}
}

func TestRoomManagerRoutesWebRTCToSpecificAgent(t *testing.T) {
	registry := NewPresenceRegistry(nil)
	rooms := NewRoomManager(registry, nil)
	receiver := &recordedPeer{}
	rooms.AttachPeer("agent_b", "dev_b", receiver)
	registry.Register(DevicePresence{AgentID: "agent_b", DeviceID: "dev_b"})

	err := rooms.Route(Envelope{Type: MessageWebRTCICE, TransferID: "tr_1", FromAgentID: "agent_a", ToAgentID: "agent_b"})
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if len(receiver.messages) != 1 || receiver.messages[0].Type != MessageWebRTCICE {
		t.Fatalf("ICE not routed: %+v", receiver.messages)
	}
}

func TestRoomManagerDisconnectReportsActiveTransfers(t *testing.T) {
	registry := NewPresenceRegistry(nil)
	var failed []string
	rooms := NewRoomManager(registry, func(transferID string, agentID string) { failed = append(failed, transferID+":"+agentID) })
	receiver := &recordedPeer{}
	rooms.AttachPeer("agent_b", "dev_b", receiver)
	registry.Register(DevicePresence{AgentID: "agent_b", DeviceID: "dev_b"})
	if err := rooms.Route(Envelope{Type: MessageTransferOffer, TransferID: "tr_1", FromAgentID: "agent_a", ToAgentID: "agent_b"}); err != nil {
		t.Fatalf("route offer: %v", err)
	}

	rooms.Disconnect("agent_b", "dev_b")

	if len(failed) != 1 || failed[0] != "tr_1:agent_b" {
		t.Fatalf("expected active transfer failure callback, got %+v", failed)
	}
	if _, ok := registry.Agent("agent_b"); ok {
		t.Fatalf("agent should be offline after disconnect")
	}
}

func TestRoomManagerDetachPeerDoesNotRemoveNewerSameDeviceConnection(t *testing.T) {
	registry := NewPresenceRegistry(nil)
	rooms := NewRoomManager(registry, nil)
	oldPeer := &recordedPeer{}
	newPeer := &recordedPeer{}
	rooms.AttachPeer("agent_b", "dev_b", oldPeer)
	registry.Register(DevicePresence{AgentID: "agent_b", DeviceID: "dev_b"})
	rooms.AttachPeer("agent_b", "dev_b", newPeer)
	registry.Register(DevicePresence{AgentID: "agent_b", DeviceID: "dev_b"})

	rooms.DetachPeer("agent_b", "dev_b", oldPeer)

	if _, ok := registry.Agent("agent_b"); !ok {
		t.Fatal("old peer detach should not mark newer same-device connection offline")
	}
	if err := rooms.Route(Envelope{Type: MessageTransferOffer, TransferID: "tr_2", FromAgentID: "agent_a", ToAgentID: "agent_b"}); err != nil {
		t.Fatalf("route after old peer detach: %v", err)
	}
	if len(newPeer.messages) != 1 || newPeer.messages[0].TransferID != "tr_2" {
		t.Fatalf("new peer should receive routed message, got %+v", newPeer.messages)
	}
	if len(oldPeer.messages) != 0 {
		t.Fatalf("old peer should not receive routed message after detach: %+v", oldPeer.messages)
	}
}

func TestRoomManagerDetachPeerRemovesOnlyDisconnectedDevicePresence(t *testing.T) {
	registry := NewPresenceRegistry(nil)
	rooms := NewRoomManager(registry, nil)
	rooms.AttachPeer("agent_b", "dev_1", &recordedPeer{})
	registry.Register(DevicePresence{AgentID: "agent_b", DeviceID: "dev_1"})
	rooms.AttachPeer("agent_b", "dev_2", &recordedPeer{})
	registry.Register(DevicePresence{AgentID: "agent_b", DeviceID: "dev_2"})

	rooms.Disconnect("agent_b", "dev_1")

	presence, ok := registry.Agent("agent_b")
	if !ok || !presence.Online {
		t.Fatalf("agent should remain online with dev_2: %+v ok=%v", presence, ok)
	}
	if _, ok := presence.Devices["dev_1"]; ok {
		t.Fatalf("dev_1 should be removed: %+v", presence.Devices)
	}
	if _, ok := presence.Devices["dev_2"]; !ok {
		t.Fatalf("dev_2 should remain: %+v", presence.Devices)
	}
}
