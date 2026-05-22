package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kirillkuzin/postamat/internal/agentd"
	"github.com/kirillkuzin/postamat/internal/api"
	"github.com/kirillkuzin/postamat/internal/sessions"
)

type Options struct {
	Name            string
	Args            []string
	ListenAddress   string
	LocalSocketPath string
	BackendURL      string
	AgentID         string
	DeviceID        string
	TokenPepper     string
	AgentAuthToken  string
	AgentTokens     map[string]string
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	Ready           chan<- string
}

func Run(ctx context.Context, opts Options) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	switch opts.Name {
	case "server":
		return runServer(ctx, opts)
	case "agentd":
		return runAgentd(ctx, opts)
	case "cli":
		return runCLI(ctx, opts)
	case "":
		return errors.New("command name is required")
	default:
		return fmt.Errorf("unknown command %q", opts.Name)
	}
}

func runServer(ctx context.Context, opts Options) error {
	pepper := opts.TokenPepper
	if pepper == "" {
		pepper = os.Getenv("POSTAMAT_TOKEN_PEPPER")
	}
	if pepper == "" {
		return errors.New("POSTAMAT_TOKEN_PEPPER is required for server mode")
	}

	address := opts.ListenAddress
	if address == "" {
		address = envOrDefault("POSTAMAT_HTTP_ADDR", ":8080")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", address, err)
	}

	service := sessions.NewService(sessions.NewMemoryRepository(), sessions.RandomTokenIssuer{Pepper: pepper}, nil)
	agentAuth, err := agentAuthenticatorFromOptions(opts, pepper)
	if err != nil {
		_ = listener.Close()
		return err
	}
	server := &http.Server{
		Handler:           api.NewRouterWithSignalingAndAgentAuth(service, nil, nil, agentAuth),
		ReadHeaderTimeout: 5 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if opts.Ready != nil {
		select {
		case opts.Ready <- listener.Addr().String():
		case <-ctx.Done():
			_ = listener.Close()
			<-done
			return nil
		}
	}

	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}

func runAgentd(ctx context.Context, opts Options) error {
	socketPath := localSocketPath(opts)
	jobs := agentd.NewJobManager(nil)
	handler, err := agentdHandler(ctx, opts, jobs)
	if err != nil {
		return err
	}
	server, err := agentd.ListenUnix(ctx, socketPath, handler)
	if err != nil {
		return err
	}
	defer server.Close()
	if opts.Ready != nil {
		select {
		case opts.Ready <- socketPath:
		case <-ctx.Done():
			return nil
		}
	}
	<-ctx.Done()
	return nil
}

func agentdHandler(ctx context.Context, opts Options, jobs *agentd.JobManager) (http.Handler, error) {
	backendURL := opts.BackendURL
	if backendURL == "" {
		backendURL = os.Getenv("POSTAMAT_BACKEND_URL")
	}
	if backendURL == "" {
		return agentd.NewLocalRouter(jobs), nil
	}
	agentID := opts.AgentID
	if agentID == "" {
		agentID = os.Getenv("POSTAMAT_AGENT_ID")
	}
	if agentID == "" {
		return nil, errors.New("POSTAMAT_AGENT_ID is required when POSTAMAT_BACKEND_URL is configured")
	}
	deviceID := opts.DeviceID
	if deviceID == "" {
		deviceID = envOrDefault("POSTAMAT_DEVICE_ID", "agentd-local")
	}
	agentToken := agentAuthTokenFromOptions(opts)
	loop := agentd.NewBackendLoop(agentd.BackendLoopOptions{AgentID: agentID, DeviceID: deviceID, Jobs: jobs, Client: agentd.NewBackendClient(backendURL, nil), AgentAuthToken: agentToken})
	if agentToken != "" {
		go func() { _ = loop.RunReceiver(ctx) }()
	}
	return agentd.NewLocalRouterWithBackendContext(ctx, jobs, loop), nil
}

func localSocketPath(opts Options) string {
	if opts.LocalSocketPath != "" {
		return opts.LocalSocketPath
	}
	if value := os.Getenv("POSTAMAT_AGENTD_SOCKET"); value != "" {
		return value
	}
	return "/tmp/postamat/agentd.sock"
}

func agentAuthenticatorFromOptions(opts Options, pepper string) (api.AgentAuthenticator, error) {
	tokens := opts.AgentTokens
	if len(tokens) == 0 {
		parsed, err := parseAgentTokens(os.Getenv("POSTAMAT_AGENT_TOKENS"))
		if err != nil {
			return nil, err
		}
		tokens = parsed
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	return api.NewStaticAgentTokenAuthenticator(tokens, pepper)
}

func parseAgentTokens(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	tokens := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || parts[1] == "" {
			return nil, errors.New("POSTAMAT_AGENT_TOKENS must be comma-separated agent_id:token pairs")
		}
		tokens[strings.TrimSpace(parts[0])] = parts[1]
	}
	return tokens, nil
}

func agentAuthTokenFromOptions(opts Options) string {
	if opts.AgentAuthToken != "" {
		return opts.AgentAuthToken
	}
	return os.Getenv("POSTAMAT_AGENT_TOKEN")
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
