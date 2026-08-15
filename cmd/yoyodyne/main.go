package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"yoyodyne/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.RunContext(ctx, os.Args[1:], os.Stdout, os.Stderr, version))
}
