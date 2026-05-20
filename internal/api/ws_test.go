package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/api"
	"github.com/kirillkuzin/postamat/internal/sessions"
	"github.com/kirillkuzin/postamat/internal/signaling"
)

func TestAgentWebSocketRegistersPresenceAfterValidatedHello(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	presence := signaling.NewPresenceRegistry(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	rooms := signaling.NewRoomManager(presence, nil)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, presence, rooms))
	defer server.Close()

	conn := dialWS(t, agentWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_1"})
	readEnvelope(t, conn)

	presenceSnapshot, ok := presence.Agent("agent_a")
	if !ok || !presenceSnapshot.Online {
		t.Fatalf("expected agent_a online, got %+v ok=%v", presenceSnapshot, ok)
	}
	if _, ok := presenceSnapshot.Devices["dev_1"]; !ok {
		t.Fatalf("expected dev_1 registered: %+v", presenceSnapshot.Devices)
	}
}

func TestAgentWebSocketRejectsMissingTicket(t *testing.T) {
	server := httptest.NewServer(newTestRouter())
	defer server.Close()

	_, response, err := websocket.DefaultDialer.Dial(wsURL(server.URL+"/api/v1/agent/ws"), nil)
	if err == nil {
		t.Fatalf("expected websocket dial to fail without ticket")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got response=%+v err=%v", response, err)
	}
}

func TestAgentWebSocketRejectsInvalidTicket(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	_, response, err := websocket.DefaultDialer.Dial(wsURL(server.URL+"/api/v1/agent/ws?transfer_id="+created.Transfer.ID+"&ticket=wrong"), nil)
	if err == nil {
		t.Fatalf("expected websocket dial to fail with invalid ticket")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got response=%+v err=%v", response, err)
	}
}

func TestAgentWebSocketRejectsHelloForAgentOutsideTransfer(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	conn := dialWS(t, agentWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_x", DeviceID: "dev_x"})
	got := readEnvelope(t, conn)
	if got.Type != signaling.MessageError {
		t.Fatalf("expected error for spoofed hello, got %+v", got)
	}
}

func TestAgentWebSocketRejectsSpoofedOutboundSender(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	conn := dialWS(t, agentWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_a"})
	readEnvelope(t, conn)
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: created.Transfer.ID, FromAgentID: "agent_x", ToAgentID: "agent_b"})
	got := readEnvelope(t, conn)
	if got.Type != signaling.MessageError {
		t.Fatalf("expected spoofed outbound sender error, got %+v", got)
	}
}

func TestAgentWebSocketRejectsOmittedOutboundSender(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	conn := dialWS(t, agentWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_a"})
	readEnvelope(t, conn)
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: created.Transfer.ID, ToAgentID: "agent_b"})
	got := readEnvelope(t, conn)
	if got.Type != signaling.MessageError {
		t.Fatalf("expected omitted outbound sender error, got %+v", got)
	}
}

func TestAgentWebSocketRejectsOutboundTargetOutsideTransfer(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	conn := dialWS(t, agentWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_a"})
	readEnvelope(t, conn)
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: created.Transfer.ID, FromAgentID: "agent_a", ToAgentID: "agent_x"})
	got := readEnvelope(t, conn)
	if got.Type != signaling.MessageError {
		t.Fatalf("expected outbound target error, got %+v", got)
	}
}

func TestAgentWebSocketReceivesRoutedOffer(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	presence := signaling.NewPresenceRegistry(nil)
	rooms := signaling.NewRoomManager(presence, nil)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, presence, rooms))
	defer server.Close()

	conn := dialWS(t, agentWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_b", DeviceID: "dev_b"})
	readEnvelope(t, conn)

	if err := rooms.Route(signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: created.Transfer.ID, FromAgentID: "agent_a", ToAgentID: "agent_b"}); err != nil {
		t.Fatalf("route offer: %v", err)
	}

	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read routed offer: %v", err)
	}
	var got signaling.Envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode routed offer: %v", err)
	}
	if got.Type != signaling.MessageTransferOffer || got.TransferID != created.Transfer.ID {
		t.Fatalf("unexpected routed envelope: %+v", got)
	}
}

