package main

import (
	"context"
	"os"
	"os/signal"
)

// version est surchargée à la compilation via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv, wd))
}
