package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/ui/headless"
)

// noticePrefix précède le texte d'un avis de tenon.
const noticePrefix = "tenon: "

// maxTitle borne la cible d'un titre d'appel d'outil, en runes.
const maxTitle = 200

// Identifiants des options de permission.
const (
	optAllow        = "allow"
	optAllowSession = "allow_session"
	optAllowNetwork = "allow_network"
	optReject       = "reject"
)

// toolKind classe un outil tenon selon les genres ACP.
func toolKind(name string) string {
	switch name {
	case "read_file":
		return "read"
	case "write_file", "edit_file":
		return "edit"
	case "glob", "grep":
		return "search"
	case "shell":
		return "execute"
	case "agent":
		return "think"
	}
	return "other"
}

// stopReason traduit la raison d'un TurnCompleted en stopReason ACP. Les
// tours bloqués ou arrêtés par un hook finissent en end_turn, après une
// notice émise par le pont.
func stopReason(turnReason string) string {
	switch turnReason {
	case "max_iterations":
		return "max_turn_requests"
	case "incomplete":
		return "max_tokens"
	case "cancelled":
		return "cancelled"
	}
	return "end_turn"
}

// toolCallID est l'identifiant ACP d'un appel : l'identifiant d'appel du
// LLM, préfixé par l'appel agent parent pour un sous-agent (« a1/c1 ») : deux
// agents peuvent produire le même identifiant.
func toolCallID(parent, id string) string {
	if parent == "" {
		return id
	}
	return parent + "/" + id
}

// openCall est l'appel d'outil en cours d'un agent (principal ou sous-agent).
type openCall struct{ id, tool string }

// bridge relaie les événements d'une session vers l'éditeur
// (session/update) et lui pose les demandes de permission
// (session/request_permission). Abonné à l'enregistrement de la session, sa
// pompe tourne jusqu'à la fermeture : rien n'est perdu entre deux tours.
type bridge struct {
	conn *Conn
	lang i18n.Lang
	s    Session
	sid  string
	root string
	ch   <-chan event.Event

	flushReq chan chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	exited   chan struct{}

	// État de la pompe (sa goroutine seule).
	open        map[string]openCall        // ParentCallID → appel en cours
	permCall    map[string]string          // PermissionID → toolCallId
	args        map[string]json.RawMessage // toolCallId → arguments masqués, jusqu'au résultat
	routing     headless.Routing
	hookNoticed bool  // une notice de hook a expliqué l'arrêt du tour
	tokens      int64 // jetons de la session (entrée + sortie)

	mu        sync.Mutex
	reason    string // raison du dernier TurnCompleted du tour
	cancelled bool   // session/cancel reçu pour le tour en cours
	perms     map[string]context.CancelFunc
}

func newBridge(conn *Conn, lang i18n.Lang, s Session) *bridge {
	return &bridge{
		conn: conn, lang: lang, s: s, sid: s.ID(), root: s.ProjectRoot(), ch: s.Subscribe(),
		flushReq: make(chan chan struct{}), stop: make(chan struct{}), exited: make(chan struct{}),
		open: map[string]openCall{}, permCall: map[string]string{}, args: map[string]json.RawMessage{}, perms: map[string]context.CancelFunc{},
	}
}

// start envoie les notices, les commandes et les événements rejoués, puis
// lance la pompe. Appelé une fois la réponse session/new ou session/load
// écrite.
func (b *bridge) start(notices []string, cmds []Command, replay []event.Event) {
	for _, n := range notices {
		b.notice(n)
	}
	list := make([]map[string]any, 0, len(cmds))
	for _, c := range cmds {
		list = append(list, map[string]any{"name": c.Name, "description": c.Description})
	}
	b.update(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": list})
	for _, ev := range replay {
		if ev, ok := b.mask(ev); ok {
			b.replay(ev)
		}
	}
	go b.run()
}

func (b *bridge) run() {
	defer close(b.exited)
	for {
		select {
		case ev, ok := <-b.ch:
			if !ok {
				return
			}
			b.live(ev)
		case done := <-b.flushReq:
			// Submit a rendu la main : tous les événements du tour sont
			// déjà dans le canal.
			for drained := false; !drained; {
				select {
				case ev, ok := <-b.ch:
					if !ok {
						close(done)
						return
					}
					b.live(ev)
				default:
					drained = true
				}
			}
			close(done)
		case <-b.stop:
			return
		}
	}
}

// close arrête la pompe et abandonne les demandes de permission en cours.
func (b *bridge) close() {
	b.stopOnce.Do(func() { close(b.stop) })
	b.cancelPerms()
}

// beginTurn prépare un tour.
func (b *bridge) beginTurn() {
	b.mu.Lock()
	b.reason, b.cancelled = "", false
	b.mu.Unlock()
}

// cancel traite session/cancel : les demandes en cours sont abandonnées
// (refus) et les suivantes du tour refusées sans être posées.
func (b *bridge) cancel() {
	b.mu.Lock()
	b.cancelled = true
	b.mu.Unlock()
	b.cancelPerms()
}

func (b *bridge) cancelPerms() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.perms {
		c()
	}
}

