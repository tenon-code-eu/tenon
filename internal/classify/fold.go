package classify

import (
	"strings"
	"unicode"
)

// foldTable associe une lettre accentuée (Latin-1 Supplement et Latin
// Extended-A) à son équivalent ASCII minuscule. Table fixe : golang.org/x/text
// n'est pas vendu.
var foldTable = buildFoldTable()

func buildFoldTable() map[rune]string {
	groups := map[string]string{
		"a":  "ÀÁÂÃÄÅàáâãäåĀāĂăĄą",
		"c":  "ÇçĆćĈĉĊċČč",
		"d":  "ÐðĎďĐđ",
		"e":  "ÈÉÊËèéêëĒēĔĕĖėĘęĚě",
		"g":  "ĜĝĞğĠġĢģ",
		"h":  "ĤĥĦħ",
		"i":  "ÌÍÎÏìíîïĨĩĪīĬĭĮįİı",
		"j":  "Ĵĵ",
		"k":  "Ķķĸ",
		"l":  "ĹĺĻļĽľĿŀŁł",
		"n":  "ÑñŃńŅņŇňŉ",
		"o":  "ÒÓÔÕÖØòóôõöøŌōŎŏŐő",
		"r":  "ŔŕŖŗŘř",
		"s":  "ŚśŜŝŞşŠšſ",
		"t":  "ŢţŤťŦŧ",
		"u":  "ÙÚÛÜùúûüŨũŪūŬŭŮůŰűŲų",
		"w":  "Ŵŵ",
		"y":  "ÝýÿŶŷŸ",
		"z":  "ŹźŻżŽž",
		"ae": "Ææ",
		"oe": "Œœ",
		"ss": "ß",
		"ij": "Ĳĳ",
	}
	t := make(map[rune]string)
	for base, letters := range groups {
		for _, r := range letters {
			t[r] = base
		}
	}
	return t
}

// fold met s en minuscules sans accents, supprime les caractères de format
// invisibles (unicode.Cf : espace sans chasse, trait d'union conditionnel,
// BOM...) et réduit toute suite d'espaces (retours à la ligne compris) à une
// seule espace.
func fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue // marque combinante (entrée NFD)
		}
		if unicode.Is(unicode.Cf, r) {
			continue // caractère de format invisible
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		if f, ok := foldTable[r]; ok {
			b.WriteString(f)
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
