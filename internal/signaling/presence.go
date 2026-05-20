package signaling

import (
	"sync"
	"time"
)

type DevicePresence struct {
	AgentID      string
	DeviceID     string
	Capabilities []string
	LastSeen     time.Time
}

type AgentPresence struct {
	AgentID  string
	Online   bool
	LastSeen time.Time
	Devices  map[string]DevicePresence
}

type PresenceRegistry struct {
	mu     sync.RWMutex
	now    func() time.Time
	agents map[string]map[string]DevicePresence
}

func NewPresenceRegistry(now func() time.Time) *PresenceRegistry {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PresenceRegistry{now: now, agents: make(map[string]map[string]DevicePresence)}
}

func (r *PresenceRegistry) Register(presence DevicePresence) {
	r.mu.Lock()
	defer r.mu.Unlock()
	presence.LastSeen = r.now()
	presence.Capabilities = append([]string(nil), presence.Capabilities...)
	if r.agents[presence.AgentID] == nil {
		r.agents[presence.AgentID] = make(map[string]DevicePresence)
	}
	r.agents[presence.AgentID][presence.DeviceID] = presence
}

func (r *PresenceRegistry) Touch(agentID string, deviceID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	devices := r.agents[agentID]
	if devices == nil {
		return false
	}
	presence, ok := devices[deviceID]
	if !ok {
		return false
	}
	presence.LastSeen = r.now()
	devices[deviceID] = presence
	return true
}

func (r *PresenceRegistry) Disconnect(agentID string, deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	devices := r.agents[agentID]
	delete(devices, deviceID)
	if len(devices) == 0 {
		delete(r.agents, agentID)
	}
}

func (r *PresenceRegistry) Agent(agentID string) (AgentPresence, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	devices := r.agents[agentID]
	if len(devices) == 0 {
		return AgentPresence{}, false
	}
	return cloneAgentPresence(agentID, devices), true
}

func (r *PresenceRegistry) OnlineAgents() []AgentPresence {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make([]AgentPresence, 0, len(r.agents))
	for agentID, devices := range r.agents {
		if len(devices) > 0 {
			agents = append(agents, cloneAgentPresence(agentID, devices))
		}
	}
	return agents
}

func (r *PresenceRegistry) RemoveStale(maxAge time.Duration) []DevicePresence {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-maxAge)
	removed := []DevicePresence{}
	for agentID, devices := range r.agents {
		for deviceID, presence := range devices {
			if presence.LastSeen.Before(cutoff) {
				removed = append(removed, cloneDevicePresence(presence))
				delete(devices, deviceID)
			}
		}
		if len(devices) == 0 {
			delete(r.agents, agentID)
		}
	}
	return removed
}

func cloneAgentPresence(agentID string, devices map[string]DevicePresence) AgentPresence {
	presence := AgentPresence{AgentID: agentID, Online: len(devices) > 0, Devices: make(map[string]DevicePresence, len(devices))}
	for deviceID, device := range devices {
		cloned := cloneDevicePresence(device)
		presence.Devices[deviceID] = cloned
		if presence.LastSeen.IsZero() || cloned.LastSeen.After(presence.LastSeen) {
			presence.LastSeen = cloned.LastSeen
		}
	}
	return presence
}

func cloneDevicePresence(presence DevicePresence) DevicePresence {
	presence.Capabilities = append([]string(nil), presence.Capabilities...)
	return presence
}
