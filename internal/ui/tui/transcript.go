// Package tui contient l'interface interactive de tenon. Ce fichier est la
// logique pure (sans Bubble Tea) qui transforme les événements de l'engine en
// éléments affichables.
package tui

import (
	"encoding/json"
	"strings"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/ui/headless"
)

// ItemKind distingue les éléments affichables.
type ItemKind int

const (
	ItemBanner ItemKind = iota
	ItemUser
	ItemAssistant
	ItemTool
	ItemNotice
	ItemError
)

const maxDetailRunes = 120

// Item est un élément de la conversation affichée.
type Item struct {
	Kind      ItemKind
	Text      string // texte affiché (assistant : accumulé pendant le streaming)
	Tool      string // ItemTool
	Detail    string // ItemTool : commande ou chemins ; résultat abrégé
	Status    string // ItemTool : "pending", "ok", "error", "denied"
	Streaming bool   // ItemAssistant en cours
	callID    string
	reqDetail string
}

// Transcript accumule les éléments affichables d'une session.
type Transcript struct {
	lang  i18n.Lang
	items []Item
	busy  bool
}

// NewTranscript crée une transcription vide dans la langue l.
func NewTranscript(l i18n.Lang) *Transcript {
	if l != i18n.EN {
		l = i18n.FR
	}
	return &Transcript{lang: l}
}

// Items renvoie une copie des éléments.
func (t *Transcript) Items() []Item { return append([]Item(nil), t.items...) }

// Busy indique qu'un tour est en cours (entre UserMessage et TurnCompleted).
func (t *Transcript) Busy() bool { return t.busy }

func (t *Transcript) add(it Item) { t.items = append(t.items, it) }

func (t *Transcript) notice(text string) { t.add(Item{Kind: ItemNotice, Text: text}) }

func (t *Transcript) decodeFailed(ev event.Event) {
	t.add(Item{Kind: ItemError, Text: i18n.T(t.lang, "tui.unreadable", ev.Type)})
}

// lastStreaming renvoie l'index du dernier assistant en streaming, ou -1.
func (t *Transcript) lastStreaming() int {
	for i := len(t.items) - 1; i >= 0; i-- {
		if t.items[i].Kind == ItemAssistant && t.items[i].Streaming {
			return i
		}
	}
	return -1
}

// Apply intègre un événement ; les types inconnus sont ignorés.
func (t *Transcript) Apply(ev event.Event) {
	switch ev.Type {
	case event.SessionStarted:
		var p event.SessionStartedPayload
		if !t.decode(ev, &p) {
			return
		}
		text := headless.Banner(t.lang, p.Provider)
		if p.Resumed {
			text += " " + i18n.T(t.lang, "tui.resumed")
		}
		if len(p.Instructions) > 0 {
			text += "\n" + i18n.T(t.lang, "banner.instructions", strings.Join(p.Instructions, ", "))
		}
		t.add(Item{Kind: ItemBanner, Text: text})
	case event.UserMessage:
		var p event.UserMessagePayload
		if !t.decode(ev, &p) {
			return
		}
		t.busy = true
		t.add(Item{Kind: ItemUser, Text: p.Text})
	case event.AssistantDelta:
		var p event.AssistantDeltaPayload
		if !t.decode(ev, &p) {
			return
		}
		if i := t.lastStreaming(); i >= 0 {
			t.items[i].Text += p.Text
			return
		}
		t.add(Item{Kind: ItemAssistant, Text: p.Text, Streaming: true})
	case event.AssistantMessage:
		var p event.AssistantMessagePayload
		if !t.decode(ev, &p) {
			return
		}
		if i := t.lastStreaming(); i >= 0 {
			t.items[i].Text = p.Text
			t.items[i].Streaming = false
			return
		}
		t.add(Item{Kind: ItemAssistant, Text: p.Text})
	case event.ToolCallRequested:
		var p event.ToolCallRequestedPayload
		if !t.decode(ev, &p) {
			return
		}
		d := requestDetail(p.Args)
		t.add(Item{Kind: ItemTool, Tool: p.Tool, Detail: d, reqDetail: d, Status: "pending", callID: p.CallID})
	case event.ToolResult:
		var p event.ToolResultPayload
		if !t.decode(ev, &p) {
			return
		}
		t.toolResult(p)
	case event.JurisdictionWarning:
		var p event.JurisdictionWarningPayload
		if !t.decode(ev, &p) {
			return
		}
		t.notice(i18n.T(t.lang, "warn.non_eu", providerLabel(p.Provider), jurisdictionLabel(t.lang, p.Provider.Jurisdiction)))
	case event.ContextCompacted:
		var p event.ContextCompactedPayload
		if !t.decode(ev, &p) {
			return
		}
		t.notice(i18n.T(t.lang, "compaction.done", p.Replaced))
	case event.RedactionApplied:
		var p event.RedactionAppliedPayload
		if !t.decode(ev, &p) {
			return
		}
		n := 0
		for _, c := range p.Counts {
			n += c
		}
		t.notice(i18n.T(t.lang, "tui.redaction", n))
	case event.Error:
		var p event.ErrorPayload
		if !t.decode(ev, &p) {
			return
		}
		msg := p.Message
		if p.Code == "context_too_large" {
			msg = i18n.T(t.lang, "err.context_too_large")
		}
		t.add(Item{Kind: ItemError, Text: i18n.T(t.lang, "err.event", p.Code, msg)})
	case event.TurnCompleted:
		var p event.TurnCompletedPayload
		if !t.decode(ev, &p) {
			return
		}
		t.busy = false
		switch p.Reason {
		case "max_iterations", "incomplete", "blocked", "cancelled", "context_too_large":
			t.notice(i18n.T(t.lang, "tui.turn_reason."+p.Reason))
		}
	}
}

func (t *Transcript) toolResult(p event.ToolResultPayload) {
	for i := len(t.items) - 1; i >= 0; i-- {
		it := &t.items[i]
		if it.Kind != ItemTool || it.callID != p.CallID {
			continue
		}
		switch {
		case p.Denied != "":
			it.Status = "denied"
		case p.IsError:
			it.Status = "error"
		default:
			it.Status = "ok"
		}
		it.Detail = it.reqDetail
		if line := firstLine(p.Content); line != "" {
			if it.Detail != "" {
				it.Detail += " · "
			}
			it.Detail += line
		}
		return
	}
}

func (t *Transcript) decode(ev event.Event, v any) bool {
	if err := json.Unmarshal(ev.Payload, v); err != nil {
		t.decodeFailed(ev)
		return false
	}
	return true
}

// requestDetail extrait la commande ou les chemins des arguments d'un outil.
func requestDetail(args json.RawMessage) string {
	var a struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
	}
	if json.Unmarshal(args, &a) != nil {
		return ""
	}
	var parts []string
	for _, s := range []string{a.Command, a.Path, a.Pattern} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// firstLine renvoie la première ligne non vide de s, abrégée à 120 runes.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if r := []rune(l); len(r) > maxDetailRunes {
			return string(r[:maxDetailRunes-1]) + "…"
		}
		return l
	}
	return ""
}

func providerLabel(p llm.ProviderInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

func jurisdictionLabel(l i18n.Lang, j llm.Jurisdiction) string {
	switch j {
	case llm.EU:
		return i18n.T(l, "banner.jurisdiction.eu")
	case llm.Local:
		return i18n.T(l, "banner.jurisdiction.local")
	}
	return i18n.T(l, "banner.jurisdiction.non_eu")
}
