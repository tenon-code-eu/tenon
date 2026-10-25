package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/classify"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// Classification des sessions : un contenu classé (chemin ou marquage) rend la
// session sensible quand un fournisseur local existe ; sinon l'appel est
// refusé avant exécution (chemin), son résultat est retenu avant d'entrer dans
// l'historique (marquage), ou le tour est bloqué (message de l'utilisateur,
// session déjà sensible).
const (
	// deniedClassification : valeur de ToolResultPayload.Denied d'un refus.
	deniedClassification = "classification"
	// classifiedRefused : contenu du ToolResult d'un appel refusé.
	classifiedRefused = "classified content requires a local provider (routing.local)"
	// classifiedWithheld remplace un résultat d'outil marqué.
	classifiedWithheld = "[classified content withheld: no local provider]"
)

// Sources de SessionClassified.
const (
	sourceTool  = "tool"
	sourceShell = "shell"
	sourceMCP   = "mcp"
	sourceUser  = "user"
	// sourceOutbound : marquage trouvé par le contrôle sortant d'une requête.
	sourceOutbound = "outbound"
	// sourceMemory : lecture d'une entrée de mémoire sensible.
	sourceMemory = "memory"
)

// classify rend la session sensible et émet SessionClassified, une seule fois
// par session (sous-agents et tours de hooks compris : ils partagent t.s).
// Fermeture par défaut : la session reste sensible même si l'émission échoue.
func (t *turn) classify(reason, source, callID, path, marker string) error {
	s := t.s
	s.classMu.Lock()
	defer s.classMu.Unlock()
	if s.sensitive {
		return nil
	}
	s.sensitive = true
	s.sensitiveReason = reason
	return t.e.emit(s, event.SessionClassified, event.SessionClassifiedPayload{
		Reason: reason, Source: source, CallID: callID, Path: path, Marker: marker,
	})
}

// sensitiveState renvoie l'état sensible de la session et sa raison.
func (s *session) sensitiveState() (bool, string) {
	s.classMu.Lock()
	defer s.classMu.Unlock()
	return s.sensitive, s.sensitiveReason
}

// toolSource renvoie la source SessionClassified d'un outil.
func toolSource(name string) string {
	switch {
	case name == "shell":
		return sourceShell
	case strings.HasPrefix(name, "mcp__"):
		return sourceMCP
	}
	return sourceTool
}

// gateClassified contrôle un appel juste avant son exécution, sur ses
// arguments définitifs (après les hooks et la décision de permission) :
// chemin classé d'un outil de fichiers, chemin classé dans les mots d'une
// commande shell, marquage dans le contenu écrit. Avec un fournisseur local,
// la session devient sensible et l'appel s'exécute ; sinon refused vaut true
// et l'outil ne doit pas être exécuté. grep et glob n'ont pas de chemin
// propre (racine du projet) : grep est contrôlé sur son résultat.
func (t *turn) gateClassified(c llm.ToolCall, target tools.Target, args json.RawMessage) (refused bool, err error) {
	d := t.e.d
	rules := d.Classify
	var reason, path, marker string
	switch c.Name {
	case "read_file", "write_file", "edit_file":
		for _, p := range target.Paths {
			if _, ok := rules.MatchPath(d.ProjectRoot, p); ok {
				reason, path = "path", t.displayPath(p)
				break
			}
		}
		// Le contenu écrit (write_file, edit_file) peut lui-même être marqué.
		if reason == "" && c.Name != "read_file" {
			if m, ok := argsMarker(rules, args); ok {
				reason, marker = "marker", m
			}
		}
	case "shell":
		if w, ok := rules.CommandPath(d.ProjectRoot, target.Command); ok {
			reason, path = "path", w
		}
	}
	if reason == "" {
		return false, nil
	}
	if !t.e.hasLocal() {
		return true, nil
	}
	return false, t.classify(reason, toolSource(c.Name), c.ID, path, marker)
}

