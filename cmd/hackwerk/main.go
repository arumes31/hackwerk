// Command hackwerk runs the web, worker, migration, and administration modes.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"example.invalid/hackplan/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:], cli.IO{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr}))
}
