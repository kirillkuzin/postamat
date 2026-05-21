package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kirillkuzin/postamat/internal/app"
	"github.com/kirillkuzin/postamat/internal/signaling"
)

func TestRunCLIHelpReturnsUsage(t *testing.T) {
	var stdout strings.Builder
	if err := app.Run(context.Background(), app.Options{Name: "cli", Args: []string{"help"}, Stdout: &stdout}); err != nil {
		t.Fatalf("Run(cli help) returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "postamat send") || !strings.Contains(stdout.String(), "mcp") {
		t.Fatalf("help output missing CLI commands:\n%s", stdout.String())
	}
}

func TestRunServerServesHealthAndMetricsUntilContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		errs <- app.Run(ctx, app.Options{Name: "server", ListenAddress: "127.0.0.1:0", TokenPepper: "test-pepper", Ready: ready})
	}()

	var addr string
	select {
	case addr = <-ready:
	case err := <-errs:
		t.Fatalf("server exited before ready: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not become ready")
	}

	assertHTTPBody(t, "http://"+addr+"/healthz", "ok\n")
	assertHTTPBodyContains(t, "http://"+addr+"/metrics", "postamat_build_info")

	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("server returned error after context cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancel")
	}
}

func TestRunServerRequiresTokenPepper(t *testing.T) {
	t.Setenv("POSTAMAT_TOKEN_PEPPER", "")
	err := app.Run(context.Background(), app.Options{Name: "server", ListenAddress: "127.0.0.1:0"})
	if err == nil || !strings.Contains(err.Error(), "POSTAMAT_TOKEN_PEPPER") {
		t.Fatalf("Run(server) error = %v, want POSTAMAT_TOKEN_PEPPER requirement", err)
	}
}

func TestRunAgentdCanAttachLocalSendsToBackend(t *testing.T) {
	messages := make(chan signaling.Envelope, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/transfers":
			if req.Method != http.MethodPost {
				t.Fatalf("unexpected backend method %s for %s", req.Method, req.URL.Path)
			}
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode backend request: %v", err)
			}
			if payload["from_agent_id"] != "agent-a" || payload["to_agent_id"] != "agent-b" {
				t.Fatalf("unexpected backend payload: %+v", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"transfer_id":"tr_app","agent_ticket":"ticket_app"}`))
		case "/api/v1/agent/ws":
			conn, err := upgrader.Upgrade(w, req, nil)
			if err != nil {
				t.Fatalf("upgrade backend ws: %v", err)
			}
			defer conn.Close()
			for i := 0; i < 2; i++ {
				var envelope signaling.Envelope
				if err := conn.ReadJSON(&envelope); err != nil {
					t.Fatalf("read backend signaling message %d: %v", i, err)
				}
				messages <- envelope
			}
		default:
			t.Fatalf("unexpected backend request %s %s", req.Method, req.URL.Path)
		}
	}))
	defer backend.Close()

	socketPath := privateSocketPathForApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		errs <- app.Run(ctx, app.Options{Name: "agentd", LocalSocketPath: socketPath, BackendURL: backend.URL, AgentID: "agent-a", DeviceID: "dev-1", Ready: ready})
	}()
	select {
	case <-ready:
	case err := <-errs:
		t.Fatalf("agentd exited before ready: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("agentd did not become ready")
	}

	var stdout bytes.Buffer
	filePath := writeTempPayload(t, "payload.txt", "hello backend")
	if err := app.Run(context.Background(), app.Options{Name: "cli", Args: []string{"send", filePath, "--to-agent", "agent-b"}, LocalSocketPath: socketPath, Stdout: &stdout}); err != nil {
		t.Fatalf("postamat send returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"status":"offered"`) || !strings.Contains(stdout.String(), `"transfer_id":"tr_app"`) {
		t.Fatalf("send output not attached to backend transfer:\n%s", stdout.String())
	}
	gotHello := readAppSignalingMessage(t, messages, "hello")
	if gotHello.Type != signaling.MessageAgentHello || gotHello.AgentID != "agent-a" || gotHello.DeviceID != "dev-1" || gotHello.TransferID != "tr_app" {
		t.Fatalf("unexpected backend hello: %+v", gotHello)
	}
	gotOffer := readAppSignalingMessage(t, messages, "offer")
	if gotOffer.Type != signaling.MessageTransferOffer || gotOffer.TransferID != "tr_app" || gotOffer.FromAgentID != "agent-a" || gotOffer.ToAgentID != "agent-b" {
		t.Fatalf("unexpected backend offer: %+v", gotOffer)
	}

	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("agentd returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agentd did not stop after context cancellation")
	}
}

func readAppSignalingMessage(t *testing.T, messages <-chan signaling.Envelope, label string) signaling.Envelope {
	t.Helper()
	select {
	case got := <-messages:
		return got
	case <-time.After(2 * time.Second):
		t.Fatalf("backend signaling loop did not receive %s", label)
		return signaling.Envelope{}
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	err := app.Run(context.Background(), app.Options{Name: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func assertHTTPBody(t *testing.T, url string, want string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test-only local ephemeral listener
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if got := string(data); got != want {
		t.Fatalf("GET %s body = %q, want %q", url, got, want)
	}
}

func assertHTTPBodyContains(t *testing.T, url string, want string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test-only local ephemeral listener
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if body := string(data); !strings.Contains(body, want) {
		t.Fatalf("GET %s body missing %q in:\n%s", url, want, body)
	}
}
