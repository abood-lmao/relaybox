// Command relaybox captures, inspects, and replays webhooks.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/abood-lmao/relaybox/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // a second Ctrl+C stops the process without waiting
	}()
	code := cli.Main(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
