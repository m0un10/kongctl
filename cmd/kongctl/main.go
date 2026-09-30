// Command kongctl composes, checks and delivers Kong declarative config.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/m0un10/kongctl/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
