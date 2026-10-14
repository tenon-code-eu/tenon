package secrets

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestEnvWins(t *testing.T) {
	keyring.MockInit()
	if err := Set("mistral", "depuis-trousseau"); err != nil {
		t.Fatal(err)
	}
	v, src, err := Lookup("mistral", "MISTRAL_API_KEY", envOf(map[string]string{"MISTRAL_API_KEY": "depuis-env"}))
	if err != nil || v != "depuis-env" || src != "env" {
		t.Fatalf("got %q %q %v", v, src, err)
	}
}

func TestKeyringFallback(t *testing.T) {
	keyring.MockInit()
	if err := Set("mistral", "depuis-trousseau"); err != nil {
		t.Fatal(err)
	}
	v, src, err := Lookup("mistral", "MISTRAL_API_KEY", envOf(nil))
	if err != nil || v != "depuis-trousseau" || src != "keyring" {
		t.Fatalf("got %q %q %v", v, src, err)
	}
}

func TestAbsent(t *testing.T) {
	keyring.MockInit()
	v, src, err := Lookup("mistral", "MISTRAL_API_KEY", envOf(nil))
	if err != nil || v != "" || src != "" {
		t.Fatalf("got %q %q %v", v, src, err)
	}
}

func TestSetDelete(t *testing.T) {
	keyring.MockInit()
	if err := Set("mistral", "k1"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := Lookup("mistral", "", envOf(nil)); v != "k1" {
		t.Fatalf("got %q", v)
	}
	if err := Delete("mistral"); err != nil {
		t.Fatal(err)
	}
	v, src, err := Lookup("mistral", "", envOf(nil))
	if err != nil || v != "" || src != "" {
		t.Fatalf("got %q %q %v", v, src, err)
	}
}

func TestEmptyValueRefused(t *testing.T) {
	keyring.MockInit()
	if err := Set("mistral", ""); err == nil {
		t.Fatal("valeur vide acceptée")
	}
}

func TestEmptyProviderRefused(t *testing.T) {
	keyring.MockInit()
	if err := Set("", "x"); err == nil {
		t.Fatal("Set: fournisseur vide accepté")
	}
	if err := Delete(""); err == nil {
		t.Fatal("Delete: fournisseur vide accepté")
	}
	if _, _, err := Lookup("", "X", envOf(nil)); err == nil {
		t.Fatal("Lookup: fournisseur vide accepté")
	}
}

func TestKeyringErrorReturned(t *testing.T) {
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	_, _, err := Lookup("mistral", "X", envOf(nil))
	if err == nil {
		t.Fatal("erreur du trousseau avalée")
	}
	keyring.MockInit()
}

func TestDeleteAbsentIsNotError(t *testing.T) {
	keyring.MockInit()
	if err := Delete("mistral"); err != nil {
		t.Fatalf("got %v", err)
	}
}
