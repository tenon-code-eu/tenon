// Package i18n fournit les chaînes FR/EN de la CLI, du mode headless et du prompt système.
package i18n

import (
	"fmt"
	"strings"
)

// Lang est une langue d'interface.
type Lang string

const (
	FR Lang = "fr"
	EN Lang = "en"
)

// Parse renvoie FR pour toute valeur commençant par "fr" (ex. "fr_FR.UTF-8"),
// sinon EN, la langue par défaut.
func Parse(s string) Lang {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "fr") {
		return FR
	}
	return EN
}

// T applique fmt.Sprintf au gabarit de la clé ; clé inconnue : "!" + key.
func T(l Lang, key string, args ...any) string {
	tpl, ok := catalog[l][key]
	if !ok {
		return "!" + key
	}
	if len(args) == 0 {
		return tpl
	}
	return fmt.Sprintf(tpl, args...)
}
