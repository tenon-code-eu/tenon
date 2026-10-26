package tui

// Ce fichier gère /remember et /memory.

import (
	"errors"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/memory"
)

const (
	rememberCommand = "/remember"
	// sensitiveTitle remplace le titre d'une entrée sensible (même texte
	// que « tenon memory list »).
	sensitiveTitle = "(sensitive)"
	// noExpiry : pas d'échéance (portée project).
	noExpiry = "—"
)

// memoryOff avise que la mémoire est coupée et renvoie vrai dans ce cas.
func (m *Model) memoryOff() bool {
	if m.opts.Memory != nil {
		return false
	}
	by := m.opts.MemoryDisabledBy
	if by == "" {
		by = "config"
	}
	m.tr.notice("memory: disabled by " + by)
	return true
}

// remember traite « /remember [--project|--global] <texte> » ; args est ce
// qui suit la commande. L'option n'est reconnue qu'en premier mot.
func (m *Model) remember(args string) {
	if m.memoryOff() {
		return
	}
	scope, text := memory.User, strings.TrimSpace(args)
	first, rest, _ := strings.Cut(text, " ")
	switch first {
	case "--project":
		scope, text = memory.Project, strings.TrimSpace(rest)
	case "--global":
		scope, text = memory.Global, strings.TrimSpace(rest)
	}
	if text == "" {
		m.tr.notice(i18n.T(m.lang, "tui.remember.usage"))
		return
	}
	e, notes, err := m.eng.Remember(m.opts.SessionID, scope, text)
	if err != nil {
		if errors.Is(err, engine.ErrMemoryDisabled) {
			m.memoryOff()
			return
		}
		m.tr.notice(m.clean(err.Error()))
		return
	}
	m.tr.notice(i18n.T(m.lang, "tui.remember.saved", m.clean(string(e.Scope)), m.clean(e.ID)))
	for _, n := range notes {
		m.tr.notice(m.clean(n))
	}
}

// memoryNotices affiche /memory : « [portée] id  titre  échéance », titres
// sensibles masqués, avis du store ensuite.
func (m *Model) memoryNotices() {
	if m.memoryOff() {
		return
	}
	entries, notes := m.opts.Memory.List()
	if len(entries) == 0 {
		m.tr.notice(i18n.T(m.lang, "memory.none"))
	}
	for _, e := range entries {
		title := e.Title
		if e.Sensitive {
			title = sensitiveTitle
		}
		exp := noExpiry
		if t := m.opts.Memory.Expires(e); !t.IsZero() {
			exp = t.UTC().Format(time.DateOnly)
		}
		m.tr.notice("[" + m.clean(string(e.Scope)) + "] " + m.clean(e.ID) + "  " + m.clean(title) + "  " + exp)
	}
	for _, n := range notes {
		m.tr.notice(m.clean(n))
	}
}
