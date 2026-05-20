package signaling

import (
	"errors"
	"sync"
)

var ErrAgentOffline = errors.New("agent offline")

type Peer interface {
	Send(Envelope) error
}

type DisconnectCallback func(transferID string, agentID string)

type RoomManager struct {
	mu           sync.RWMutex
	presence     *PresenceRegistry
	onDisconnect DisconnectCallback
	peers        map[string]map[string]Peer
	active       map[string]map[string]struct{}
}

func NewRoomManager(presence *PresenceRegistry, onDisconnect DisconnectCallback) *RoomManager {
	if presence == nil {
		presence = NewPresenceRegistry(nil)
	}
	return &RoomManager{
		presence:     presence,
		onDisconnect: onDisconnect,
		peers:        make(map[string]map[string]Peer),
		active:       make(map[string]map[string]struct{}),
	}
}

func (m *RoomManager) AttachPeer(agentID string, deviceID string, peer Peer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peers[agentID] == nil {
		m.peers[agentID] = make(map[string]Peer)
	}
	m.peers[agentID][deviceID] = peer
}

func (m *RoomManager) Route(envelope Envelope) error {
	if err := envelope.Validate(); err != nil {
		return err
	}
	targetAgentID := envelope.ToAgentID
	if targetAgentID == "" && envelope.Type == MessageTransferOffer {
		targetAgentID = envelope.ToAgentID
	}
	if targetAgentID == "" {
		return ErrRouteTargetRequired
	}
	peer, ok := m.firstPeer(targetAgentID)
	if !ok {
		return ErrAgentOffline
	}
	if envelope.TransferID != "" {
		m.trackActive(envelope.TransferID, envelope.FromAgentID, envelope.ToAgentID)
	}
	return peer.Send(envelope)
}

func (m *RoomManager) Disconnect(agentID string, deviceID string) {
	m.detach(agentID, deviceID, nil)
}

func (m *RoomManager) DetachPeer(agentID string, deviceID string, peer Peer) {
	m.detach(agentID, deviceID, peer)
}

func (m *RoomManager) detach(agentID string, deviceID string, peer Peer) {
	var affected []string
	removed := false
	m.mu.Lock()
	if devices := m.peers[agentID]; devices != nil {
		if peer == nil || devices[deviceID] == peer {
			delete(devices, deviceID)
			removed = true
		}
		if len(devices) == 0 {
			delete(m.peers, agentID)
		}
	}
	stillOnline := len(m.peers[agentID]) > 0
	if removed && !stillOnline {
		for transferID, agents := range m.active {
			if _, ok := agents[agentID]; ok {
				affected = append(affected, transferID)
				delete(m.active, transferID)
			}
		}
	}
	m.mu.Unlock()

	if removed {
		m.presence.Disconnect(agentID, deviceID)
	}
	if m.onDisconnect != nil {
		for _, transferID := range affected {
			m.onDisconnect(transferID, agentID)
		}
	}
}

func (m *RoomManager) firstPeer(agentID string) (Peer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	devices := m.peers[agentID]
	for _, peer := range devices {
		return peer, true
	}
	return nil, false
}

func (m *RoomManager) trackActive(transferID string, fromAgentID string, toAgentID string) {
	if transferID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[transferID] == nil {
		m.active[transferID] = make(map[string]struct{})
	}
	if fromAgentID != "" {
		m.active[transferID][fromAgentID] = struct{}{}
	}
	if toAgentID != "" {
		m.active[transferID][toAgentID] = struct{}{}
	}
}
