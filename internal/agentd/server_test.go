package agentd

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestListenUnixServesLocalRouterOverUnixSocketAndCleansUp(t *testing.T) {
	socketPath := privateSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	server, err := ListenUnix(ctx, socketPath, NewLocalRouter(NewJobManager(nil)))
	if err != nil {
		t.Fatalf("ListenUnix returned error: %v", err)
	}
	defer server.Close()

	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return net.Dial("unix", socketPath)
	}}}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://agentd.local/local/v1/transfers", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unix socket request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.Dial("unix", socketPath); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("unix socket still accepts connections after context cancellation")
}

func TestListenUnixRefusesToRemoveNonSocketPath(t *testing.T) {
	socketPath := privateSocketPath(t)
	if err := os.WriteFile(socketPath, []byte("do not delete"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	_, err := ListenUnix(context.Background(), socketPath, NewLocalRouter(NewJobManager(nil)))
	if err == nil {
		t.Fatal("expected ListenUnix to reject existing non-socket path")
	}
	data, readErr := os.ReadFile(socketPath)
	if readErr != nil {
		t.Fatalf("sentinel was removed: %v", readErr)
	}
	if string(data) != "do not delete" {
		t.Fatalf("sentinel content changed: %q", data)
	}
}

func TestListenUnixRestrictsSocketPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket permissions are not meaningful on Windows")
	}
	socketPath := privateSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := ListenUnix(ctx, socketPath, NewLocalRouter(NewJobManager(nil)))
	if err != nil {
		t.Fatalf("ListenUnix returned error: %v", err)
	}
	defer server.Close()
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o, want 0600", info.Mode().Perm())
	}
}

func privateSocketPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create private socket dir: %v", err)
	}
	return filepath.Join(dir, "agentd.sock")
}
