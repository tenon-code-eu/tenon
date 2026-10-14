package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/policy"
	"codeberg.org/tenon/tenon/internal/provider"
	"codeberg.org/tenon/tenon/internal/tools"
)

// reMarker reconnaît un marqueur de masquage (voir policy.Redactor).
var reMarker = regexp.MustCompile(`\[[A-Z_]+_\d+\]`)

// turn porte l'état d'un tour ; s.turn est tenu pendant toute sa durée.
type turn struct {
	e   *Engine
	s   *session
	ctx context.Context
}

func (t *turn) emit(typ event.Type, payload any) error { return t.e.emit(t.s, typ, payload) }

func (t *turn) done(iter int, reason string) error {
	return t.emit(event.TurnCompleted, event.TurnCompletedPayload{Iterations: iter, Reason: reason})
}

func (t *turn) run(input string) error {
	d := t.e.d
	info := d.Provider.Info()
	if !t.s.started {
		if err := t.emit(event.SessionStarted, event.SessionStartedPayload{
			Provider: info, ProjectRoot: d.ProjectRoot, Resumed: t.s.resumed, Instructions: d.Instructions,
		}); err != nil {
			return err
		}
		t.s.started = true
	}
	t.s.history = append(t.s.history, llm.Message{Role: llm.User, Content: input})
	if err := t.emit(event.UserMessage, event.UserMessagePayload{Text: input}); err != nil {
		return err
	}

	for iter := 1; iter <= d.MaxIterations; iter++ {
		var defs []llm.ToolDef
		if d.Tools != nil {
			defs = d.Tools.Defs()
		}
		if stop, err := t.compactOrStop(info, defs, false, iter-1); stop || err != nil {
			return err
		}

		var (
			text      string
			calls     []llm.ToolCall
			provErr   error
			compacted bool
		)
		for {
			msgs, ok, err := t.outbound(info, t.s.history)
			if err != nil {
				return err
			}
			if !ok {
				return t.done(iter-1, "blocked")
			}
			text, calls, provErr, err = t.stream(llm.Request{Model: info.Model, Messages: msgs, Tools: defs}, true)
			if err != nil {
				return err
			}
			// Contexte trop long pour le fournisseur : une compaction forcée,
			// puis une seule nouvelle tentative.
			if compacted || !errors.Is(provErr, provider.ErrContextLength) || t.ctx.Err() != nil {
				break
			}
			compacted = true
			if stop, err := t.compactOrStop(info, defs, true, iter); stop || err != nil {
				return err
			}
		}
		if provErr != nil {
			if cerr := t.ctx.Err(); cerr != nil {
				t.cancelled(iter)
				return cerr
			}
			if errors.Is(provErr, provider.ErrIncomplete) {
				if text != "" {
					t.s.history = append(t.s.history, llm.Message{Role: llm.Assistant, Content: text})
				}
				if err := t.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: text, Incomplete: true}); err != nil {
					return err
				}
				return t.done(iter, "incomplete")
			}
			if err := t.emit(event.Error, event.ErrorPayload{Code: "provider", Message: provErr.Error()}); err != nil {
				return err
			}
			return provErr
		}

		t.s.history = append(t.s.history, llm.Message{Role: llm.Assistant, Content: text, ToolCalls: calls})
		if err := t.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: text, ToolCalls: safeCalls(calls)}); err != nil {
			t.closeCalls(calls, err)
			return err
		}
		if len(calls) == 0 {
			return t.done(iter, "done")
		}
		for i, c := range calls {
			res, err := t.runTool(c)
			if err != nil {
				t.closeCalls(calls[i:], err)
				if t.ctx.Err() != nil {
					t.cancelled(iter)
				}
				return err
			}
			t.s.history = append(t.s.history, llm.Message{Role: llm.Tool, Content: res.Content, ToolCallID: c.ID})
		}
	}
	return t.done(d.MaxIterations, "max_iterations")
}

// closeCalls ajoute à l'historique une réponse synthétique pour chaque appel
// d'outil resté sans résultat : sans elle, la requête suivante de la session
// contiendrait des tool_calls sans réponse, refusés par les API compatibles
// OpenAI (HTTP 400).
func (t *turn) closeCalls(calls []llm.ToolCall, cause error) {
	for _, c := range calls {
		t.s.history = append(t.s.history, llm.Message{
			Role: llm.Tool, Content: "annulé : " + cause.Error(), ToolCallID: c.ID,
		})
	}
}

// cancelled clôt le tour dans le journal après une annulation du contexte.
// Au mieux : une erreur d'émission (audit déjà en échec) est ignorée.
func (t *turn) cancelled(iter int) {
	_ = t.done(iter, "cancelled")
}