func TestBrowserReceiverWebSocketEndpointAcceptsTokenScopedPath(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	presence := signaling.NewPresenceRegistry(nil)
	rooms := signaling.NewRoomManager(presence, nil)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, presence, rooms))
	defer server.Close()

	conn := dialWS(t, browserWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_1"})
	ack := readEnvelope(t, conn)
	if ack.Type != signaling.MessageAgentPresence {
		t.Fatalf("expected presence ack, got %+v", ack)
	}
}

func TestBrowserReceiverWebSocketRejectsMalformedPath(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	_, response, err := websocket.DefaultDialer.Dial(wsURL(server.URL+"/api/public/transfers/"+url.QueryEscape(created.PublicToken)+"/extra/receiver/ws?ticket="+url.QueryEscape(created.PublicToken)), nil)
	if err == nil {
		t.Fatalf("expected websocket dial to fail for malformed path")
	}
	if response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got response=%+v err=%v", response, err)
	}
}

func TestBrowserReceiverWebSocketDoesNotAcceptArbitraryAgentIdentity(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	presence := signaling.NewPresenceRegistry(nil)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, presence, nil))
	defer server.Close()

	conn := dialWS(t, browserWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "browser_1"})
	ack := readEnvelope(t, conn)
	if ack.Type != signaling.MessageAgentPresence || ack.AgentID != "browser_recipient" {
		t.Fatalf("expected browser recipient identity, got %+v", ack)
	}
	if _, ok := presence.Agent("agent_a"); ok {
		t.Fatalf("browser receiver must not register as arbitrary agent")
	}
}

func TestBrowserReceiverWebSocketCannotTargetArbitraryAgent(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	conn := dialWS(t, browserWSURL(server.URL, created))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_1"})
	readEnvelope(t, conn)
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageWebRTCICE, TransferID: created.Transfer.ID, FromAgentID: "browser_recipient", ToAgentID: "agent_x"})
	got := readEnvelope(t, conn)
	if got.Type != signaling.MessageError {
		t.Fatalf("expected arbitrary target error, got %+v", got)
	}
}

func testService() *sessions.Service {
	return sessions.NewService(sessions.NewMemoryRepository(), &fixedTokenIssuer{}, nil)
}

func secureTestService() *sessions.Service {
	return sessions.NewService(sessions.NewMemoryRepository(), sessions.RandomTokenIssuer{}, nil)
}

func createWSTransfer(t *testing.T, service *sessions.Service, target string) sessions.CreateTransferResult {
	t.Helper()
	input := sessions.CreateTransferInput{
		FromAgentID:   "agent_a",
		ToAgentID:     "agent_b",
		Target:        target,
		FileName:      "report.pdf",
		FileSizeBytes: 42,
	}
	if target == sessions.TargetBrowserLink {
		input.ToAgentID = ""
	}
	created, err := service.CreateTransfer(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	return created
}

func agentWSURL(baseURL string, created sessions.CreateTransferResult) string {
	return baseURL + "/api/v1/agent/ws?transfer_id=" + url.QueryEscape(created.Transfer.ID) + "&ticket=" + url.QueryEscape(created.AgentTicket)
}

func browserWSURL(baseURL string, created sessions.CreateTransferResult) string {
	return baseURL + "/api/public/transfers/" + url.PathEscape(created.PublicToken) + "/receiver/ws?ticket=" + url.QueryEscape(created.PublicToken)
}

func dialWS(t *testing.T, rawURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(rawURL), nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	return conn
}

func writeEnvelope(t *testing.T, conn *websocket.Conn, envelope signaling.Envelope) {
	t.Helper()
	if err := conn.WriteJSON(envelope); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) signaling.Envelope {
	t.Helper()
	var envelope signaling.Envelope
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	return envelope
}

func wsURL(rawURL string) string {
	return "ws" + rawURL[len("http"):]
}
