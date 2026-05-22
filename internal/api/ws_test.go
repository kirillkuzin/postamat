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

func TestAuthenticatedAgentWebSocketRequiresBearerToken(t *testing.T) {
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(secureTestService(), signaling.NewPresenceRegistry(nil), nil, testAgentAuth(t, map[string]string{"agent_b": "token-b"})))
	defer server.Close()

	_, response, err := websocket.DefaultDialer.Dial(wsURL(server.URL+"/api/v1/agents/ws?agent_id=agent_b"), nil)
	if err == nil {
		t.Fatalf("expected websocket dial to fail without bearer token")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got response=%+v err=%v", response, err)
	}
}

func TestAuthenticatedAgentWebSocketRejectsSpoofedHelloAgent(t *testing.T) {
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(secureTestService(), signaling.NewPresenceRegistry(nil), nil, testAgentAuth(t, map[string]string{"agent_b": "token-b"})))
	defer server.Close()

	conn := dialAuthenticatedAgentWS(t, server.URL, "agent_b", "token-b")
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_x", DeviceID: "dev_x"})
	if got := readEnvelope(t, conn); got.Type != signaling.MessageError {
		t.Fatalf("expected spoofed hello error, got %+v", got)
	}
}

func TestAuthenticatedAgentWebSocketRegistersAlwaysOnPresenceAndReceivesOffer(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	presence := signaling.NewPresenceRegistry(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	rooms := signaling.NewRoomManager(presence, nil)
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(service, presence, rooms, testAgentAuth(t, map[string]string{"agent_b": "token-b"})))
	defer server.Close()

	receiver := dialAuthenticatedAgentWS(t, server.URL, "agent_b", "token-b")
	defer receiver.Close()
	writeEnvelope(t, receiver, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_b", DeviceID: "dev_b"})
	ack := readEnvelope(t, receiver)
	if ack.Type != signaling.MessageAgentPresence || ack.AgentID != "agent_b" {
		t.Fatalf("unexpected presence ack: %+v", ack)
	}
	if snapshot, ok := presence.Agent("agent_b"); !ok || !snapshot.Online {
		t.Fatalf("expected agent_b always-on presence, got %+v ok=%v", snapshot, ok)
	}

	sender := dialWS(t, agentWSURL(server.URL, created))
	defer sender.Close()
	writeEnvelope(t, sender, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_a"})
	readEnvelope(t, sender)
	writeEnvelope(t, sender, signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: created.Transfer.ID, FromAgentID: "agent_a", ToAgentID: "agent_b"})

	got := readEnvelope(t, receiver)
	if got.Type != signaling.MessageTransferOffer || got.TransferID != created.Transfer.ID || got.FromAgentID != "agent_a" || got.ToAgentID != "agent_b" {
		t.Fatalf("always-on receiver got unexpected offer: %+v", got)
	}
}

func TestAuthenticatedAgentWebSocketRejectsNonParticipantTransferRoute(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(service, signaling.NewPresenceRegistry(nil), nil, testAgentAuth(t, map[string]string{"agent_x": "token-x"})))
	defer server.Close()

	conn := dialAuthenticatedAgentWS(t, server.URL, "agent_x", "token-x")
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_x", DeviceID: "dev_x"})
	readEnvelope(t, conn)
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageTransferAccepted, TransferID: created.Transfer.ID, FromAgentID: "agent_x", ToAgentID: "agent_a"})
	if got := readEnvelope(t, conn); got.Type != signaling.MessageError {
		t.Fatalf("expected non-participant route error, got %+v", got)
	}
}

func TestAuthenticatedAgentWebSocketRejectsRoleReversedOffer(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(service, signaling.NewPresenceRegistry(nil), nil, testAgentAuth(t, map[string]string{"agent_a": "token-a", "agent_b": "token-b"})))
	defer server.Close()

	sender := dialAuthenticatedAgentWS(t, server.URL, "agent_a", "token-a")
	defer sender.Close()
	writeEnvelope(t, sender, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_a"})
	readEnvelope(t, sender)

	conn := dialAuthenticatedAgentWS(t, server.URL, "agent_b", "token-b")
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_b", DeviceID: "dev_b"})
	readEnvelope(t, conn)
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: created.Transfer.ID, FromAgentID: "agent_b", ToAgentID: "agent_a"})
	if got := readEnvelope(t, conn); got.Type != signaling.MessageError {
		t.Fatalf("expected role-reversed offer error, got %+v", got)
	}
}

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

