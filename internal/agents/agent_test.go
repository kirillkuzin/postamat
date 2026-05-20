package agents

import (
	"testing"
	"time"
)

func TestRegisterAgentDeviceBuildsActiveIdentity(t *testing.T) {
	now := time.Date(2026, 5, 20, 15, 0, 0, 0, time.UTC)
	agent, err := RegisterAgentDevice(RegisterAgentDeviceInput{
		AgentID:   "agent-a",
		DeviceID:  "device-laptop",
		PublicKey: "ed25519:placeholder",
		Capabilities: []Capability{
			CapabilitySend,
			CapabilityReceive,
			CapabilityWebRTCP2P,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("RegisterAgentDevice returned error: %v", err)
	}

	if agent.ID != "agent-a" {
		t.Fatalf("agent id = %q", agent.ID)
	}
	if agent.Device.ID != "device-laptop" {
		t.Fatalf("device id = %q", agent.Device.ID)
	}
	if agent.Device.PublicKey != "ed25519:placeholder" {
		t.Fatalf("public key = %q", agent.Device.PublicKey)
	}
	if agent.State != StateActive || agent.Device.State != StateActive {
		t.Fatalf("expected active agent/device, got %q/%q", agent.State, agent.Device.State)
	}
	if !agent.Device.HasCapability(CapabilityReceive) || !agent.Device.HasCapability(CapabilityWebRTCP2P) {
		t.Fatalf("expected receive and webrtc capabilities: %#v", agent.Device.Capabilities)
	}
	if !agent.CreatedAt.Equal(now) || !agent.Device.RegisteredAt.Equal(now) {
		t.Fatalf("timestamps were not set from input time")
	}
}

func TestRegisterAgentDeviceValidatesRequiredFields(t *testing.T) {
	tests := []struct {
		name  string
		input RegisterAgentDeviceInput
		err   error
	}{
		{name: "agent id", input: RegisterAgentDeviceInput{DeviceID: "device", PublicKey: "key", Capabilities: []Capability{CapabilitySend}}, err: ErrAgentIDRequired},
		{name: "device id", input: RegisterAgentDeviceInput{AgentID: "agent", PublicKey: "key", Capabilities: []Capability{CapabilitySend}}, err: ErrDeviceIDRequired},
		{name: "public key", input: RegisterAgentDeviceInput{AgentID: "agent", DeviceID: "device", Capabilities: []Capability{CapabilitySend}}, err: ErrPublicKeyRequired},
		{name: "capabilities", input: RegisterAgentDeviceInput{AgentID: "agent", DeviceID: "device", PublicKey: "key"}, err: ErrCapabilitiesRequired},
		{name: "unknown capability", input: RegisterAgentDeviceInput{AgentID: "agent", DeviceID: "device", PublicKey: "key", Capabilities: []Capability{"teleport"}}, err: ErrUnsupportedCapability},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RegisterAgentDevice(tc.input)
			if err != tc.err {
				t.Fatalf("expected %v, got %v", tc.err, err)
			}
		})
	}
}

func TestAgentDeviceCanBeRevoked(t *testing.T) {
	now := time.Date(2026, 5, 20, 16, 0, 0, 0, time.UTC)
	agent, err := RegisterAgentDevice(RegisterAgentDeviceInput{
		AgentID:      "agent-a",
		DeviceID:     "device-laptop",
		PublicKey:    "key",
		Capabilities: []Capability{CapabilitySend},
		Now:          now,
	})
	if err != nil {
		t.Fatalf("RegisterAgentDevice returned error: %v", err)
	}

	revokedAt := now.Add(time.Hour)
	agent.Device.Revoke(revokedAt)
	if agent.Device.State != StateRevoked {
		t.Fatalf("expected revoked device, got %q", agent.Device.State)
	}
	if agent.Device.RevokedAt == nil || !agent.Device.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoked_at was not recorded")
	}
}