// scanResult contrôle le texte complet d'un résultat d'outil avant son
// enregistrement (ToolResult) et son entrée dans l'historique : chemin classé
// dans les lignes de grep, marquage pour tout outil sauf glob (noms seuls).
// Les préfixes de ligne de read_file et grep sont aussi retirés, pour qu'un
// marquage coupé par un retour à la ligne soit reconnu. file, s'il n'est pas
// vide, est le fichier lu par read_file : il est contrôlé en entier (une
// lecture partielle d'un document marqué en reste un extrait). Sans
// fournisseur local, le contenu est remplacé par classifiedWithheld et la
// session ne devient pas sensible. full, s'il n'est pas vide, est le texte
// complet d'un résultat tronqué (tools.Result.Untruncated), contrôlé lui aussi.
func (t *turn) scanResult(name, callID, content, full, file string) (string, error) {
	if name == "glob" {
		return content, nil
	}
	d := t.e.d
	var reason, path, marker string
	if name == "grep" {
		if p, ok := grepClassified(d.Classify, d.ProjectRoot, content); ok {
			reason, path = "path", p
		}
	}
	if reason == "" {
		texts := []string{content}
		if full != "" {
			texts = append(texts, full)
		}
		var files []string
		switch name {
		case "read_file":
			texts = append(texts, stripLinePrefixes(content, readPrefix))
			if file != "" {
				files = []string{file}
			}
		case "grep":
			texts = append(texts, stripLinePrefixes(content, grepPrefix))
			// Les fichiers trouvés sont contrôlés en entier : une ligne
			// trouvée sans le marquage reste un extrait du document marqué.
			files = grepFiles(d.ProjectRoot, content)
		}
		for _, x := range texts {
			if m, ok := d.Classify.FindMarker(x); ok {
				reason, marker = "marker", m
				break
			}
		}
		for _, f := range files {
			if reason != "" {
				break
			}
			if m, ok := fileMarker(d.Classify, f); ok {
				reason, marker = "marker", m
			}
		}
	}
	if reason == "" {
		return content, nil
	}
	if !t.e.hasLocal() {
		return classifiedWithheld, nil
	}
	return content, t.classify(reason, toolSource(name), callID, path, marker)
}

// checkUser contrôle le texte d'un message de l'utilisateur (ou le contexte
// de hooks qui lui est joint). Avec un fournisseur local, un marquage rend la
// session sensible ; sans, le tour est bloqué : Error{classification} puis
// TurnCompleted{blocked}, et blocked vaut true. Le texte n'est ni journalisé
// ni ajouté à l'historique.
func (t *turn) checkUser(texts ...string) (blocked bool, err error) {
	rules := t.e.d.Classify
	for _, s := range texts {
		m, ok := rules.FindMarker(s)
		if !ok {
			continue
		}
		if t.e.hasLocal() {
			return false, t.classify("marker", sourceUser, "", "", m)
		}
		msg := fmt.Sprintf("classification marker in the message (%s): %s", m, classifiedRefused)
		if err := t.emit(event.Error, event.ErrorPayload{Code: deniedClassification, Message: msg}); err != nil {
			return true, err
		}
		return true, t.done(0, "blocked")
	}
	return false, nil
}

// blockSensitive émet Error{classification} quand la session est sensible
// sans fournisseur local (session reprise) : aucun envoi n'est alors permis.
// blocked indique que l'envoi doit être refusé.
func (t *turn) blockSensitive() (blocked bool, err error) {
	sens, reason := t.s.sensitiveState()
	if !sens || t.e.hasLocal() {
		return false, nil
	}
	msg := fmt.Sprintf("session is sensitive (%s): %s", reason, classifiedRefused)
	return true, t.emit(event.Error, event.ErrorPayload{Code: deniedClassification, Message: msg})
}

