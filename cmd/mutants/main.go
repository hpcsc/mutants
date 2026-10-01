package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/hpcsc/mutants/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(cmd.Run(ctx))
}