func TestAgentWebSocketRejectsPartialTransferScopedTicket(t *testing.T) {
	server := httptest.NewServer(newTestRouter())
	defer server.Close()

	_, response, err := websocket.DefaultDialer.Dial(wsURL(server.URL+"/api/v1/agent/ws?transfer_id=tr_missing_ticket"), nil)
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
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(service, presence, rooms, testAgentAuth(t, map[string]string{"agent_b": "token-b"})))
	defer server.Close()

	conn := dialAuthenticatedAgentWS(t, server.URL, "agent_b", "token-b")
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

func TestAuthenticatedAgentWebSocketRoutesInterruptedAndRetryableState(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	presence := signaling.NewPresenceRegistry(nil)
	rooms := signaling.NewRoomManager(presence, nil)
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(service, presence, rooms, testAgentAuth(t, map[string]string{"agent_a": "token-a", "agent_b": "token-b"})))
	defer server.Close()

	sender := dialAuthenticatedAgentWS(t, server.URL, "agent_a", "token-a")
	defer sender.Close()
	writeEnvelope(t, sender, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_a"})
	readEnvelope(t, sender)

	receiver := dialAuthenticatedAgentWS(t, server.URL, "agent_b", "token-b")
	defer receiver.Close()
	writeEnvelope(t, receiver, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_b", DeviceID: "dev_b"})
	readEnvelope(t, receiver)

	writeEnvelope(t, sender, signaling.Envelope{Type: signaling.MessageTransferInterrupted, TransferID: created.Transfer.ID, FromAgentID: "agent_a", ToAgentID: "agent_b"})
	if got := readEnvelope(t, receiver); got.Type != signaling.MessageTransferInterrupted || got.TransferID != created.Transfer.ID || got.FromAgentID != "agent_a" || got.ToAgentID != "agent_b" {
		t.Fatalf("receiver got unexpected interrupted envelope: %+v", got)
	}

	writeEnvelope(t, receiver, signaling.Envelope{Type: signaling.MessageTransferRetryable, TransferID: created.Transfer.ID, FromAgentID: "agent_b", ToAgentID: "agent_a"})
	if got := readEnvelope(t, sender); got.Type != signaling.MessageTransferRetryable || got.TransferID != created.Transfer.ID || got.FromAgentID != "agent_b" || got.ToAgentID != "agent_a" {
		t.Fatalf("sender got unexpected retryable envelope: %+v", got)
	}
}

func TestAgentWebSocketRoutesWebRTCOfferFromTicketScopedSenderToAlwaysOnAgent(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetAgent)
	server := httptest.NewServer(api.NewRouterWithSignalingAndAgentAuth(service, signaling.NewPresenceRegistry(nil), nil, testAgentAuth(t, map[string]string{"agent_b": "token-b"})))
	defer server.Close()

	receiver := dialAuthenticatedAgentWS(t, server.URL, "agent_b", "token-b")
	defer receiver.Close()
	writeEnvelope(t, receiver, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_b", DeviceID: "dev_b"})
	readEnvelope(t, receiver)

	sender := dialWS(t, agentWSURL(server.URL, created))
	defer sender.Close()
	writeEnvelope(t, sender, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "dev_a"})
	readEnvelope(t, sender)
	writeEnvelope(t, sender, signaling.Envelope{Type: signaling.MessageWebRTCOffer, TransferID: created.Transfer.ID, FromAgentID: "agent_a", ToAgentID: "agent_b", Payload: json.RawMessage(`{"sdp":{"type":"offer","sdp":"v=0\\r\\n"}}`)})

	got := readEnvelope(t, receiver)
	if got.Type != signaling.MessageWebRTCOffer || got.TransferID != created.Transfer.ID || got.FromAgentID != "agent_a" || got.ToAgentID != "agent_b" {
		t.Fatalf("receiver got unexpected routed WebRTC offer: %+v", got)
	}
}

func TestBrowserReceiverWebSocketEndpointAcceptsTicketScopedPath(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	ticket := issueBrowserReceiverTicket(t, service, created.PublicToken)
	presence := signaling.NewPresenceRegistry(nil)
	rooms := signaling.NewRoomManager(presence, nil)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, presence, rooms))
	defer server.Close()

	conn := dialWS(t, browserWSURL(server.URL, created, ticket))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_1"})
	ack := readEnvelope(t, conn)
	if ack.Type != signaling.MessageAgentPresence || ack.AgentID != browserAgentID(created) {
		t.Fatalf("expected transfer-scoped browser presence ack, got %+v", ack)
	}
}

