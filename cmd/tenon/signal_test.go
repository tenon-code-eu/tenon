package main

import (
	"context"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestSecondInterruptExits130(t *testing.T) {
	sig := make(chan os.Signal, 2)
	exited := make(chan int, 1)
	ctx, stop := interruptContext(context.Background(), sig, func(code int) { exited <- code })
	defer stop()

	sig <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("premier Ctrl+C : contexte non annulé")
	}
	select {
	case code := <-exited:
		t.Fatalf("sortie prématurée : %d", code)
	case <-time.After(50 * time.Millisecond):
	}

	sig <- os.Interrupt
	select {
	case code := <-exited:
		if code != 130 {
			t.Fatalf("code=%d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Ctrl+C : pas de sortie")
	}
}

// SIGTERM et SIGHUP annulent le contexte comme Ctrl+C, mais ne forcent
// jamais la sortie : l'arrêt normal ferme les serveurs MCP. Un Ctrl+C
// ultérieur quitte encore aussitôt (130).
func TestTermAndHupCancelWithoutExit(t *testing.T) {
	for _, s := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		if !slices.Contains(handledSignals, s) {
			t.Fatalf("%v non intercepté : %v", s, handledSignals)
		}
		sig := make(chan os.Signal, 2)
		exited := make(chan int, 1)
		ctx, stop := interruptContext(context.Background(), sig, func(code int) { exited <- code })
		sig <- s
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("%v : contexte non annulé", s)
		}
		sig <- s
		select {
		case code := <-exited:
			t.Fatalf("%v répété : sortie forcée %d", s, code)
		case <-time.After(100 * time.Millisecond):
		}
		sig <- os.Interrupt
		select {
		case code := <-exited:
			if code != 130 {
				t.Fatalf("code=%d", code)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%v puis Ctrl+C : pas de sortie", s)
		}
		stop()
	}
}
