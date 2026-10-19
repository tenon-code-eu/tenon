package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// claudeNames associe le nom d'un outil tenon à son nom Claude Code.
// Les outils absents (mcp__*, outils inconnus) gardent leur nom.
var claudeNames = map[string]string{
	"shell":      "Bash",
	"read_file":  "Read",
	"write_file": "Write",
	"edit_file":  "Edit",
	"grep":       "Grep",
	"glob":       "Glob",
	"skill":      "Skill",
	"agent":      "Task",
}

// fieldRename décrit le renommage d'un argument entre tenon et Claude Code.
type fieldRename struct{ tenon, claude string }

// argRenames liste les renommages d'arguments par outil tenon.
// Le timeout de shell (secondes ↔ millisecondes) est traité à part.
var argRenames = map[string][]fieldRename{
	"read_file":  {{"path", "file_path"}},
	"write_file": {{"path", "file_path"}},
	"edit_file":  {{"path", "file_path"}},
	"skill":      {{"name", "skill"}},
	"agent":      {{"agent", "subagent_type"}},
}

var errNotObject = errors.New("updatedInput must be an object")
var errInvalidTimeout = errors.New("invalid timeout in updatedInput")

// ClaudeName renvoie le nom Claude Code de l'outil tenon, ou le nom inchangé.
func ClaudeName(tenon string) string {
	if name, ok := claudeNames[tenon]; ok {
		return name
	}
	return tenon
}

// ToClaude traduit les arguments d'un outil tenon vers le format Claude Code.
// Des arguments qui ne sont pas un objet JSON sont renvoyés inchangés.
func ToClaude(tenon string, args json.RawMessage) json.RawMessage {
	m, ok := decodeObject(args)
	if !ok {
		return args
	}
	for _, r := range argRenames[tenon] {
		renameField(m, r.tenon, r.claude)
	}
	if tenon == "shell" {
		shellTimeoutToClaude(m)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}

// FromClaude traduit des arguments au format Claude Code (updatedInput d'un hook)
// vers le format de l'outil tenon. in doit être un objet JSON.
func FromClaude(tenon string, in json.RawMessage) (json.RawMessage, error) {
	m, ok := decodeObject(in)
	if !ok {
		return nil, errNotObject
	}
	for _, r := range argRenames[tenon] {
		renameField(m, r.claude, r.tenon)
	}
	if tenon == "shell" {
		if err := shellTimeoutFromClaude(m); err != nil {
			return nil, err
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// decodeObject décode un objet JSON en valeurs brutes, pour garder les champs non traduits intacts.
func decodeObject(data json.RawMessage) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// renameField déplace from vers to. La clé source gagne : to est écrasée si elle existe,
// et rien ne bouge si from est absente. Il ne reste ainsi qu'une clé par notion.
func renameField(m map[string]json.RawMessage, from, to string) {
	raw, ok := m[from]
	if !ok {
		return
	}
	delete(m, from)
	m[to] = raw
}

// isNumber indique si la valeur brute est un nombre JSON (null, booléen, chaîne exclus).
func isNumber(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && (trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9'))
}

// shellTimeoutToClaude convertit timeout_seconds (s, entier) en timeout (ms), la source gagnant.
// Une valeur non entière est recopiée telle quelle sous timeout.
func shellTimeoutToClaude(m map[string]json.RawMessage) {
	raw, ok := m["timeout_seconds"]
	if !ok {
		return
	}
	var s int64
	if !isNumber(raw) || json.Unmarshal(raw, &s) != nil || s > math.MaxInt64/1000 || s < math.MinInt64/1000 {
		renameField(m, "timeout_seconds", "timeout")
		return
	}
	delete(m, "timeout_seconds")
	m["timeout"] = json.RawMessage(strconv.FormatInt(s*1000, 10))
}

// shellTimeoutFromClaude convertit timeout (ms) en timeout_seconds (s, arrondi au supérieur),
// la source gagnant. Un timeout non numérique ou négatif (-0 compris) est une erreur.
func shellTimeoutFromClaude(m map[string]json.RawMessage) error {
	raw, ok := m["timeout"]
	if !ok {
		return nil
	}
	var ms float64
	if !isNumber(raw) || json.Unmarshal(raw, &ms) != nil || ms < 0 || math.Signbit(ms) {
		return errInvalidTimeout
	}
	s := math.Ceil(ms / 1000)
	delete(m, "timeout")
	m["timeout_seconds"] = json.RawMessage(strconv.FormatFloat(s, 'f', -1, 64))
	return nil
}