// displayPath rend p relatif à la racine du projet quand il s'y trouve
// (séparateurs /), absolu sinon.
func (t *turn) displayPath(p string) string {
	root := t.e.d.ProjectRoot
	if root == "" {
		return p
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

// grepClassified cherche un chemin classé dans une sortie de grep
// (« chemin:ligne:texte », chemin relatif à la racine). Un chemin pouvant
// contenir « :<n>: », chaque préfixe suivi de « :<chiffres>: » est essayé :
// une correspondance en trop est le sens sûr.
func grepClassified(rules classify.Rules, root, out string) (string, bool) {
	if len(rules.Paths) == 0 {
		return "", false
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		for i := 0; i < len(line); i++ {
			if line[i] != ':' || !lineNumberAt(line, i) {
				continue
			}
			p := line[:i]
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			if _, ok := rules.MatchPath(root, filepath.FromSlash(p)); ok {
				return p, true
			}
		}
	}
	return "", false
}

// fileMarker cherche un marquage dans le fichier path, lu par blocs. Au
// mieux : un fichier illisible n'a pas pu être lu par l'outil non plus.
func fileMarker(rules classify.Rules, path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	m, ok, _ := rules.FindMarkerReader(f)
	return m, ok
}

// grepFiles renvoie les fichiers distincts (absolus) d'une sortie de grep.
// Chaque préfixe suivi de « :<chiffres>: » qui désigne un fichier régulier
// est retenu (un chemin peut contenir « :<n>: » : le sens sûr).
func grepFiles(root, out string) []string {
	seen := map[string]bool{}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		for i := 0; i < len(line); i++ {
			if line[i] != ':' || !lineNumberAt(line, i) {
				continue
			}
			p := filepath.Join(root, filepath.FromSlash(line[:i]))
			if line[:i] == "" || seen[p] {
				continue
			}
			seen[p] = true
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				files = append(files, p)
			}
		}
	}
	return files
}

// scanHookText contrôle un texte de hook qui entre dans l'historique sans
// passer par un résultat d'outil (retour d'un hook Stop/SubagentStop,
// contexte SessionStart de compaction). Avec un fournisseur local, un
// marquage rend la session sensible (source tool) ; sans, le texte est
// remplacé par classifiedWithheld.
func (t *turn) scanHookText(text string) (string, error) {
	return t.scanHookTextFrom(text, sourceTool)
}

// scanHookTextFrom est scanHookText avec la source SessionClassified donnée
// (sourceUser pour le contexte joint au prompt).
func (t *turn) scanHookTextFrom(text, source string) (string, error) {
	m, ok := t.e.d.Classify.FindMarker(text)
	if !ok {
		return text, nil
	}
	if !t.e.hasLocal() {
		return classifiedWithheld, nil
	}
	return text, t.classify("marker", source, "", "", m)
}

// readPrefix renvoie la longueur du préfixe « <n>\t » d'une ligne de read_file.
func readPrefix(line string) int {
	j := 0
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	if j > 0 && j < len(line) && line[j] == '\t' {
		return j + 1
	}
	return 0
}

// grepPrefix renvoie la longueur du préfixe « chemin:<n>: » d'une ligne de grep.
func grepPrefix(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] == ':' && lineNumberAt(line, i) {
			return i + 1 + strings.IndexByte(line[i+1:], ':') + 1
		}
	}
	return 0
}

// stripLinePrefixes retire de chaque ligne de s le préfixe mesuré par prefix.
func stripLinePrefixes(s string, prefix func(string) int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = l[prefix(l):]
	}
	return strings.Join(lines, "\n")
}

// lineNumberAt indique que line[i:] commence par « :<chiffres>: ».
func lineNumberAt(line string, i int) bool {
	j := i + 1
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	return j > i+1 && j < len(line) && line[j] == ':'
}

// argsMarker cherche un marquage dans les chaînes décodées des arguments (les
// échappements JSON comme \n ou É ne doivent pas le masquer), puis dans
// le texte brut.
func argsMarker(rules classify.Rules, args json.RawMessage) (string, bool) {
	var v any
	if json.Unmarshal(args, &v) == nil {
		var strs []string
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case string:
				strs = append(strs, x)
			case []any:
				for _, e := range x {
					walk(e)
				}
			case map[string]any:
				for k, e := range x {
					strs = append(strs, k)
					walk(e)
				}
			}
		}
		walk(v)
		for _, s := range strs {
			if m, ok := rules.FindMarker(s); ok {
				return m, true
			}
		}
	}
	return rules.FindMarker(string(args))
}
