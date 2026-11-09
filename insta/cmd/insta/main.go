package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/instaclustr/labs/insta/internal/cli"
	"github.com/instaclustr/labs/insta/internal/runner"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__serve" {
		if len(os.Args) != 8 {
			os.Exit(2)
		}
		if e := runner.Serve(context.Background(), os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6], os.Args[7]); e != nil {
			os.Exit(1)
		}
		return
	}
	app := cli.New(os.Stdin, os.Stdout, os.Stderr)
	command, _, _ := app.Command().Find(os.Args[1:])
	// The exec handler selects signal behavior after parsing its mode. The
	// native Codex terminal needs to receive Ctrl-C directly.
	if command != nil && command.Name() == "exec" {
		os.Exit(app.Execute(context.Background(), os.Args[1:]))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	code := app.Execute(ctx, os.Args[1:])
	if ctx.Err() != nil && code == 0 {
		fmt.Fprintln(os.Stderr, "Operation interrupted")
		code = 130
	}
	os.Exit(code)
}
