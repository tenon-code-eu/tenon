package tools

import (
	"os"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/sandbox"
)

// TestMain : sous Linux, le binaire de test sert d'assistant `__sandbox`
// (os.Executable) pour les commandes isolées.
func TestMain(m *testing.M) {
	sandbox.MaybeMain()
	os.Exit(m.Run())
}
