package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

const (
	// maskStart : le masquage commence quand la vue dépasse cette part de la
	// fenêtre (en dixièmes) ; maskStop : il s'arrête sous cette part.
	maskStart = 5
	maskStop  = 4
	// protectedIters : nombre de dernières itérations (messages assistant
	// portant des appels d'outils) dont les résultats ne sont jamais masqués.
	protectedIters = 3
	// maskArgsRunes : longueur maximale des arguments rappelés dans le talon.
	maskArgsRunes = 60
)

// Shaped décrit ce que la vue a changé : call_id des résultats masqués et
// dédupliqués (ordre de l'historique) et jetons estimés économisés.
type Shaped struct {
	Masked, Deduplicated []string
	SavedTokens          int
}

// sameSets indique si a et b masquent et dédupliquent les mêmes appels.
func (a Shaped) sameSets(b Shaped) bool {
	return slices.Equal(a.Masked, b.Masked) && slices.Equal(a.Deduplicated, b.Deduplicated)
}

// view projette history en messages à envoyer, sans jamais modifier history
// ni ses messages : (1) déduplication des résultats d'outils identiques (même
// outil, mêmes arguments, même SHA-256 du contenu), seul le plus récent reste ;
// (2) masquage collant (ruling R12) : les résultats de prev (call_id masqués
// par la vue précédente) restent masqués, puis, si l'estimation dépasse
// encore 50 % de window, les plus anciens résultats d'outils sont masqués un
// par un jusqu'à repasser sous 40 %. Entre 40 et 50 %, rien de nouveau n'est
// masqué : le préfixe envoyé reste stable (cache du fournisseur). Seul le
// contenu des messages tool change ; les résultats des 3 dernières
// itérations (messages assistant à appels d'outils) et ceux de l'outil agent
// (ruling R13) ne sont jamais masqués. Un talon qui ne raccourcit pas le
// résultat n'est pas appliqué. Déterministe pour history et prev donnés.
func view(history []llm.Message, defs []llm.ToolDef, window int, prev []string) ([]llm.Message, Shaped) {
	out := slices.Clone(history)
	var sh Shaped

	// Appels d'outils par call_id, et numéro de tour utilisateur (1, 2, …)
	// de chaque message : nombre de messages user jusqu'à lui inclus.
	calls := map[string]llm.ToolCall{}
	turnOf := make([]int, len(out))
	iters := []int{}
	n := 0
	for i, m := range out {
		if m.Role == llm.User {
			n++
		}
		if m.Role == llm.Assistant && len(m.ToolCalls) > 0 {
			iters = append(iters, i)
		}
		turnOf[i] = n
		for _, c := range m.ToolCalls {
			calls[c.ID] = c
		}
	}
	// Début de la zone protégée : le plus ancien des 3 derniers messages
	// assistant à appels d'outils ; rien n'est masqué à partir de lui (un
	// long tour agentique ou un sous-agent n'a qu'un message user).
	protect := len(out)
	if len(iters) > 0 {
		protect = iters[max(0, len(iters)-protectedIters)]
	}

	deduped := make([]bool, len(out))
	before := estimate(out, defs)

	// 1. Déduplication, du plus récent au plus ancien.
	latest := map[string]int{} // clé → indice du résultat conservé
	for i := len(out) - 1; i >= 0; i-- {
		m := out[i]
		c, ok := calls[m.ToolCallID]
		if m.Role != llm.Tool || !ok {
			continue
		}
		sum := sha256.Sum256([]byte(m.Content))
		key := c.Name + "\x00" + string(c.Args) + "\x00" + string(sum[:])
		j, seen := latest[key]
		if !seen {
			latest[key] = i
			continue
		}
		stub := fmt.Sprintf("[same content as the read at turn %d]", turnOf[j])
		if utf8.RuneCountInString(stub) >= utf8.RuneCountInString(m.Content) {
			continue
		}
		out[i].Content = stub
		deduped[i] = true
	}
	for i, d := range deduped {
		if d {
			sh.Deduplicated = append(sh.Deduplicated, out[i].ToolCallID)
		}
	}

	// 2. Masquage, hors zone protégée, des résultats d'outils autres que
	// agent. L'estimation est tenue en runes (estimate = runes / 4).
	r := runeCount(out, defs)
	masked := make([]bool, len(out))
	mask := func(i int) {
		m := out[i]
		c, ok := calls[m.ToolCallID]
		if m.Role != llm.Tool || !ok || deduped[i] || masked[i] || c.Name == agentToolName {
			return
		}
		stub := maskStub(c, m.Content)
		old, nw := utf8.RuneCountInString(m.Content), utf8.RuneCountInString(stub)
		if nw >= old {
			return
		}
		out[i].Content = stub
		r -= old - nw
		masked[i] = true
	}
	// 2a. Ensemble précédent réappliqué tel quel.
	if len(prev) > 0 {
		for i := 0; i < protect; i++ {
			if out[i].Role == llm.Tool && slices.Contains(prev, out[i].ToolCallID) {
				mask(i)
			}
		}
	}
	// 2b. Extension sous pression, du plus ancien au plus récent.
	if window > 0 && 10*(r/4) > maskStart*window {
		for i := 0; i < protect && 10*(r/4) >= maskStop*window; i++ {
			mask(i)
		}
	}
	for i, m := range masked {
		if m {
			sh.Masked = append(sh.Masked, out[i].ToolCallID)
		}
	}
	sh.SavedTokens = before - estimate(out, defs)
	return out, sh
}

