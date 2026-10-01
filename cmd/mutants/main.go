package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hpcsc/mutants/internal/cmd"
)

func main() {
	// the test binaries run in their own process groups, so mutants must stop them before it exits
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cmd.Run(ctx))
}
