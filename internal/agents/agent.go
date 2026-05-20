package agents

import (
	"errors"
	"time"
)

type Capability string

const (
	CapabilitySend      Capability = "send"
	CapabilityReceive   Capability = "receive"
	CapabilityWebRTCP2P Capability = "webrtc_p2p"
)

type State string

const (
	StateActive  State = "active"
	StateRevoked State = "revoked"
)

var (
	ErrAgentIDRequired       = errors.New("agent id is required")
	ErrDeviceIDRequired      = errors.New("device id is required")
	ErrPublicKeyRequired     = errors.New("public key is required")
	ErrCapabilitiesRequired  = errors.New("capabilities are required")
	ErrUnsupportedCapability = errors.New("unsupported capability")
)

type RegisterAgentDeviceInput struct {
	AgentID      string
	DeviceID     string
	PublicKey    string
	Capabilities []Capability
	Now          time.Time
}

type Agent struct {
	ID        string
	State     State
	Device    Device
	CreatedAt time.Time
}

type Device struct {
	ID           string
	PublicKey    string
	Capabilities []Capability
	State        State
	RegisteredAt time.Time
	RevokedAt    *time.Time
}

func RegisterAgentDevice(input RegisterAgentDeviceInput) (Agent, error) {
	if input.AgentID == "" {
		return Agent{}, ErrAgentIDRequired
	}
	if input.DeviceID == "" {
		return Agent{}, ErrDeviceIDRequired
	}
	if input.PublicKey == "" {
		return Agent{}, ErrPublicKeyRequired
	}
	if len(input.Capabilities) == 0 {
		return Agent{}, ErrCapabilitiesRequired
	}
	capabilities := make([]Capability, 0, len(input.Capabilities))
	seen := make(map[Capability]bool, len(input.Capabilities))
	for _, capability := range input.Capabilities {
		if !isSupportedCapability(capability) {
			return Agent{}, ErrUnsupportedCapability
		}
		if seen[capability] {
			continue
		}
		seen[capability] = true
		capabilities = append(capabilities, capability)
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return Agent{
		ID:        input.AgentID,
		State:     StateActive,
		CreatedAt: now,
		Device: Device{
			ID:           input.DeviceID,
			PublicKey:    input.PublicKey,
			Capabilities: capabilities,
			State:        StateActive,
			RegisteredAt: now,
		},
	}, nil
}

func (d Device) HasCapability(capability Capability) bool {
	for _, candidate := range d.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func (d *Device) Revoke(at time.Time) {
	d.State = StateRevoked
	d.RevokedAt = cloneTime(at)
}

func isSupportedCapability(capability Capability) bool {
	switch capability {
	case CapabilitySend, CapabilityReceive, CapabilityWebRTCP2P:
		return true
	default:
		return false
	}
}

func cloneTime(at time.Time) *time.Time {
	cloned := at
	return &cloned
}