// maskStub renvoie le talon d'un résultat masqué de l'appel c.
func maskStub(c llm.ToolCall, content string) string {
	args := string(c.Args)
	if utf8.RuneCountInString(args) > maskArgsRunes {
		args = string([]rune(args)[:maskArgsRunes]) + "…"
	}
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("[output hidden to save context: %s %s, %d lines, sha256 %s; run the tool again if you need it]",
		c.Name, args, lineCount(content), hex.EncodeToString(sum[:])[:12])
}

// lineCount renvoie le nombre de lignes de s (un saut de ligne final ne
// compte pas une ligne vide de plus).
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// shapeRequest applique la vue aux messages msgs (déjà passés par le
// contrôle sortant) et émet ContextShaped si les ensembles masqués ou
// dédupliqués diffèrent des derniers émis par ce tour (la session pour le
// tour principal). Une requête hors conversation (noShape) n'émet rien et
// laisse cet état intact.
func (t *turn) shapeRequest(msgs []llm.Message, defs []llm.ToolDef) ([]llm.Message, error) {
	last := t.lastShaped()
	out, sh := view(msgs, defs, t.window(), last.Masked)
	if t.noShape {
		return out, nil
	}
	if sh.sameSets(*last) {
		return out, nil
	}
	*last = sh
	// Listes vides plutôt que null dans le journal.
	p := event.ContextShapedPayload{Masked: []string{}, Deduplicated: []string{}, SavedTokens: sh.SavedTokens}
	p.Masked = append(p.Masked, sh.Masked...)
	p.Deduplicated = append(p.Deduplicated, sh.Deduplicated...)
	if err := t.emit(event.ContextShaped, p); err != nil {
		return nil, err
	}
	return out, nil
}

// lastShaped renvoie les derniers ensembles de la vue de ce tour (la session
// pour le tour principal) : leur ensemble masqué est réappliqué par la vue
// suivante (ruling R12).
func (t *turn) lastShaped() *Shaped {
	if t.parent == "" {
		return &t.s.shaped
	}
	return &t.shaped
}

// viewSize renvoie l'estimation de la vue de msgs (ensemble masqué collant
// du tour, sans le modifier ni émettre d'événement) : base des décisions de
// compaction (ruling R11).
func (t *turn) viewSize(msgs []llm.Message, defs []llm.ToolDef) int {
	v, _ := view(msgs, defs, t.window(), t.lastShaped().Masked)
	return estimate(v, defs)
}
