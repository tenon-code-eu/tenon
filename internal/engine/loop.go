package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// reMarker reconnaît un marqueur de masquage (voir policy.Redactor).
var reMarker = regexp.MustCompile(`\[[A-Z_]+_\d+\]`)

// turn porte l'état d'un tour ; s.turn est tenu pendant toute sa durée.
// Un sous-agent utilise un second turn : historique local, outils restreints,
// parent = identifiant de l'appel d'outil agent, sans deltas.
type turn struct {
	e      *Engine
	s      *session
	ctx    context.Context
	h      *[]llm.Message  // historique du tour (session : &s.history)
	tools  *tools.Registry // outils proposés et exécutables (peut être nil)
	parent string          // call_id de l'appel agent parent ("" pour le tour principal)
	deltas bool            // émettre AssistantDelta
	// noHooks : tour d'un hook agent, dont les appels ne déclenchent aucun hook.
	noHooks bool
	// stopReason : non vide après un hook continue:false ; le tour s'arrête
	// après l'outil en cours (ou au prochain point de contrôle de la boucle)
	// avec cette raison.
	stopReason string
	// agent : nom de l'agent d'un sous-agent (entrée de SubagentStop).
	agent string
	// outer : contexte du tour qui exécute le hook, pour la requête d'un hook
	// prompt ou le tour d'un hook agent (ctx porte alors le délai du hook) ;
	// nil sinon.
	outer context.Context
}

// loopResult résume la fin de loop. Reason vaut l'une des raisons de
// TurnCompleted, ou "error" quand ProvErr n'est pas nil.
type loopResult struct {
	Iterations int
	Reason     string
	Text       string // dernier texte d'assistant non vide (même incomplet)
	Final      string // texte du dernier message d'assistant (raison done)
	ProvErr    error
}

func (t *turn) emit(typ event.Type, payload any) error {
	return t.e.emit(t.s, typ, event.WithParent(payload, t.parent))
}

func (t *turn) done(iter int, reason string) error {
	return t.emit(event.TurnCompleted, event.TurnCompletedPayload{Iterations: iter, Reason: reason})
}

func (t *turn) run(in Input) error {
	d := t.e.d
	info := d.Provider.Info()
	if !t.s.started {
		if err := t.emit(event.SessionStarted, event.SessionStartedPayload{
			Provider: info, ProjectRoot: d.ProjectRoot, Resumed: t.s.resumed, Instructions: d.Instructions,
			MCP: d.MCP,
		}); err != nil {
			return err
		}
		t.s.started = true
		for _, u := range d.UnavailableMCP {
			if err := t.emit(event.McpServerDown, u); err != nil {
				return err
			}
		}
		source := "startup"
		if t.s.resumed {
			source = "resume"
		}
		out, err := t.runHooks(hooks.SessionStart, "", map[string]any{"source": source}, source)
		if err != nil {
			return t.hookFailed(err)
		}
		// Un contexte restauré (prompt bloqué avant la reprise) reste en tête.
		t.s.startCtx = joinContext(t.s.startCtx, out.Context)
	}
	prompt := in.Typed
	if prompt == "" {
		prompt = in.Text
	}
	out, err := t.runHooks(hooks.UserPromptSubmit, "", map[string]any{"prompt": prompt})
	if err != nil {
		return t.hookFailed(err)
	}
	um := event.UserMessagePayload{Text: in.Text, Typed: in.Typed, Skill: in.Skill, SkillSHA256: in.SkillSHA256}
	// Prompt bloqué : ni historique, ni requête ; le message est journalisé
	// comme bloqué, avec le contexte SessionStart encore en attente (pour la
	// reprise).
	if out.Block {
		um.Blocked = true
		um.HookContext = t.s.startCtx
		if err := t.emit(event.UserMessage, um); err != nil {
			return err
		}
		return t.done(0, "blocked_by_hook")
	}
	um.HookContext = joinContext(t.s.startCtx, out.Context)
	t.s.startCtx = ""
	*t.h = append(*t.h, llm.Message{Role: llm.User, Content: withHookContext(in.Text, um.HookContext)})
	if err := t.emit(event.UserMessage, um); err != nil {
		return err
	}
	// continue:false de SessionStart ou UserPromptSubmit : fin avant toute requête.
	if t.stopReason != "" {
		return t.done(0, t.stopReason)
	}

	res, err := t.loop()
	if res.Reason == "cancelled" {
		t.cancelled(res.Iterations) // au mieux
		return err
	}
	if err != nil {
		return err
	}
	if res.ProvErr != nil {
		return res.ProvErr
	}
	return t.done(res.Iterations, res.Reason)
}

