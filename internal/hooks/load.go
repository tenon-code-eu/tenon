// Package hooks charge, filtre et exécute les hooks utilisateur, au format
// compatible avec les hooks de Claude Code.
package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/safefile"
)

const (
	// MaxHooks borne le nombre total de hooks chargés.
	MaxHooks = 200
	// MaxFileBytes borne la taille d'un fichier de hooks (1 MiB).
	MaxFileBytes = 1 << 20
)

// Event est le nom d'un événement de hook.
type Event string

const (
	PreToolUse       Event = "PreToolUse"
	PostToolUse      Event = "PostToolUse"
	UserPromptSubmit Event = "UserPromptSubmit"
	Stop             Event = "Stop"
	SubagentStop     Event = "SubagentStop"
	SessionStart     Event = "SessionStart"
	SessionEnd       Event = "SessionEnd"
	PreCompact       Event = "PreCompact"
	Notification     Event = "Notification"
)

// events fixe l'ordre de parcours (déterministe) des événements d'un fichier.
var events = []Event{PreToolUse, PostToolUse, UserPromptSubmit, Stop, SubagentStop,
	SessionStart, SessionEnd, PreCompact, Notification}

// Type est le type d'un hook.
type Type string

const (
	Command Type = "command"
	Prompt  Type = "prompt"
	Agent   Type = "agent"
)

// Source indique d'où vient un hook.
type Source string

const (
	SourceTenon  Source = "tenon"
	SourceClaude Source = "claude"
)

// Notice est un avertissement non bloquant du chargement.
type Notice string

// Hook est un hook compilé et validé.
type Hook struct {
	ID      string // <source>:<event>:<i>.<j>
	Source  Source
	Event   Event
	Matcher string
	Type    Type
	Command string
	Prompt  string
	Model   string
	Timeout time.Duration

	re *regexp.Regexp // nil : tout correspond
}

// Options règle Load.
type Options struct {
	Getenv      func(string) string
	ProjectRoot string
	Config      config.Hooks
}

// Set est l'ensemble des hooks chargés, dans l'ordre de configuration.
type Set struct {
	hooks []Hook
}

