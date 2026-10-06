package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ramilS/faultline/internal/command"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	exitCode := command.Run(ctx, os.Args[1:], int(os.Stdin.Fd()), os.Stdout, os.Stderr)
	stop()
	os.Exit(exitCode)
}
