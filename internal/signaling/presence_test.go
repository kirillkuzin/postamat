package signaling

import (
	"reflect"
	"testing"
	"time"
)

func TestPresenceRegistryRegistersMultipleDevicesPerAgent(t *testing.T) {
	now := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	registry := NewPresenceRegistry(func() time.Time { return now })

	registry.Register(DevicePresence{AgentID: "agent_a", DeviceID: "dev_1", Capabilities: []string{"send_files"}})
	now = now.Add(time.Minute)
	registry.Register(DevicePresence{AgentID: "agent_a", DeviceID: "dev_2", Capabilities: []string{"receive_files"}})

	presence, ok := registry.Agent("agent_a")
	if !ok || !presence.Online {
		t.Fatalf("expected agent online, got %+v ok=%v", presence, ok)
	}
	if len(presence.Devices) != 2 {
		t.Fatalf("expected two devices, got %+v", presence.Devices)
	}
	if !reflect.DeepEqual(presence.Devices["dev_1"].Capabilities, []string{"send_files"}) {
		t.Fatalf("capabilities not preserved: %+v", presence.Devices["dev_1"])
	}
	if !presence.LastSeen.Equal(now) {
		t.Fatalf("last seen should track newest device: %s", presence.LastSeen)
	}
}

func TestPresenceRegistryDisconnectsSingleDeviceAndKeepsAgentOnline(t *testing.T) {
	registry := NewPresenceRegistry(func() time.Time { return time.Now().UTC() })
	registry.Register(DevicePresence{AgentID: "agent_a", DeviceID: "dev_1"})
	registry.Register(DevicePresence{AgentID: "agent_a", DeviceID: "dev_2"})

	registry.Disconnect("agent_a", "dev_1")

	presence, ok := registry.Agent("agent_a")
	if !ok || !presence.Online || len(presence.Devices) != 1 {
		t.Fatalf("expected one remaining online device, got %+v ok=%v", presence, ok)
	}
	if _, ok := presence.Devices["dev_2"]; !ok {
		t.Fatalf("expected dev_2 to remain: %+v", presence.Devices)
	}
}

func TestPresenceRegistryRemovesStaleDevices(t *testing.T) {
	now := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	registry := NewPresenceRegistry(func() time.Time { return now })
	registry.Register(DevicePresence{AgentID: "agent_a", DeviceID: "dev_1"})
	now = now.Add(10 * time.Minute)
	registry.Register(DevicePresence{AgentID: "agent_a", DeviceID: "dev_2"})

	removed := registry.RemoveStale(5 * time.Minute)

	if len(removed) != 1 || removed[0].DeviceID != "dev_1" {
		t.Fatalf("unexpected removed devices: %+v", removed)
	}
	presence, ok := registry.Agent("agent_a")
	if !ok || !presence.Online || len(presence.Devices) != 1 {
		t.Fatalf("expected fresh device to keep agent online: %+v ok=%v", presence, ok)
	}
}
