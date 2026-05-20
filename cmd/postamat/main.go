package main

import (
	"context"
	"log"

	"github.com/kirillkuzin/postamat/internal/app"
)

func main() {
	if err := app.Run(context.Background(), app.Options{Name: "cli"}); err != nil {
		log.Fatal(err)
	}
}