// hookFailed traite l'erreur d'un hook de run avant la boucle : après une
// annulation, le tour est clos (au mieux) avec la raison cancelled.
func (t *turn) hookFailed(err error) error {
	if t.ctx.Err() != nil {
		t.cancelled(0)
	}
	return err
}

// loop exécute les itérations du tour sur *t.h. Il n'émet pas TurnCompleted :
// l'appelant le fait à partir du résultat. Une erreur du fournisseur est
// signalée par un événement Error puis par ProvErr ; l'error renvoyée est
// fatale (audit, annulation du contexte).
func (t *turn) loop() (loopResult, error) {
	d := t.e.d
	info := d.Provider.Info()
	var last, final string
	res := func(iter int, reason string) loopResult {
		return loopResult{Iterations: iter, Reason: reason, Text: last, Final: final}
	}
	// stopActive : le tour a déjà été relancé par un hook Stop/SubagentStop.
	stopActive := false
	for iter := 1; iter <= d.MaxIterations; iter++ {
		var defs []llm.ToolDef
		if t.tools != nil {
			defs = t.tools.Defs()
		}
		if stop, reason, err := t.compactOrStop(info, defs, false, iter-1); stop || err != nil {
			return res(iter-1, reason), err
		}
		// continue:false d'un hook de compaction (PreCompact, SessionStart).
		if t.stopReason != "" {
			return res(iter-1, t.stopReason), nil
		}

		var (
			text      string
			calls     []llm.ToolCall
			provErr   error
			compacted bool
		)
		for {
			msgs, ok, err := t.outbound(info, *t.h)
			if err != nil {
				return res(iter, ""), err
			}
			if !ok {
				return res(iter-1, "blocked"), nil
			}
			// continue:false d'un hook Notification (demande de juridiction).
			if t.stopReason != "" {
				return res(iter-1, t.stopReason), nil
			}
			text, calls, provErr, err = t.stream(llm.Request{Model: info.Model, Messages: msgs, Tools: defs}, t.deltas)
			if err != nil {
				return res(iter, ""), err
			}
			// Contexte trop long pour le fournisseur : une compaction forcée,
			// puis une seule nouvelle tentative.
			if compacted || !errors.Is(provErr, provider.ErrContextLength) || t.ctx.Err() != nil {
				break
			}
			compacted = true
			if stop, reason, err := t.compactOrStop(info, defs, true, iter); stop || err != nil {
				return res(iter, reason), err
			}
			if t.stopReason != "" {
				return res(iter, t.stopReason), nil
			}
		}
		if provErr != nil {
			if cerr := t.ctx.Err(); cerr != nil {
				return res(iter, "cancelled"), cerr
			}
			if errors.Is(provErr, provider.ErrIncomplete) {
				if text != "" {
					*t.h = append(*t.h, llm.Message{Role: llm.Assistant, Content: text})
					last = text
				}
				if err := t.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: text, Incomplete: true}); err != nil {
					return res(iter, ""), err
				}
				return res(iter, "incomplete"), nil
			}
			if err := t.emit(event.Error, event.ErrorPayload{Code: "provider", Message: provErr.Error()}); err != nil {
				return res(iter, ""), err
			}
			r := res(iter, "error")
			r.ProvErr = provErr
			return r, nil
		}

		*t.h = append(*t.h, llm.Message{Role: llm.Assistant, Content: text, ToolCalls: calls})
		if text != "" {
			last = text
		}
		if err := t.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: text, ToolCalls: safeCalls(calls)}); err != nil {
			t.closeCalls(calls, err)
			return res(iter, ""), err
		}
		if len(calls) == 0 {
			final = text
			// Annulation arrivée avec la fin du flux : pas de hook Stop.
			if cerr := t.ctx.Err(); cerr != nil {
				return res(iter, "cancelled"), cerr
			}
			// Stop / SubagentStop : seulement pour une fin done. Un blocage
			// relance la boucle (l'itération compte pour MaxIterations).
			again, err := t.stopHook(stopActive)
			if err != nil {
				if t.ctx.Err() != nil {
					return res(iter, "cancelled"), err
				}
				return res(iter, ""), err
			}
			if t.stopReason != "" {
				return res(iter, t.stopReason), nil
			}
			if again {
				stopActive = true
				continue
			}
			return res(iter, "done"), nil
		}
		for i, c := range calls {
			tr, err := t.runTool(c)
			if err != nil {
				t.closeCalls(calls[i:], err)
				if t.ctx.Err() != nil {
					return res(iter, "cancelled"), err
				}
				return res(iter, ""), err
			}
			*t.h = append(*t.h, llm.Message{Role: llm.Tool, Content: tr.Content, ToolCallID: c.ID})
			if t.stopReason != "" {
				t.closeCalls(calls[i+1:], errors.New("stopped by hook"))
				return res(iter, t.stopReason), nil
			}
		}
	}
	return res(d.MaxIterations, "max_iterations"), nil
}

