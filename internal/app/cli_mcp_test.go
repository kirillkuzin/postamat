package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/agentd"
	"github.com/kirillkuzin/postamat/internal/app"
)

func TestPostamatCLISendCreatesAgentdJob(t *testing.T) {
	socketPath := startLocalAgentd(t)
	filePath := writeTempPayload(t, "payload.txt", "hello agent")

	var stdout bytes.Buffer
	err := app.Run(context.Background(), app.Options{
		Name:            "cli",
		Args:            []string{"send", filePath, "--to-agent", "agent-b"},
		LocalSocketPath: socketPath,
		Stdout:          &stdout,
	})
	if err != nil {
		t.Fatalf("postamat send returned error: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("send output is not JSON: %v\n%s", err, stdout.String())
	}
	if response["direction"] != "send" || response["status"] != "queued" || response["to_agent_id"] != "agent-b" {
		t.Fatalf("unexpected send response: %#v", response)
	}
	if response["file_name"] != "payload.txt" || response["file_size_bytes"] != float64(len("hello agent")) {
		t.Fatalf("file metadata not inferred from source file: %#v", response)
	}
}

func TestPostamatCLIShareCreatesBrowserLinkJob(t *testing.T) {
	socketPath := startLocalAgentd(t)
	filePath := writeTempPayload(t, "browser.bin", "browser")

	var stdout bytes.Buffer
	err := app.Run(context.Background(), app.Options{
		Name:            "cli",
		Args:            []string{"share", filePath, "--browser-link"},
		LocalSocketPath: socketPath,
		Stdout:          &stdout,
	})
	if err != nil {
		t.Fatalf("postamat share returned error: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("share output is not JSON: %v\n%s", err, stdout.String())
	}
	if response["browser_link"] != true || response["to_agent_id"] != nil {
		t.Fatalf("share should create a browser-link job without target agent: %#v", response)
	}
}

func TestPostamatCLIStatusCancelListAndInboxUseAgentd(t *testing.T) {
	socketPath := startLocalAgentd(t)
	filePath := writeTempPayload(t, "payload.txt", "hello")
	jobID := createJobViaCLI(t, socketPath, filePath)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "status", args: []string{"status", jobID}, want: `"id":"` + jobID + `"`},
		{name: "cancel", args: []string{"cancel", jobID}, want: `"status":"cancelled"`},
		{name: "list", args: []string{"list"}, want: `"jobs"`},
		{name: "inbox", args: []string{"inbox"}, want: `"jobs"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := app.Run(context.Background(), app.Options{Name: "cli", Args: tc.args, LocalSocketPath: socketPath, Stdout: &stdout})
			if err != nil {
				t.Fatalf("postamat %s returned error: %v", tc.name, err)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("output missing %q:\n%s", tc.want, stdout.String())
			}
		})
	}
}

func TestPostamatAgentdCommandServesUnixSocketUntilCancelled(t *testing.T) {
	socketPath := privateSocketPathForApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- app.Run(ctx, app.Options{Name: "agentd", LocalSocketPath: socketPath})
	}()

	waitForSocket(t, socketPath)
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

func TestPostamatMCPListsAndCallsAgentdTools(t *testing.T) {
	socketPath := startLocalAgentd(t)
	filePath := writeTempPayload(t, "mcp.txt", "mcp-data")
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create","arguments":{"source_path":%q,"to_agent_id":"agent-b"}}}`+"\n", filePath)

	var stdout bytes.Buffer
	err := app.Run(context.Background(), app.Options{
		Name:            "cli",
		Args:            []string{"mcp"},
		LocalSocketPath: socketPath,
		Stdin:           strings.NewReader(request),
		Stdout:          &stdout,
	})
	if err != nil {
		t.Fatalf("postamat mcp returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"jsonrpc":"2.0"`) || !strings.Contains(stdout.String(), `\"direction\":\"send\"`) {
		t.Fatalf("MCP response did not contain created job JSON:\n%s", stdout.String())
	}
}

func TestPostamatMCPHandlesFramedInitializeAndNotifications(t *testing.T) {
	socketPath := startLocalAgentd(t)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	notification := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	stdin := framedRPC(initialize) + framedRPC(notification)

	var stdout bytes.Buffer
	err := app.Run(context.Background(), app.Options{Name: "cli", Args: []string{"mcp"}, LocalSocketPath: socketPath, Stdin: strings.NewReader(stdin), Stdout: &stdout})
	if err != nil {
		t.Fatalf("postamat mcp returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Content-Length:") || !strings.Contains(output, `"serverInfo"`) {
		t.Fatalf("framed initialize response missing MCP payload:\n%s", output)
	}
	if strings.Count(output, "Content-Length:") != 1 {
		t.Fatalf("notification should not produce a response:\n%s", output)
	}
}

func framedRPC(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func startLocalAgentd(t *testing.T) string {
	t.Helper()
	socketPath := privateSocketPathForApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := agentd.ListenUnix(ctx, socketPath, agentd.NewLocalRouter(agentd.NewJobManager(nil)))
	if err != nil {
		t.Fatalf("start local agentd: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	waitForSocket(t, socketPath)
	return socketPath
}

func createJobViaCLI(t *testing.T, socketPath string, filePath string) string {
	t.Helper()
	var stdout bytes.Buffer
	err := app.Run(context.Background(), app.Options{Name: "cli", Args: []string{"send", filePath, "--to-agent", "agent-b"}, LocalSocketPath: socketPath, Stdout: &stdout})
	if err != nil {
		t.Fatalf("create job via cli: %v", err)
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	if response.ID == "" {
		t.Fatalf("empty job id in response: %s", stdout.String())
	}
	return response.ID
}

func writeTempPayload(t *testing.T, name string, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return path
}

func privateSocketPathForApp(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create private dir: %v", err)
	}
	return filepath.Join(dir, "agentd.sock")
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("unix socket %s did not become ready", socketPath)
}

func TestPostamatCLIRejectsMissingBrowserLinkFlag(t *testing.T) {
	var stderr bytes.Buffer
	err := app.Run(context.Background(), app.Options{Name: "cli", Args: []string{"share", "file.txt"}, Stderr: &stderr})
	if err == nil || !strings.Contains(err.Error(), "--browser-link") {
		t.Fatalf("share without --browser-link error = %v, stderr = %s", err, stderr.String())
	}
}
