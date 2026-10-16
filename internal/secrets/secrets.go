// Package secrets recherche les clés API des fournisseurs : d'abord dans
// l'environnement, puis dans le trousseau système (Keychain sur macOS,
// Secret Service sur Linux). Le service du trousseau est « tenon » et le
// compte est l'identifiant du fournisseur.
package secrets

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Service est le nom de service utilisé dans le trousseau système.
const Service = "tenon"

var errFournisseurVide = errors.New("empty provider id")

// Lookup renvoie la clé du fournisseur et sa provenance : "env" (variable
// d'environnement, prioritaire), "keyring" (trousseau) ou "" si la clé est
// absente. Une erreur du trousseau autre que « introuvable » est renvoyée
// telle quelle ; l'appelant la traite comme une clé absente.
func Lookup(providerID, envName string, getenv func(string) string) (value, source string, err error) {
	if providerID == "" {
		return "", "", errFournisseurVide
	}
	if envName != "" && getenv != nil {
		if v := getenv(envName); v != "" {
			return v, "env", nil
		}
	}
	v, err := keyring.Get(Service, providerID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("reading keychain for %q: %w", providerID, err)
	}
	if v == "" {
		return "", "", nil
	}
	return v, "keyring", nil
}

// Set enregistre la clé du fournisseur dans le trousseau.
func Set(providerID, value string) error {
	if providerID == "" {
		return errFournisseurVide
	}
	if value == "" {
		return errors.New("empty key value rejected")
	}
	if err := keyring.Set(Service, providerID, value); err != nil {
		return fmt.Errorf("writing keychain for %q: %w", providerID, err)
	}
	return nil
}

// Delete supprime la clé du fournisseur du trousseau ; une clé déjà absente
// n'est pas une erreur.
func Delete(providerID string) error {
	if providerID == "" {
		return errFournisseurVide
	}
	err := keyring.Delete(Service, providerID)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("deleting from keychain for %q: %w", providerID, err)
	}
	return nil
}