func TestBrowserReceiverWebSocketRoutingIsTransferScoped(t *testing.T) {
	service := secureTestService()
	first := createWSTransfer(t, service, sessions.TargetBrowserLink)
	second := createWSTransfer(t, service, sessions.TargetBrowserLink)
	firstTicket := issueBrowserReceiverTicket(t, service, first.PublicToken)
	secondTicket := issueBrowserReceiverTicket(t, service, second.PublicToken)
	presence := signaling.NewPresenceRegistry(nil)
	rooms := signaling.NewRoomManager(presence, nil)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, presence, rooms))
	defer server.Close()

	firstBrowser := dialWS(t, browserWSURL(server.URL, first, firstTicket))
	defer firstBrowser.Close()
	writeEnvelope(t, firstBrowser, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_1"})
	readEnvelope(t, firstBrowser)

	secondBrowser := dialWS(t, browserWSURL(server.URL, second, secondTicket))
	defer secondBrowser.Close()
	writeEnvelope(t, secondBrowser, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_2"})
	readEnvelope(t, secondBrowser)

	agent := dialWS(t, agentWSURL(server.URL, first))
	defer agent.Close()
	writeEnvelope(t, agent, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "agent_dev"})
	readEnvelope(t, agent)
	writeEnvelope(t, agent, signaling.Envelope{Type: signaling.MessageTransferOffer, TransferID: first.Transfer.ID, FromAgentID: "agent_a", ToAgentID: browserAgentID(first)})

	got := readEnvelope(t, firstBrowser)
	if got.Type != signaling.MessageTransferOffer || got.TransferID != first.Transfer.ID || got.ToAgentID != browserAgentID(first) {
		t.Fatalf("first browser got unexpected envelope: %+v", got)
	}
	_ = secondBrowser.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var leaked signaling.Envelope
	if err := secondBrowser.ReadJSON(&leaked); err == nil {
		t.Fatalf("second browser received cross-transfer envelope: %+v", leaked)
	}
	_ = secondBrowser.SetReadDeadline(time.Time{})
}

func TestBrowserReceiverWebSocketRoutesBackToTransferScopedSenderSocket(t *testing.T) {
	service := secureTestService()
	first := createWSTransfer(t, service, sessions.TargetBrowserLink)
	second := createWSTransfer(t, service, sessions.TargetBrowserLink)
	secondTicket := issueBrowserReceiverTicket(t, service, second.PublicToken)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	firstAgent := dialWS(t, agentWSURL(server.URL, first))
	defer firstAgent.Close()
	writeEnvelope(t, firstAgent, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "agent_first"})
	readEnvelope(t, firstAgent)

	secondAgent := dialWS(t, agentWSURL(server.URL, second))
	defer secondAgent.Close()
	writeEnvelope(t, secondAgent, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "agent_second"})
	readEnvelope(t, secondAgent)

	secondBrowser := dialWS(t, browserWSURL(server.URL, second, secondTicket))
	defer secondBrowser.Close()
	writeEnvelope(t, secondBrowser, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_second"})
	readEnvelope(t, secondBrowser)
	writeEnvelope(t, secondBrowser, signaling.Envelope{Type: signaling.MessageTransferAccepted, TransferID: second.Transfer.ID, FromAgentID: browserAgentID(second), ToAgentID: "agent_a"})

	got := readEnvelope(t, secondAgent)
	if got.Type != signaling.MessageTransferAccepted || got.TransferID != second.Transfer.ID || got.FromAgentID != browserAgentID(second) {
		t.Fatalf("second agent got unexpected envelope: %+v", got)
	}
	_ = firstAgent.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var leaked signaling.Envelope
	if err := firstAgent.ReadJSON(&leaked); err == nil {
		t.Fatalf("first agent received cross-transfer browser response: %+v", leaked)
	}
	_ = firstAgent.SetReadDeadline(time.Time{})
}

