package app_test

import (
	"context"
	"testing"

	"github.com/kirillkuzin/postamat/internal/app"
)

func TestRunAcceptsSkeletonCommands(t *testing.T) {
	for _, name := range []string{"server", "agentd", "cli"} {
		t.Run(name, func(t *testing.T) {
			if err := app.Run(context.Background(), app.Options{Name: name}); err != nil {
				t.Fatalf("Run(%q) returned error: %v", name, err)
			}
		})
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	err := app.Run(context.Background(), app.Options{Name: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}
