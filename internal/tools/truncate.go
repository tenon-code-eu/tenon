package tools

import "fmt"

// Truncate réduit s à limit caractères (runes) en gardant le début (70 %)
// et la fin (30 %), avec un marqueur au milieu. Le marqueur s'ajoute à la
// limite, il n'est pas compté. limit <= 0 ou s assez court : s inchangé.
func Truncate(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	n := len(r)
	if n <= limit {
		return s
	}
	head := limit * 7 / 10
	tail := limit - head

	// Forme multi-lignes : la tête finit en fin de ligne, la queue commence
	// en début de ligne, pour que le nombre de lignes omises soit exact.
	p := head // la tête est r[:p], r[p] est un saut de ligne
	for p >= 0 && (p >= n || r[p] != '\n') {
		p--
	}
	q := n - tail // la queue est r[q:], r[q-1] est un saut de ligne
	for q <= n && (q == 0 || r[q-1] != '\n') {
		q++
	}
	if p >= 0 && q < n && q-1 > p {
		omitted := 1
		for _, c := range r[p+1 : q-1] {
			if c == '\n' {
				omitted++
			}
		}
		return string(r[:p]) + fmt.Sprintf("\n[… %d lines omitted …]\n", omitted) + string(r[q:])
	}

	// Une seule ligne (ou aucune coupe propre) : forme en caractères.
	return string(r[:head]) + fmt.Sprintf("\n[… %d characters omitted …]\n", n-limit) + string(r[n-tail:])
}
