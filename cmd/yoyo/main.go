package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/cli"
)

// version is what the linker stamps into a release build and what "make build"
// fills with a git description. It stays a plain constant initializer because
// that is what "go build -ldflags -X" can overwrite. A binary from
// "go install" has no stamp, so buildinfo resolves the rest.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.RunContext(ctx, os.Args[1:], os.Stdout, os.Stderr, buildinfo.Resolve(version)))
}