// compactOrStop appelle compact et, si la compaction termine le tour, émet
// TurnCompleted avec iter itérations. stop indique la fin du tour ; err est
// l'erreur à renvoyer par Submit.
func (t *turn) compactOrStop(info llm.ProviderInfo, defs []llm.ToolDef, force bool, iter int) (stop bool, err error) {
	reason, err := t.compact(info, defs, force)
	switch {
	case reason == reasonCancelled:
		t.cancelled(iter)
		return true, err
	case err != nil:
		return true, err
	case reason != "":
		return true, t.done(iter, reason)
	}
	return false, nil
}

// outbound applique le contrôle sortant à in (historique ou requête de
// résumé) et renvoie la copie masquée à envoyer. ok vaut false si
// l'utilisateur refuse la juridiction.
func (t *turn) outbound(info llm.ProviderInfo, in []llm.Message) (msgs []llm.Message, ok bool, err error) {
	d := t.e.d
	var counts map[string]int
	for asked := false; ; asked = true {
		msgs, counts, err = d.Outbound.Check(info, in)
		if err == nil {
			break
		}
		if !errors.Is(err, policy.ErrJurisdiction) {
			if eerr := t.emit(event.Error, event.ErrorPayload{Code: "outbound", Message: err.Error()}); eerr != nil {
				return nil, false, eerr
			}
			return nil, false, err
		}
		if asked {
			return nil, false, nil
		}
		p := info
		r, err := t.ask(event.PermissionRequestedPayload{Kind: "jurisdiction", Provider: &p})
		if err != nil {
			return nil, false, err
		}
		if !r.Allow {
			return nil, false, nil
		}
		d.Outbound.Consent(info.ID)
	}
	if !t.s.warned && info.Jurisdiction != llm.EU && info.Jurisdiction != llm.Local {
		if err := t.emit(event.JurisdictionWarning, event.JurisdictionWarningPayload{Provider: info}); err != nil {
			return nil, false, err
		}
		t.s.warned = true
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total > 0 {
		if err := t.emit(event.RedactionApplied, event.RedactionAppliedPayload{Counts: counts}); err != nil {
			return nil, false, err
		}
	}
	return msgs, true, nil
}

// stream appelle le fournisseur et draine le flux jusqu'à sa fermeture.
// provErr est l'erreur du fournisseur ; err une erreur d'émission (fatale).
// deltas indique s'il faut émettre AssistantDelta (faux pour un résumé).
func (t *turn) stream(req llm.Request, deltas bool) (text string, calls []llm.ToolCall, provErr, err error) {
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	ch, perr := t.e.d.Provider.Stream(ctx, req)
	if perr != nil {
		return "", nil, perr, nil
	}
	var sb strings.Builder
	final := false
	for c := range ch {
		if c.Text != "" {
			sb.WriteString(c.Text)
			if err := t.delta(deltas, c.Text); err != nil {
				// Annule le flux puis le draine en arrière-plan pour libérer
				// le goroutine du fournisseur.
				cancel()
				go func() {
					for range ch {
					}
				}()
				return "", nil, nil, err
			}
		}
		if c.Done {
			final = true
			provErr = c.Err
			calls = c.ToolCalls
		}
	}
	if !final && provErr == nil {
		provErr = provider.ErrIncomplete
	}
	if provErr != nil {
		calls = nil
	}
	return sb.String(), calls, provErr, nil
}

// delta émet AssistantDelta si emit est vrai (faux pour un résumé).
func (t *turn) delta(emit bool, text string) error {
	if !emit {
		return nil
	}
	return t.emit(event.AssistantDelta, event.AssistantDeltaPayload{Text: text})
}

// ask enregistre la demande, émet PermissionRequested, attend Respond ou
// l'annulation du contexte, puis émet PermissionResolved.
func (t *turn) ask(p event.PermissionRequestedPayload) (Reply, error) {
	t.s.permN++
	id := fmt.Sprintf("%s-p%d", t.s.id, t.s.permN)
	p.PermissionID = id
	ch := make(chan Reply, 1)
	e := t.e
	e.mu.Lock()
	e.pending[id] = ch
	e.mu.Unlock()
	forget := func() {
		e.mu.Lock()
		delete(e.pending, id)
		e.mu.Unlock()
	}
	if err := t.emit(event.PermissionRequested, p); err != nil {
		forget()
		return Reply{}, err
	}
	var r Reply
	select {
	case r = <-ch:
	case <-t.ctx.Done():
		forget()
		return Reply{}, t.ctx.Err()
	}
	if err := t.emit(event.PermissionResolved, event.PermissionResolvedPayload{
		PermissionID: id, Allowed: r.Allow, Remember: r.Remember,
	}); err != nil {
		return Reply{}, err
	}
	return r, nil
}

func toolError(format string, a ...any) tools.Result {
	return tools.Result{IsError: true, Content: fmt.Sprintf(format, a...)}
}

// runTool traite un appel d'outil : audit, garde, permissions, exécution,
// résultat. Une error renvoyée met fin au tour (audit, annulation).
func (t *turn) runTool(c llm.ToolCall) (tools.Result, error) {
	if err := t.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{
		CallID: c.ID, Tool: c.Name, Args: safeArgs(c.Args),
	}); err != nil {
		return tools.Result{}, err
	}
	res, denied, err := t.decideAndRun(c)
	if err != nil {
		return tools.Result{}, err
	}
	if err := t.emit(event.ToolResult, event.ToolResultPayload{
		CallID: c.ID, Tool: c.Name, Content: res.Content, IsError: res.IsError,
		Path: res.Path, HashBefore: res.HashBefore, HashAfter: res.HashAfter,
		Denied: denied,
	}); err != nil {
		return tools.Result{}, err
	}
	return res, nil
}

