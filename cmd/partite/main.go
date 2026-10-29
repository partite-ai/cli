package main

import (
	"context"
	"errors"
	"os"
	"os/signal"

	"github.com/partite-ai/partite-cli/internal/cmd/root"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	app := cmdutil.NewApp()
	cmd := root.New(app, root.All...)
	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return
	}
	var cliErr *cmdutil.Error
	if ctx.Err() != nil && !errors.As(err, &cliErr) {
		err = context.Canceled
	}
	e := app.Classify(err)
	app.PrintError(app.Err, e)
	os.Exit(e.Code)
}
