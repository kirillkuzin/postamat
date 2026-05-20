package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/api"
	"github.com/kirillkuzin/postamat/internal/signaling"
)

func TestGetAgentUsesPresenceRegistry(t *testing.T) {
	presence := signaling.NewPresenceRegistry(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	presence.Register(signaling.DevicePresence{AgentID: "agent_a", DeviceID: "dev_1", Capabilities: []string{"send_files"}})
	handler := api.NewRouterWithSignaling(testService(), presence, signaling.NewRoomManager(presence, nil))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent_a", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET agent status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var got struct {
		AgentID  string `json:"agent_id"`
		Status   string `json:"status"`
		Devices  int    `json:"devices"`
		LastSeen string `json:"last_seen"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.AgentID != "agent_a" || got.Status != "online" || got.Devices != 1 || got.LastSeen == "" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestListOnlineAgentsUsesPresenceRegistry(t *testing.T) {
	presence := signaling.NewPresenceRegistry(nil)
	presence.Register(signaling.DevicePresence{AgentID: "agent_a", DeviceID: "dev_1"})
	handler := api.NewRouterWithSignaling(testService(), presence, signaling.NewRoomManager(presence, nil))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/agents?status=online", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET agents status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var got struct {
		Agents []struct {
			AgentID string `json:"agent_id"`
			Status  string `json:"status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Agents) != 1 || got.Agents[0].AgentID != "agent_a" || got.Agents[0].Status != "online" {
		t.Fatalf("unexpected agents response: %+v", got)
	}
}
