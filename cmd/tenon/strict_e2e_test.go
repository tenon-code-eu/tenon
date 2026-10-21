package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictRequiresP(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, _, errb := e.run("--strict")
	if code != 1 || !strings.Contains(errb, "--strict ne s'utilise qu'avec -p") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestStrictE2E(t *testing.T) {
	// La limite d'itérations est atteinte : le modèle appelle un outil à chaque tour.
	f := newFakeLLM(t, sseCall("glob", `{"pattern":"*"}`))
	e := newEnv(t, f, "eu")
	cfgPath := filepath.Join(e.vars["XDG_CONFIG_HOME"], "tenon", "config.toml")
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	e.writeFile(cfgPath, "max_iterations = 2\n"+string(b))

	code, _, errb := e.run("-p", "x", "--strict")
	if code != 4 || !strings.Contains(errb, "strict : tour terminé avec max_iterations") {
		t.Fatalf("strict : code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("-p", "x")
	if code != 0 || strings.Contains(errb, "strict") {
		t.Fatalf("sans strict : code=%d stderr=%q", code, errb)
	}
}
