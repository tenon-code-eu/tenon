package sandbox

import (
	"fmt"
	"os"
)

// Main est l'assistant interne `tenon __sandbox` : `--probe` sonde le
// mécanisme, `-- <argv>` applique la Spec (variable SpecEnv) puis exécute la
// commande. Les échecs écrivent `sandbox: <raison>` sur stderr et rendent 126.
func Main(args []string) int {
	if len(args) > 0 && args[0] == "--probe" {
		return helperProbe()
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sandbox: missing command")
		return 126
	}
	return helperMain(args)
}

// MaybeMain quitte avec le code de Main quand le processus est lancé en
// assistant (`__sandbox` en premier argument) ; sinon elle ne fait rien.
func MaybeMain() {
	if len(os.Args) > 1 && os.Args[1] == "__sandbox" {
		os.Exit(Main(os.Args[2:]))
	}
}
