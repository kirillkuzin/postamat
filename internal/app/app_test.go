package app_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/app"
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