// flush attend que la pompe ait traité les événements du tour et renvoie la
// raison du TurnCompleted reçu ("" sans lui).
func (b *bridge) flush() string {
	done := make(chan struct{})
	select {
	case b.flushReq <- done:
		select {
		case <-done:
		case <-b.exited:
		}
	case <-b.exited:
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reason
}

func (b *bridge) update(u map[string]any) {
	_ = b.conn.Notify("session/update", map[string]any{"sessionId": b.sid, "update": u})
}

func textBlock(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// notice envoie un avis de tenon comme pensée de l'agent : les clients
// standard ignorent une mise à jour de type inconnu.
func (b *bridge) notice(text string) {
	b.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textBlock(noticePrefix + text)})
}

func (b *bridge) chunk(kind, text string) {
	if text == "" {
		return
	}
	b.update(map[string]any{"sessionUpdate": kind, "content": textBlock(text)})
}

func decodeAs[T any](ev event.Event) (T, bool) {
	var p T
	err := json.Unmarshal(ev.Payload, &p)
	return p, err == nil
}

// mask masque la charge utile de ev avant tout envoi à l'éditeur : le moteur
// ne masque que vers le fournisseur. Comme --output json, fermeture par
// défaut : une charge que le masquage rend illisible n'est pas envoyée.
func (b *bridge) mask(ev event.Event) (event.Event, bool) {
	masked := b.s.RedactPayload(string(ev.Payload))
	if !json.Valid([]byte(masked)) {
		return ev, false
	}
	ev.Payload = json.RawMessage(masked)
	return ev, true
}

// live masque puis traduit un événement du direct. Une demande de
// permission illisible une fois masquée est refusée sans être posée.
func (b *bridge) live(ev event.Event) {
	m, ok := b.mask(ev)
	if ok {
		b.handle(m)
		return
	}
	if ev.Type == event.PermissionRequested {
		if p, ok := decodeAs[event.PermissionRequestedPayload](ev); ok {
			_ = b.s.Respond(p.PermissionID, engine.Reply{})
		}
	}
}

