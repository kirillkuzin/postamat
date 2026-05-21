package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/kirillkuzin/postamat/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, app.Options{Name: "agentd"}); err != nil {
		log.Fatal(err)
	}
}
