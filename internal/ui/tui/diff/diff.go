// Package diff calcule l'aperçu des modifications affiché par la TUI avant
// qu'une écriture (write_file / edit_file) soit approuvée. Il ne fait que lire.
package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Kind qualifie une ligne du diff.
type Kind int

const (
	Same Kind = iota
	Add
	Del
)

// Line est une ligne du diff.
type Line struct {
	Kind Kind
	Text string
}

const (
	// MaxLines est le nombre maximal de lignes d'aperçu renvoyées.
	MaxLines = 400
	// maxLCS est la taille au-delà de laquelle on évite le LCS quadratique.
	maxLCS = 2000
	// maxRead plafonne la lecture du fichier courant.
	maxRead = 1 << 20
	// binProbe est la fenêtre d'analyse de la détection binaire.
	binProbe = 8000

	binaryText = "(fichier binaire)"
	ellipsis   = "…"
)

// BinaryText est le texte de l'unique ligne renvoyée par Preview quand le
// fichier courant ou le nouveau contenu est binaire.
const BinaryText = binaryText

// splitLines découpe en lignes ; un saut de ligne final ne crée pas de ligne vide.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func isBinary(s string) bool {
	if len(s) > binProbe {
		s = s[:binProbe]
	}
	return strings.IndexByte(s, 0) >= 0
}

// Lines calcule un diff ligne à ligne (LCS), sans limitation de contexte.
func Lines(before, after string) []Line {
	return diffLines(splitLines(before), splitLines(after), false)
}

// diffLines : force impose le repli « tout supprimer puis tout ajouter ».
func diffLines(a, b []string, force bool) []Line {
	// Préfixe et suffixe communs : peu coûteux et réduit le LCS.
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	out := make([]Line, 0, len(a)+len(b))
	for _, t := range a[:p] {
		out = append(out, Line{Same, t})
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	out = append(out, middle(ma, mb, force)...)
	for _, t := range a[len(a)-s:] {
		out = append(out, Line{Same, t})
	}
	return out
}

func middle(a, b []string, force bool) []Line {
	out := make([]Line, 0, len(a)+len(b))
	if force || len(a) > maxLCS || len(b) > maxLCS {
		for _, t := range a {
			out = append(out, Line{Del, t})
		}
		for _, t := range b {
			out = append(out, Line{Add, t})
		}
		return out
	}
	// dp[i][j] = longueur du LCS de a[i:] et b[j:].
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, Line{Same, a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, Line{Del, a[i]})
			i++
		default:
			out = append(out, Line{Add, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, Line{Del, a[i]})
	}
	for ; j < m; j++ {
		out = append(out, Line{Add, b[j]})
	}
	return out
}

// Unified garde `context` lignes autour des changements et insère une ligne
// « … » aux endroits où du texte inchangé est omis.
func Unified(before, after string, context int) []Line {
	return unified(diffLines(splitLines(before), splitLines(after), false), context)
}

func unified(all []Line, context int) []Line {
	if context < 0 {
		context = 0
	}
	keep := make([]bool, len(all))
	changed := false
	for i, l := range all {
		if l.Kind == Same {
			continue
		}
		changed = true
		for k := max(0, i-context); k <= min(len(all)-1, i+context); k++ {
			keep[k] = true
		}
	}
	if !changed {
		return nil
	}
	var out []Line
	for i, l := range all {
		if keep[i] {
			out = append(out, l)
		} else if i == 0 || keep[i-1] {
			out = append(out, Line{Same, ellipsis})
		}
	}
	return out
}

// truncate limite à MaxLines et renvoie le nombre de lignes omises.
func truncate(l []Line) ([]Line, int) {
	if len(l) <= MaxLines {
		return l, 0
	}
	return l[:MaxLines], len(l) - MaxLines
}

// readCurrent lit le contenu actuel d'un fichier sans jamais écrire. Les liens
// symboliques sont suivis (l'écriture atterrit sur la cible). Un fichier absent,
// non régulier (FIFO, périphérique, répertoire, y compris via un lien) ou
// illisible vaut « vide ». huge est vrai si le fichier dépasse maxRead octets.
func readCurrent(path string) (content string, huge bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	// Revérifie sur le descripteur ouvert (course entre Lstat et Open).
	if fi2, err := f.Stat(); err != nil || !fi2.Mode().IsRegular() {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRead+1))
	if err != nil {
		return "", false
	}
	if len(data) > maxRead {
		return string(bytes.Clone(data[:maxRead])), true
	}
	return string(data), false
}

// Preview calcule l'aperçu du diff pour un appel d'outil d'écriture. Les autres
// outils renvoient nil, 0, nil.
func Preview(tool string, args json.RawMessage, root string) (lines []Line, more int, err error) {
	switch tool {
	case "write_file":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, 0, fmt.Errorf("arguments invalides : %w", err)
		}
		p := a.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		cur, huge := readCurrent(filepath.Clean(p))
		if isBinary(cur) || isBinary(a.Content) {
			return []Line{{Same, binaryText}}, 0, nil
		}
		var all []Line
		if huge {
			all = diffLines(splitLines(cur), splitLines(a.Content), true)
			l, m := truncate(all)
			return l, m, nil
		}
		l, m := truncate(Unified(cur, a.Content, 3))
		return l, m, nil
	case "edit_file":
		var a struct {
			Path       string `json:"path"`
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, 0, fmt.Errorf("arguments invalides : %w", err)
		}
		if isBinary(a.OldString) || isBinary(a.NewString) {
			return []Line{{Same, binaryText}}, 0, nil
		}
		// replace_all : une seule occurrence suffit à l'aperçu.
		l, m := truncate(Unified(a.OldString, a.NewString, 3))
		return l, m, nil
	}
	return nil, 0, nil
}
