package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/kirillkuzin/postamat/internal/api"
	"github.com/kirillkuzin/postamat/internal/sessions"
)

type Options struct {
	Name          string
	ListenAddress string
	TokenPepper   string
	Ready         chan<- string
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
	case "agentd", "cli":
		return nil
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
	server := &http.Server{
		Handler:           api.NewRouter(service),
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

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