// handle traduit un événement du direct, déjà masqué.
func (b *bridge) handle(ev event.Event) {
	switch ev.Type {
	case event.AssistantDelta:
		// Seul l'agent principal diffuse des fragments.
		if p, ok := decodeAs[event.AssistantDeltaPayload](ev); ok {
			b.chunk("agent_message_chunk", p.Text)
		}
	case event.ToolCallRequested:
		if p, ok := decodeAs[event.ToolCallRequestedPayload](ev); ok {
			b.open[p.ParentCallID] = openCall{p.CallID, p.Tool}
			if p.Tool == "write_file" || p.Tool == "edit_file" {
				b.args[toolCallID(p.ParentCallID, p.CallID)] = p.Args
			}
			b.update(b.toolCall(p))
		}
	case event.PermissionRequested:
		if p, ok := decodeAs[event.PermissionRequestedPayload](ev); ok {
			b.ask(p)
		}
	case event.PermissionResolved:
		if p, ok := decodeAs[event.PermissionResolvedPayload](ev); ok {
			id, found := b.permCall[p.PermissionID]
			delete(b.permCall, p.PermissionID)
			if found && p.Allowed {
				b.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": "in_progress"})
			}
		}
	case event.ToolResult:
		if p, ok := decodeAs[event.ToolResultPayload](ev); ok {
			if b.open[p.ParentCallID].id == p.CallID {
				delete(b.open, p.ParentCallID)
			}
			b.update(b.toolResult(p, true))
			delete(b.args, toolCallID(p.ParentCallID, p.CallID))
		}
	case event.Usage:
		if p, ok := decodeAs[event.UsagePayload](ev); ok {
			b.tokens += int64(p.Input) + int64(p.Output)
			if p.ParentCallID == "" && (p.Purpose == "turn" || p.Purpose == "") {
				b.update(map[string]any{
					"sessionUpdate": "usage_update",
					"used":          p.Input + p.Output,
					"_meta": map[string]any{"tenon": map[string]any{
						"input": p.Input, "output": p.Output, "cached": p.Cached, "sessionTokens": b.tokens,
					}},
				})
			}
		}
	case event.HookCompleted:
		if p, ok := decodeAs[event.HookCompletedPayload](ev); ok && p.ParentCallID == "" {
			switch {
			case p.Stop:
				b.notice(i18n.T(b.lang, "headless.hook_stopped", headless.OneLine(p.StopReason)))
				b.hookNoticed = true
			case p.Status == "blocked":
				b.notice(i18n.T(b.lang, "headless.hook_blocked", headless.OneLine(p.HookID), headless.OneLine(p.Reason)))
				b.hookNoticed = true
			}
		}
	case event.TurnCompleted:
		if p, ok := decodeAs[event.TurnCompletedPayload](ev); ok {
			b.turnCompleted(p.Reason)
		}
	case event.SessionStarted:
		if p, ok := decodeAs[event.SessionStartedPayload](ev); ok {
			b.routing.Started(p)
		}
	case event.SessionClassified:
		if p, ok := decodeAs[event.SessionClassifiedPayload](ev); ok {
			b.notice(b.routing.Classified(b.lang, p))
		}
	case event.ProviderSelected:
		if p, ok := decodeAs[event.ProviderSelectedPayload](ev); ok {
			if n := b.routing.Selected(b.lang, p); n != "" {
				b.notice(n)
			}
		}
	case event.McpServerDown:
		if p, ok := decodeAs[event.McpServerDownPayload](ev); ok {
			b.notice(headless.ServerDownNotice(b.lang, p, b.s.Redact))
		}
	case event.JurisdictionWarning:
		if p, ok := decodeAs[event.JurisdictionWarningPayload](ev); ok {
			b.notice(i18n.T(b.lang, "warn.non_eu", headless.OneLine(headless.ProviderLabel(p.Provider)),
				headless.JurisdictionLabel(b.lang, p.Provider.Jurisdiction)))
		}
	case event.BudgetWarning:
		if p, ok := decodeAs[event.BudgetWarningPayload](ev); ok && p.Max > 0 {
			b.notice(i18n.T(b.lang, "budget.warning", p.Used, p.Max, p.Used*100/p.Max))
		}
	}
}

// turnCompleted note la raison du tour et explique par une notice un tour
// bloqué ou arrêté (sauf si un hook l'a déjà fait).
func (b *bridge) turnCompleted(reason string) {
	b.mu.Lock()
	b.reason = reason
	b.mu.Unlock()
	noticed := b.hookNoticed
	b.hookNoticed = false
	switch reason {
	case "blocked":
		b.notice(i18n.T(b.lang, "turn.blocked"))
	case "context_too_large":
		b.notice(i18n.T(b.lang, "err.context_too_large"))
	case "budget_exceeded":
		b.notice(i18n.T(b.lang, "tui.turn_reason.budget_exceeded"))
	case "blocked_by_hook":
		if !noticed {
			b.notice(i18n.T(b.lang, "turn.blocked_by_hook"))
		}
	case "stopped_by_hook":
		if !noticed {
			b.notice(i18n.T(b.lang, "turn.ended", reason))
		}
	}
}

// replay traduit un événement rejoué (déjà masqué) par session/load : messages de
// l'agent principal et appels d'outils (résultats en texte seul : le disque
// n'est plus dans l'état d'alors).
func (b *bridge) replay(ev event.Event) {
	switch ev.Type {
	case event.UserMessage:
		if p, ok := decodeAs[event.UserMessagePayload](ev); ok && p.ParentCallID == "" && !p.Blocked && p.Hook == "" {
			text := p.Text
			if p.Typed != "" {
				text = p.Typed
			}
			b.chunk("user_message_chunk", text)
		}
	case event.AssistantMessage:
		if p, ok := decodeAs[event.AssistantMessagePayload](ev); ok && p.ParentCallID == "" {
			b.chunk("agent_message_chunk", p.Text)
		}
	case event.ToolCallRequested:
		if p, ok := decodeAs[event.ToolCallRequestedPayload](ev); ok {
			b.update(b.toolCall(p))
		}
	case event.ToolResult:
		if p, ok := decodeAs[event.ToolResultPayload](ev); ok {
			b.update(b.toolResult(p, false))
		}
	}
}

