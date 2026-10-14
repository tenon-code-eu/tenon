package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
)

// version est surchargée à la compilation via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt)
	ctx, stop := interruptContext(context.Background(), sig, os.Exit)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tenon : dossier courant illisible : %v\n", err)
		os.Exit(1)
	}

	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, wd))
}

// interruptContext annule le contexte au premier signal reçu sur sig ; au
// second, le processus quitte aussitôt via exit(130).
func interruptContext(parent context.Context, sig <-chan os.Signal, exit func(int)) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			cancel()
		case <-done:
			return
		}
		select {
		case <-sig:
			exit(130)
		case <-done:
		}
	}()
	var once sync.Once
	return ctx, func() {
		cancel()
		once.Do(func() { close(done) })
	}
}
