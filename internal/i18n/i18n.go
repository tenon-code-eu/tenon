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

// Parse renvoie EN pour toute valeur commençant par "en" (ex. "en_US.UTF-8"), sinon FR.
func Parse(s string) Lang {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "en") {
		return EN
	}
	return FR
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