// Valeurs de ToolResultPayload.Denied.
const (
	deniedPolicy = "policy"
	deniedUser   = "user"
)

// decideAndRun renvoie aussi l'origine d'un refus (deniedPolicy, deniedUser)
// ou "" si l'appel n'a pas été refusé.
func (t *turn) decideAndRun(c llm.ToolCall) (tools.Result, string, error) {
	d := t.e.d
	var tool tools.Tool
	ok := false
	if d.Tools != nil {
		tool, ok = d.Tools.Get(c.Name)
	}
	if !ok {
		return toolError("outil inconnu : %s", c.Name), "", nil
	}
	// Les marqueurs de masquage ne sont jamais restaurés : les écrire
	// remplacerait la vraie valeur par le marqueur.
	if tool.Risk() == tools.Write {
		if m := findMarker(c.Args); m != "" {
			return toolError("contenu masqué : impossible d'écrire un marqueur de masquage (%s)", m), deniedPolicy, nil
		}
	}
	target, err := tool.Target(c.Args)
	if err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "arguments invalides") {
			msg = "arguments invalides : " + msg
		}
		return tools.Result{IsError: true, Content: msg}, "", nil
	}
	switch d.Permissions.Check(policy.Call{Tool: c.Name, Risk: tool.Risk(), Target: target}) {
	case policy.Allow:
	case policy.Ask:
		key := c.Name + "\x00" + target.Command + "\x00" + strings.Join(target.Paths, "\x00")
		if !t.s.remembered[key] {
			r, err := t.ask(event.PermissionRequestedPayload{
				Kind: "tool", Tool: c.Name, Paths: target.Paths, Command: target.Command,
			})
			if err != nil {
				return tools.Result{}, "", err
			}
			if !r.Allow {
				return toolError("action refusée par l'utilisateur"), deniedUser, nil
			}
			if r.Remember {
				t.s.remembered[key] = true
			}
		}
	default:
		return toolError("action refusée par la politique"), deniedPolicy, nil
	}
	res, err := tool.Run(t.ctx, c.Args)
	if err != nil {
		return tools.Result{}, "", err
	}
	return res, "", nil
}

// safeArgs garantit un JSON valide pour les payloads d'événements : des
// arguments invalides sont conservés sous forme de chaîne JSON.
func safeArgs(a json.RawMessage) json.RawMessage {
	if a == nil || json.Valid(a) {
		return a
	}
	b, _ := json.Marshal(string(a))
	return b
}

func safeCalls(cs []llm.ToolCall) []llm.ToolCall {
	if cs == nil {
		return nil
	}
	out := make([]llm.ToolCall, len(cs))
	for i, c := range cs {
		c.Args = safeArgs(c.Args)
		out[i] = c
	}
	return out
}

// findMarker cherche un marqueur de masquage dans les arguments bruts, puis
// dans leurs chaînes décodées (un marqueur peut être écrit avec des
// échappements JSON comme \u005b).
func findMarker(args json.RawMessage) string {
	if m := reMarker.FindString(string(args)); m != "" {
		return m
	}
	var v any
	if json.Unmarshal(args, &v) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case string:
			return reMarker.FindString(x)
		case []any:
			for _, e := range x {
				if m := walk(e); m != "" {
					return m
				}
			}
		case map[string]any:
			for k, e := range x {
				if m := reMarker.FindString(k); m != "" {
					return m
				}
				if m := walk(e); m != "" {
					return m
				}
			}
		}
		return ""
	}
	return walk(v)
}
