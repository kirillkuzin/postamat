package app

import (
	"context"
	"errors"
	"fmt"
)

type Options struct {
	Name string
}

func Run(ctx context.Context, opts Options) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	switch opts.Name {
	case "server", "agentd", "cli":
		return nil
	case "":
		return errors.New("command name is required")
	default:
		return fmt.Errorf("unknown command %q", opts.Name)
	}
}