// Load lit les fichiers de hooks utilisateur. Une erreur de fichier utilisateur
// est fatale (fail closed) ; les fichiers projet ne produisent que des notices.
func Load(o Options) (*Set, []Notice, error) {
	s := &Set{}
	if o.Config.Disabled {
		return s, nil, nil
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var notices []Notice
	home := getenv("HOME")
	xdg := getenv("XDG_CONFIG_HOME")
	// Même règle que skills.Dirs : une base vide ou relative se résoudrait
	// contre le cwd (donc le projet), on l'écarte.
	if !filepath.IsAbs(xdg) {
		xdg = ""
		if filepath.IsAbs(home) {
			xdg = filepath.Join(home, ".config")
		}
	}
	type src struct {
		path   string
		source Source
	}
	var srcs []src
	if xdg != "" {
		srcs = append(srcs, src{filepath.Join(xdg, "tenon", "hooks.json"), SourceTenon})
	}
	if o.Config.ClaudeSettings && filepath.IsAbs(home) {
		srcs = append(srcs, src{filepath.Join(home, ".claude", "settings.json"), SourceClaude})
	}
	for _, f := range srcs {
		hs, ns, err := loadFile(f.path, f.source)
		if err != nil {
			return nil, nil, err
		}
		notices = append(notices, ns...)
		s.hooks = append(s.hooks, hs...)
		if len(s.hooks) > MaxHooks {
			return nil, nil, fmt.Errorf("%s: too many hooks (max %d)", filepath.Base(f.path), MaxHooks)
		}
	}
	if o.ProjectRoot != "" {
		notices = append(notices, projectNotices(o.ProjectRoot)...)
	}
	return s, notices, nil
}

// projectNotices signale les fichiers de hooks du projet, jamais exécutés.
func projectNotices(root string) []Notice {
	var out []Notice
	ignored := func(p string) Notice { return Notice("project hooks ignored: " + p) }
	p := filepath.Join(root, ".tenon", "hooks.json")
	if _, _, ok, _ := safefile.ReadRegular(p, MaxFileBytes); ok {
		out = append(out, ignored(p))
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		p := filepath.Join(root, ".claude", name)
		data, truncated, ok, err := safefile.ReadRegular(p, MaxFileBytes)
		if err != nil || !ok {
			continue
		}
		var top map[string]json.RawMessage
		if truncated || json.Unmarshal(data, &top) != nil {
			out = append(out, Notice("project hooks file unreadable: "+p))
			continue
		}
		if _, has := top["hooks"]; has {
			out = append(out, ignored(p))
		}
	}
	return out
}

type group struct {
	Matcher json.RawMessage `json:"matcher"`
	Hooks   json.RawMessage `json:"hooks"`
}

type rawHook struct {
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Prompt  string          `json:"prompt"`
	Model   string          `json:"model"`
	Timeout json.RawMessage `json:"timeout"`
}

// loadFile lit et valide un fichier utilisateur. Fichier absent : aucun hook.
func loadFile(path string, source Source) ([]Hook, []Notice, error) {
	base := filepath.Base(path)
	// Un fichier utilisateur peut être un lien symbolique (dotfiles).
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("%s: %v", base, err)
	}
	data, truncated, ok, err := safefile.ReadRegular(real, MaxFileBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %v", base, err)
	}
	if !ok {
		return nil, nil, nil
	}
	if truncated {
		return nil, nil, fmt.Errorf("%s: file too large (max %d bytes)", base, MaxFileBytes)
	}
	var top map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&top); err != nil {
		return nil, nil, fmt.Errorf("%s: invalid JSON: %v", base, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, fmt.Errorf("%s: invalid JSON: trailing data", base)
	}
	if raw, has := top["disableAllHooks"]; has {
		var off bool
		if err := json.Unmarshal(raw, &off); err != nil {
			return nil, nil, fmt.Errorf("%s: disableAllHooks: must be a boolean", base)
		}
		if off {
			return nil, nil, nil
		}
	}
	rawHooks, has := top["hooks"]
	if !has {
		return nil, nil, nil
	}
	var byEvent map[string]json.RawMessage
	if err := json.Unmarshal(rawHooks, &byEvent); err != nil || byEvent == nil {
		return nil, nil, fmt.Errorf("%s: hooks: must be an object", base)
	}
	known := map[string]bool{}
	for _, ev := range events {
		known[string(ev)] = true
	}
	var notices []Notice
	for name := range byEvent {
		if !known[name] {
			notices = append(notices, Notice(fmt.Sprintf("%s: unknown event %q ignored", base, name)))
		}
	}
	var out []Hook
	for _, ev := range events {
		raw, has := byEvent[string(ev)]
		if !has {
			continue
		}
		epath := "hooks." + string(ev)
		var groups []group
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, nil, fmt.Errorf("%s: %s: must be an array of groups", base, epath)
		}
		for i, g := range groups {
			gpath := fmt.Sprintf("%s[%d]", epath, i)
			matcher, re, err := parseMatcher(g.Matcher)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %s.matcher: %v", base, gpath, err)
			}
			var raws []json.RawMessage
			if err := json.Unmarshal(g.Hooks, &raws); err != nil {
				return nil, nil, fmt.Errorf("%s: %s.hooks: must be an array", base, gpath)
			}
			for j, rr := range raws {
				hpath := fmt.Sprintf("%s.hooks[%d]", gpath, j)
				var rh rawHook
				hd := json.NewDecoder(bytes.NewReader(rr))
				hd.UseNumber()
				if err := hd.Decode(&rh); err != nil {
					return nil, nil, fmt.Errorf("%s: %s: invalid hook: %v", base, hpath, err)
				}
				t := Type(rh.Type)
				if t != Command && t != Prompt && t != Agent {
					notices = append(notices, Notice(fmt.Sprintf("%s: %s: unknown type %q ignored", base, hpath, rh.Type)))
					continue
				}
				h := Hook{
					ID:      fmt.Sprintf("%s:%s:%d.%d", source, ev, i, j),
					Source:  source,
					Event:   ev,
					Matcher: matcher,
					Type:    t,
					Command: rh.Command,
					Prompt:  rh.Prompt,
					Model:   rh.Model,
					re:      re,
				}
				switch t {
				case Command:
					if rh.Command == "" {
						return nil, nil, fmt.Errorf("%s: %s: command must not be empty", base, hpath)
					}
					h.Timeout = 60 * time.Second
				case Prompt:
					if rh.Prompt == "" {
						return nil, nil, fmt.Errorf("%s: %s: prompt must not be empty", base, hpath)
					}
					h.Timeout = 30 * time.Second
				case Agent:
					if rh.Prompt == "" {
						return nil, nil, fmt.Errorf("%s: %s: prompt must not be empty", base, hpath)
					}
					h.Timeout = 60 * time.Second
				}
				if len(rh.Timeout) > 0 && string(rh.Timeout) != "null" {
					n, err := strconv.ParseInt(string(rh.Timeout), 10, 64)
					if err != nil || n <= 0 || n > 86400 {
						return nil, nil, fmt.Errorf("%s: %s: timeout must be a positive integer", base, hpath)
					}
					h.Timeout = time.Duration(n) * time.Second
				}
				out = append(out, h)
				if len(out) > MaxHooks {
					return nil, nil, fmt.Errorf("%s: too many hooks (max %d)", base, MaxHooks)
				}
			}
		}
	}
	return out, notices, nil
}

// parseMatcher compile un matcher RE2 ancré ; vide, absent ou "*" : tout.
func parseMatcher(raw json.RawMessage) (string, *regexp.Regexp, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var m string
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", nil, errors.New("must be a string")
	}
	if m == "" || m == "*" {
		return m, nil, nil
	}
	re, err := regexp.Compile("^(?:" + m + ")$")
	if err != nil {
		return "", nil, fmt.Errorf("invalid regex: %v", err)
	}
	return m, re, nil
}

// Match renvoie les hooks de l'événement dont le matcher accepte l'un des
// sujets, dans l'ordre de configuration. Sans sujet, le matcher est ignoré.
// Deux hooks command de même commande : le premier est gardé.
func (s *Set) Match(ev Event, subjects ...string) []Hook {
	if s == nil {
		return nil
	}
	var out []Hook
	seen := map[string]bool{}
	for _, h := range s.hooks {
		if h.Event != ev || !h.matches(subjects) {
			continue
		}
		if h.Type == Command {
			if seen[h.Command] {
				continue
			}
			seen[h.Command] = true
		}
		out = append(out, h)
	}
	return out
}

func (h Hook) matches(subjects []string) bool {
	if h.re == nil || len(subjects) == 0 {
		return true
	}
	for _, sub := range subjects {
		if h.re.MatchString(sub) {
			return true
		}
	}
	return false
}

// List renvoie tous les hooks chargés.
func (s *Set) List() []Hook {
	if s == nil {
		return nil
	}
	return append([]Hook(nil), s.hooks...)
}

// Count renvoie le nombre total de hooks et celui issu de Claude Code.
func (s *Set) Count() (total, claude int) {
	if s == nil {
		return 0, 0
	}
	for _, h := range s.hooks {
		if h.Source == SourceClaude {
			claude++
		}
	}
	return len(s.hooks), claude
}