// toolArgs sont les champs des arguments qui désignent la cible d'un appel.
type toolArgs struct {
	Path        string `json:"path"`
	Command     string `json:"command"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
}

func (b *bridge) toolCall(p event.ToolCallRequestedPayload) map[string]any {
	var a toolArgs
	_ = json.Unmarshal(p.Args, &a)
	title := headless.OneLine(p.Tool)
	target := a.Path
	for _, t := range []string{a.Command, a.Pattern, a.Description} {
		if target == "" {
			target = t
		}
	}
	if target = clip(headless.OneLine(target), maxTitle); target != "" {
		title += " · " + target
	}
	u := map[string]any{
		"sessionUpdate": "tool_call", "toolCallId": toolCallID(p.ParentCallID, p.CallID), "title": title,
		"kind": toolKind(p.Tool), "status": "pending",
	}
	if len(p.Args) > 0 && json.Valid(p.Args) {
		u["rawInput"] = p.Args
	}
	switch p.Tool {
	case "read_file", "write_file", "edit_file":
		if a.Path != "" {
			u["locations"] = []map[string]any{{"path": b.abs(a.Path)}}
		}
	}
	return u
}

func (b *bridge) toolResult(p event.ToolResultPayload, live bool) map[string]any {
	status := "completed"
	if p.IsError || p.Denied != "" {
		status = "failed"
	}
	u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": toolCallID(p.ParentCallID, p.CallID), "status": status}
	if status == "completed" && live && (p.Tool == "write_file" || p.Tool == "edit_file") {
		if d := b.diff(p); d != nil {
			u["content"] = []map[string]any{d}
			return u
		}
	}
	// Contenu déjà masqué et tronqué par le moteur ; nettoyé comme en headless.
	if text := headless.Clean(p.Content); text != "" {
		u["content"] = []map[string]any{{"type": "content", "content": textBlock(text)}}
	}
	return u
}

// diff construit le contenu diff d'une écriture, sans jamais lire le
// disque (masquage, et la pompe est asynchrone) : ancien texte depuis le
// point de reprise, masqué ; nouveau texte depuis les arguments masqués de
// l'appel (write_file : content ; edit_file : old_string → new_string
// appliqué à l'ancien texte). Fichier nouveau : oldText absent. nil : texte
// seul.
func (b *bridge) diff(p event.ToolResultPayload) map[string]any {
	args, ok := b.args[toolCallID(p.ParentCallID, p.CallID)]
	if !ok || p.Path == "" {
		return nil
	}
	var a struct {
		Content    *string `json:"content"`
		OldString  string  `json:"old_string"`
		NewString  string  `json:"new_string"`
		ReplaceAll bool    `json:"replace_all"`
	}
	if json.Unmarshal(args, &a) != nil {
		return nil
	}
	d := map[string]any{"type": "diff", "path": b.abs(p.Path)}
	old, hasOld := "", false
	if p.HashBefore != "" {
		raw, ok := b.s.OldText(p.HashBefore)
		if !ok {
			return nil
		}
		old, hasOld = b.s.Redact(raw), true
		d["oldText"] = old
	}
	switch p.Tool {
	case "write_file":
		if a.Content == nil {
			return nil
		}
		d["newText"] = *a.Content
	case "edit_file":
		n := 0
		if hasOld && a.OldString != "" {
			n = strings.Count(old, a.OldString)
		}
		if n == 0 || (n > 1 && !a.ReplaceAll) {
			return nil
		}
		d["newText"] = strings.ReplaceAll(old, a.OldString, a.NewString)
	default:
		return nil
	}
	return d
}

func (b *bridge) abs(path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.root, path)
	}
	return filepath.Clean(path)
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

type permOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// options renvoie les options proposées pour la demande p.
func (b *bridge) options(p event.PermissionRequestedPayload) []permOption {
	allow := i18n.T(b.lang, "acp.perm.allow")
	if p.Kind != "tool" {
		return []permOption{
			{optAllow, allow, "allow_once"},
			{optReject, i18n.T(b.lang, "acp.perm.reject"), "reject_once"},
		}
	}
	if p.RuleNetwork {
		allow = i18n.T(b.lang, "acp.perm.allow_rule_network")
	}
	opts := []permOption{{optAllow, allow, "allow_once"}}
	if p.Sandboxed && !p.RuleNetwork {
		opts = append(opts, permOption{optAllowNetwork, i18n.T(b.lang, "perm.allow_network"), "allow_once"})
	}
	// Une demande d'un hook n'est jamais mémorisée (comme dans la TUI).
	if p.HookReason == "" {
		opts = append(opts, permOption{optAllowSession, i18n.T(b.lang, "acp.perm.allow_session"), "allow_always"})
	}
	return append(opts, permOption{optReject, i18n.T(b.lang, "acp.perm.reject"), "reject_once"})
}

// replyFor traduit l'option choisie ; une option non proposée est un refus.
func replyFor(id string, opts []permOption) engine.Reply {
	offered := false
	for _, o := range opts {
		offered = offered || o.OptionID == id
	}
	if !offered {
		return engine.Reply{}
	}
	switch id {
	case optAllow:
		return engine.Reply{Allow: true}
	case optAllowSession:
		// Jamais le réseau : seule l'option « avec réseau » l'accorde.
		return engine.Reply{Allow: true, Remember: true}
	case optAllowNetwork:
		return engine.Reply{Allow: true, Network: true}
	}
	return engine.Reply{}
}

// permTitle décrit la demande p sur une ligne.
func (b *bridge) permTitle(p event.PermissionRequestedPayload) string {
	if p.Kind == "jurisdiction" && p.Provider != nil {
		return i18n.T(b.lang, "acp.perm.jurisdiction", headless.OneLine(headless.ProviderLabel(*p.Provider)),
			headless.JurisdictionLabel(b.lang, p.Provider.Jurisdiction))
	}
	s := headless.OneLine(p.Tool)
	target := p.Command
	if target == "" {
		target = strings.Join(p.Paths, ", ")
	}
	if target = clip(headless.OneLine(target), maxTitle); target != "" {
		s += " · " + target
	}
	if p.HookReason != "" {
		s += " (" + i18n.T(b.lang, "tui.perm.hook", clip(headless.OneLine(p.HookReason), maxTitle)) + ")"
	}
	return s
}

// ask pose la demande p à l'éditeur dans sa goroutine (la pompe continue) et
// transmet la réponse au moteur. Refus : option reject, réponse cancelled,
// erreur du client, session/cancel ou fermeture.
func (b *bridge) ask(p event.PermissionRequestedPayload) {
	id := p.PermissionID
	callID := id
	if p.Kind == "tool" {
		// Les appels d'un même agent sont séquentiels : la demande porte sur
		// son appel en cours, s'il s'agit du même outil.
		if o, ok := b.open[p.ParentCallID]; ok && o.tool == p.Tool {
			callID = toolCallID(p.ParentCallID, o.id)
			b.permCall[id] = callID
		}
	}
	opts := b.options(p)
	tc := map[string]any{"toolCallId": callID, "title": b.permTitle(p)}
	if p.Kind == "tool" {
		tc["kind"] = toolKind(p.Tool)
	}
	if len(p.Args) > 0 && json.Valid(p.Args) {
		tc["rawInput"] = p.Args
	}
	params := map[string]any{"sessionId": b.sid, "toolCall": tc, "options": opts}

	b.mu.Lock()
	if b.cancelled {
		b.mu.Unlock()
		_ = b.s.Respond(id, engine.Reply{})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.perms[id] = cancel
	b.mu.Unlock()

	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.perms, id)
			b.mu.Unlock()
			cancel()
		}()
		var res struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		r := engine.Reply{}
		if err := b.conn.Call(ctx, "session/request_permission", params, &res); err == nil && res.Outcome.Outcome == "selected" {
			r = replyFor(res.Outcome.OptionID, opts)
		}
		// Respond n'échoue que si la demande est déjà résolue (tour annulé).
		_ = b.s.Respond(id, r)
	}()
}
