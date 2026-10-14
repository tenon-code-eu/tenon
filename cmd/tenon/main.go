package main

import (
	"context"
	"fmt"
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
		fmt.Fprintf(os.Stderr, "tenon : dossier courant illisible : %v\n", err)
		os.Exit(1)
	}

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv, wd))
}
