package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"golang.org/x/term"
)

// version est surchargée à la compilation via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt)
	// Au second Ctrl+C, les serveurs MCP stdio sont tués avant de quitter :
	// os.Exit n'exécute pas les fonctions différées qui les ferment.
	ctx, stop := interruptContext(context.Background(), sig, func(code int) {
		killLiveMCP()
		os.Exit(code)
	})
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tenon : dossier courant illisible : %v\n", err)
		os.Exit(1)
	}

	isTerminal := func() bool {
		return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	}
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, wd, isTerminal))
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
