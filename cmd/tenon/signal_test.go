package main

import (
	"context"
	"os"
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