// closeCalls ajoute à l'historique une réponse synthétique pour chaque appel
// d'outil resté sans résultat : sans elle, la requête suivante de la session
// contiendrait des tool_calls sans réponse, refusés par les API compatibles
// OpenAI (HTTP 400).
func (t *turn) closeCalls(calls []llm.ToolCall, cause error) {
	for _, c := range calls {
		*t.h = append(*t.h, llm.Message{
			Role: llm.Tool, Content: "cancelled: " + cause.Error(), ToolCallID: c.ID,
		})
	}
}

// cancelled clôt le tour dans le journal après une annulation du contexte.
// Au mieux : une erreur d'émission (audit déjà en échec) est ignorée.
func (t *turn) cancelled(iter int) {
	_ = t.done(iter, "cancelled")
}

// compactOrStop appelle compact. stop indique que la compaction termine le
// tour, avec reason ("cancelled", "blocked", "context_too_large"...) ; err est
// l'erreur à renvoyer. Le tour est clos par l'appelant, avec iter itérations.
func (t *turn) compactOrStop(info llm.ProviderInfo, defs []llm.ToolDef, force bool, iter int) (stop bool, reason string, err error) {
	reason, err = t.compact(info, defs, force)
	switch {
	case reason == reasonCancelled:
		return true, "cancelled", err
	case err != nil:
		return true, "", err
	case reason != "":
		return true, reason, nil
	}
	return false, "", nil
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
	// Notification : à l'ouverture du panneau de validation. Un continue:false
	// arrête le tour au prochain point de contrôle, après la réponse.
	if err := t.notifyPermission(p); err != nil {
		forget()
		return Reply{}, err
	}
	var r Reply
	select {
	case r = <-ch:
	case <-t.ctx.Done():
		forget()
		// Seul le délai du hook a expiré (le tour continue) : la demande est
		// résolue en refus, sans quoi l'interface la garderait affichée.
		// Au mieux : l'erreur de ctx est renvoyée de toute façon.
		if t.outer != nil && t.outer.Err() == nil {
			_ = t.emit(event.PermissionResolved, event.PermissionResolvedPayload{PermissionID: id})
		}
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
	res, denied, ran, args, err := t.decideAndRun(c)
	if err != nil {
		return tools.Result{}, err
	}
	if denied == "" {
		denied = res.Denied // refus décidé par l'outil lui-même
	}
	// PostToolUse : seulement après une exécution réelle, jamais après un
	// refus ; son entrée porte les arguments exécutés (updatedInput compris).
	if ran && res.Denied == "" {
		executed := c
		executed.Args = args
		fields := toolFields(executed)
		fields["tool_response"] = map[string]any{"content": res.Content, "is_error": res.IsError}
		out, err := t.runHooks(hooks.PostToolUse, c.ID, fields, hooks.ClaudeName(c.Name), c.Name)
		if err != nil {
			return tools.Result{}, err
		}
		if out.Block {
			res.Content += "\n\nhook feedback: " + out.Reason
		}
		if out.Context != "" {
			res.Content += "\n\n" + out.Context
		}
	}
	if err := t.emit(event.ToolResult, event.ToolResultPayload{
		CallID: c.ID, Tool: c.Name, Content: res.Content, IsError: res.IsError,
		Path: res.Path, HashBefore: res.HashBefore, HashAfter: res.HashAfter,
		Denied: denied,
	}); err != nil {
		return tools.Result{}, err
	}
	if res.ServerDown != "" {
		if err := t.emit(event.McpServerDown, event.McpServerDownPayload{
			Server: res.ServerDown, Error: res.Content,
		}); err != nil {
			return tools.Result{}, err
		}
	}
	return res, nil
}

// Valeurs de ToolResultPayload.Denied (deniedHook : hooks.go).
const (
	deniedPolicy = "policy"
	deniedUser   = "user"
)

// checkArgs applique les garde-fous de marqueurs puis Target. ok vaut false
// si l'appel est refusé : res est alors le résultat d'erreur et denied son
// origine (deniedPolicy pour un marqueur, "" pour des arguments invalides).
func checkArgs(tool tools.Tool, name string, args json.RawMessage) (target tools.Target, res tools.Result, denied string, ok bool) {
	// Les marqueurs de masquage ne sont jamais restaurés : les écrire
	// remplacerait la vraie valeur par le marqueur.
	if tool.Risk() == tools.Write {
		if m := findMarker(args); m != "" {
			return target, toolError("redacted content: cannot write a redaction marker (%s)", m), deniedPolicy, false
		}
	}
	// De même pour un outil MCP, quel que soit son risque déclaré : le
	// serveur recevrait le marqueur à la place de la valeur.
	if strings.HasPrefix(name, "mcp__") {
		if m := findMarker(args); m != "" {
			return target, toolError("redacted content: cannot send a redaction marker to an MCP server (%s)", m), deniedPolicy, false
		}
	}
	target, err := tool.Target(args)
	if err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "invalid arguments") {
			msg = "invalid arguments: " + msg
		}
		return target, tools.Result{IsError: true, Content: msg}, "", false
	}
	return target, tools.Result{}, "", true
}

