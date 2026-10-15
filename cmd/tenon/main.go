package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// version est surchargée à la compilation via -ldflags "-X main.version=...".
var version = "dev"

// handledSignals sont les signaux interceptés : le premier annule le contexte.
var handledSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

func main() {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, handledSignals...)
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

// interruptContext annule le contexte au premier signal reçu sur sig ; à un
// Ctrl+C ultérieur, le processus quitte aussitôt via exit(130). SIGTERM et
// SIGHUP ne forcent jamais la sortie : l'arrêt normal ferme les serveurs MCP.
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
		for {
			select {
			case s := <-sig:
				if s == os.Interrupt {
					exit(130)
					return
				}
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return ctx, func() {
		cancel()
		once.Do(func() { close(done) })
	}
}
