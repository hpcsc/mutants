package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hpcsc/mutants/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cmd.Run(ctx))
}