// decideAndRun renvoie aussi l'origine d'un refus (deniedPolicy, deniedUser,
// deniedHook, "" si l'appel n'a pas été refusé), ran, vrai si l'outil a été
// réellement exécuté, et args, les arguments exécutés (updatedInput compris).
func (t *turn) decideAndRun(c llm.ToolCall) (res tools.Result, denied string, ran bool, args json.RawMessage, err error) {
	d := t.e.d
	var tool tools.Tool
	ok := false
	if t.tools != nil {
		tool, ok = t.tools.Get(c.Name)
	}
	if !ok {
		return toolError("unknown tool: %s", c.Name), "", false, nil, nil
	}
	target, bad, denied, ok := checkArgs(tool, c.Name, c.Args)
	if !ok {
		return bad, denied, false, nil, nil
	}

	// PreToolUse : après la validation, avant les permissions.
	out, err := t.runHooks(hooks.PreToolUse, c.ID, toolFields(c), hooks.ClaudeName(c.Name), c.Name)
	if err != nil {
		return tools.Result{}, "", false, nil, err
	}
	switch {
	case out.Stop:
		return toolError("stopped by hook: %s", out.StopReason), deniedHook, false, nil, nil
	case out.Conflict:
		return toolError("conflicting updatedInput from hooks"), deniedHook, false, nil, nil
	case out.Block:
		return toolError("blocked by hook: %s", out.Reason), deniedHook, false, nil, nil
	}
	args = c.Args
	if out.UpdatedInput != nil {
		// Nouvelle entrée : retraduite, revalidée ; c'est elle qui est
		// vérifiée par les permissions puis exécutée.
		args, err = hooks.FromClaude(c.Name, out.UpdatedInput)
		if err != nil {
			return toolError("invalid updatedInput from hook: %s", err), deniedHook, false, nil, nil
		}
		if target, bad, _, ok = checkArgs(tool, c.Name, args); !ok {
			return toolError("invalid updatedInput from hook: %s", bad.Content), deniedHook, false, nil, nil
		}
	}

	// Un allow de hook vaut une règle allow globale : il ne lève qu'un Ask
	// levable (règle ask globale ou défaut), jamais un Deny. Un ask de hook
	// impose la demande, même si les règles autorisent.
	dec, liftable := d.Permissions.Decide(policy.Call{Tool: c.Name, Risk: tool.Risk(), Target: target})
	var prompt bool
	switch dec {
	case policy.Allow:
		prompt = out.Ask
	case policy.Ask:
		prompt = !(out.Allow && liftable && !out.Ask)
	default:
		return toolError("action denied by policy"), deniedPolicy, false, nil, nil
	}
	if prompt {
		key := c.Name + "\x00" + target.Command + "\x00" + strings.Join(target.Paths, "\x00")
		// Une approbation mémorisée ne dispense jamais d'une demande de hook,
		// et la réponse à celle-ci n'est pas mémorisée.
		if out.Ask || !t.s.remembered[key] {
			p := event.PermissionRequestedPayload{
				Kind: "tool", Tool: c.Name, Paths: target.Paths, Command: target.Command,
			}
			if out.Ask {
				p.HookReason = out.Reason
				if p.HookReason == "" {
					p.HookReason = noHookReason
				}
			}
			if out.UpdatedInput != nil && !sameJSON(args, c.Args) {
				p.Args = safeArgs(args)
			}
			r, err := t.ask(p)
			if err != nil {
				return tools.Result{}, "", false, nil, err
			}
			if !r.Allow {
				return toolError("action denied by the user"), deniedUser, false, nil, nil
			}
			if r.Remember && !out.Ask {
				t.s.remembered[key] = true
			}
		}
	}
	if _, isAgent := tool.(agentTool); isAgent {
		run := c
		run.Args = args
		res, err = t.runSubagent(run)
	} else {
		res, err = tool.Run(t.ctx, args)
	}
	if err != nil {
		return tools.Result{}, "", false, nil, err
	}
	return res, "", true, args, nil
}

// noHookReason remplace la raison absente d'un ask de hook : la demande reste
// marquée comme venant d'un hook (pas de « oui pour la session »).
const noHookReason = "(no reason given)"

// sameJSON indique que a et b sont deux JSON valides de même valeur.
func sameJSON(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
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
