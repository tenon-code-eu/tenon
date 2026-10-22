package sandbox

import (
	"os"
	"testing"
)

func TestMaybeMainIgnoresOtherArgs(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"x", "y"}
	MaybeMain() // ne doit pas quitter
}

func TestMainMissingCommand(t *testing.T) {
	if got := Main([]string{}); got != 126 {
		t.Fatalf("Main sans commande = %d, attendu 126", got)
	}
}