func TestBrowserReceiverWebSocketDoesNotFallbackToOtherTransferSenderSocket(t *testing.T) {
	service := secureTestService()
	first := createWSTransfer(t, service, sessions.TargetBrowserLink)
	second := createWSTransfer(t, service, sessions.TargetBrowserLink)
	firstTicket := issueBrowserReceiverTicket(t, service, first.PublicToken)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	secondAgent := dialWS(t, agentWSURL(server.URL, second))
	defer secondAgent.Close()
	writeEnvelope(t, secondAgent, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "agent_second"})
	readEnvelope(t, secondAgent)

	firstBrowser := dialWS(t, browserWSURL(server.URL, first, firstTicket))
	defer firstBrowser.Close()
	writeEnvelope(t, firstBrowser, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_first"})
	readEnvelope(t, firstBrowser)
	writeEnvelope(t, firstBrowser, signaling.Envelope{Type: signaling.MessageTransferAccepted, TransferID: first.Transfer.ID, FromAgentID: browserAgentID(first), ToAgentID: "agent_a"})

	got := readEnvelope(t, firstBrowser)
	if got.Type != signaling.MessageError {
		t.Fatalf("expected offline error when matching sender transfer socket is absent, got %+v", got)
	}
	_ = secondAgent.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var leaked signaling.Envelope
	if err := secondAgent.ReadJSON(&leaked); err == nil {
		t.Fatalf("second transfer sender received fallback-routed message: %+v", leaked)
	}
	_ = secondAgent.SetReadDeadline(time.Time{})
}

func TestBrowserReceiverWebSocketRejectsEmptyTransferRoutedMessages(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	ticket := issueBrowserReceiverTicket(t, service, created.PublicToken)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	conn := dialWS(t, browserWSURL(server.URL, created, ticket))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "browser_recipient", DeviceID: "browser_1"})
	readEnvelope(t, conn)
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageError, FromAgentID: browserAgentID(created), ToAgentID: "agent_a"})
	got := readEnvelope(t, conn)
	if got.Type != signaling.MessageError {
		t.Fatalf("expected empty-transfer message to be rejected, got %+v", got)
	}
}

func TestBrowserReceiverWebSocketRejectsMalformedPath(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	_, response, err := websocket.DefaultDialer.Dial(wsURL(server.URL+"/api/public/transfers/"+url.QueryEscape(created.PublicToken)+"/extra/receiver/ws?ticket=wrong"), nil)
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
	ticket := issueBrowserReceiverTicket(t, service, created.PublicToken)
	presence := signaling.NewPresenceRegistry(nil)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, presence, nil))
	defer server.Close()

	conn := dialWS(t, browserWSURL(server.URL, created, ticket))
	defer conn.Close()
	writeEnvelope(t, conn, signaling.Envelope{Type: signaling.MessageAgentHello, AgentID: "agent_a", DeviceID: "browser_1"})
	ack := readEnvelope(t, conn)
	if ack.Type != signaling.MessageAgentPresence || ack.AgentID != browserAgentID(created) {
		t.Fatalf("expected transfer-scoped browser recipient identity, got %+v", ack)
	}
	if _, ok := presence.Agent("agent_a"); ok {
		t.Fatalf("browser receiver must not register as arbitrary agent")
	}
}

func TestBrowserReceiverWebSocketCannotTargetArbitraryAgent(t *testing.T) {
	service := secureTestService()
	created := createWSTransfer(t, service, sessions.TargetBrowserLink)
	ticket := issueBrowserReceiverTicket(t, service, created.PublicToken)
	server := httptest.NewServer(api.NewRouterWithSignaling(service, signaling.NewPresenceRegistry(nil), nil))
	defer server.Close()

	conn := dialWS(t, browserWSURL(server.URL, created, ticket))
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

func browserWSURL(baseURL string, created sessions.CreateTransferResult, receiverTicket string) string {
	return baseURL + "/api/public/transfers/" + url.PathEscape(created.PublicToken) + "/receiver/ws?ticket=" + url.QueryEscape(receiverTicket)
}

func browserAgentID(created sessions.CreateTransferResult) string {
	return "browser_recipient:" + created.Transfer.ID
}

func issueBrowserReceiverTicket(t *testing.T, service *sessions.Service, publicToken string) string {
	t.Helper()
	issued, err := service.IssueBrowserReceiverTicket(context.Background(), publicToken, sessions.ReceiverConsent{Accepted: true})
	if err != nil {
		t.Fatalf("IssueBrowserReceiverTicket: %v", err)
	}
	return issued.ReceiverTicket
}

func testAgentAuth(t *testing.T, tokens map[string]string) api.AgentAuthenticator {
	t.Helper()
	authenticator, err := api.NewStaticAgentTokenAuthenticator(tokens, "test-pepper")
	if err != nil {
		t.Fatalf("NewStaticAgentTokenAuthenticator: %v", err)
	}
	return authenticator
}

func dialAuthenticatedAgentWS(t *testing.T, baseURL string, agentID string, token string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(baseURL+"/api/v1/agents/ws?agent_id="+url.QueryEscape(agentID)), header)
	if err != nil {
		t.Fatalf("authenticated websocket dial: %v", err)
	}
	return conn
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
