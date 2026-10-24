package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

const (
	// deniedHook : valeur de ToolResultPayload.Denied pour un refus de hook.
	deniedHook = "hook"
	// stoppedByHook : raison de fin de tour après un hook continue:false.
	stoppedByHook = "stopped_by_hook"
	// auditMax borne command, prompt, stdout et stderr dans les événements de hook.
	auditMax = 4096
)

// hookTools : outils d'un hook agent, en lecture seule.
var hookTools = []string{"read_file", "grep", "glob"}

// runHooks exécute les hooks de ev qui correspondent à subjects : HookStarted
// pour chacun (dans l'ordre, avant l'exécution), hooks.Run, puis HookCompleted
// pour chaque résultat. Sans effet pour le tour d'un hook agent (noHooks) ou
// quand aucun hook ne correspond. Un continue:false arrête le tour (stopReason).
// L'error renvoyée est fatale (audit, annulation du contexte).
func (t *turn) runHooks(ev hooks.Event, callID string, fields map[string]any, subjects ...string) (hooks.Outcome, error) {
	if t.noHooks {
		return hooks.Outcome{}, nil
	}
	d := t.e.d
	hs := d.Hooks.Match(ev, subjects...)
	if len(hs) == 0 {
		return hooks.Outcome{}, nil
	}
	runIDs := make(map[string]string, len(hs))
	for _, h := range hs {
		p := event.HookStartedPayload{HookID: h.ID, Event: string(ev), Type: string(h.Type), CallID: callID}
		if h.Type == hooks.Command {
			p.Command = cutBytes(h.Command, auditMax)
		} else {
			p.Prompt = cutBytes(h.Prompt, auditMax)
		}
		// RunID est l'identifiant de cet événement HookStarted, fixé à l'émission.
		if err := t.e.emitBuild(t.s, event.HookStarted, func(id string) any {
			p.RunID = id
			return event.WithParent(p, t.parent)
		}); err != nil {
			return hooks.Outcome{}, err
		}
		runIDs[h.ID] = p.RunID
	}
	env := hooks.Env{SessionID: t.s.id, TranscriptPath: d.TranscriptPath, ProjectRoot: d.ProjectRoot}
	out := hooks.Run(t.ctx, hs, ev, fields, env, hookEval{t: t, runIDs: runIDs, sem: make(chan struct{}, 1)})
	for _, r := range out.Results {
		p := event.HookCompletedPayload{
			RunID: runIDs[r.Hook.ID], HookID: r.Hook.ID, Event: string(ev), Status: string(r.Status),
			Decision: r.Decision, Reason: r.Reason, UpdatedInput: safeArgs(r.UpdatedInput),
			SystemMessage: r.SystemMessage, Stop: r.Stop, StopReason: r.StopReason, Error: r.Err,
			DurationMS: r.Duration.Milliseconds(),
			Stdout:     cutBytes(r.Stdout, auditMax), Stderr: cutBytes(r.Stderr, auditMax), CallID: callID,
		}
		// Code de sortie : hooks command seulement, et seulement s'il existe
		// (ni échec de lancement, ni délai, ni annulation).
		if r.Hook.Type == hooks.Command && r.ExitCode >= 0 {
			code := r.ExitCode
			p.ExitCode = &code
		}
		if err := t.emit(event.HookCompleted, p); err != nil {
			return hooks.Outcome{}, err
		}
	}
	if err := t.ctx.Err(); err != nil {
		return hooks.Outcome{}, err
	}
	if out.Stop {
		t.stopReason = stoppedByHook
	}
	return out, nil
}

// hookEval exécute les hooks prompt et agent pour le tour t. Ces hooks
// partagent l'état de la session (compteurs, consentement, avertissement de
// juridiction, demandes de permission) : sem les sérialise entre eux, les
// hooks command restent parallèles.
type hookEval struct {
	t      *turn
	runIDs map[string]string // Hook.ID -> RunID (parent des événements d'un hook agent)
	sem    chan struct{}
}

// Evaluate renvoie le texte du modèle ; ctx porte le délai du hook.
func (e hookEval) Evaluate(ctx context.Context, h hooks.Hook, prompt string) (string, error) {
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-e.sem }()
	switch h.Type {
	case hooks.Prompt:
		return e.t.evalPrompt(ctx, prompt)
	case hooks.Agent:
		return e.t.evalAgent(ctx, e.runIDs[h.ID], prompt)
	}
	return "", fmt.Errorf("unsupported hook type %q", h.Type)
}

// evalPrompt envoie une requête sans outils par le chemin sortant habituel
// (masquage, juridiction), hors de l'historique, sans deltas.
func (t *turn) evalPrompt(ctx context.Context, prompt string) (string, error) {
	d := t.e.d
	tc := *t // copie superficielle : même session, contexte du hook
	tc.ctx = ctx
	tc.noHooks = true // la requête d'un hook prompt ne déclenche aucun hook
	tc.noShape = true // hors conversation : état de la vue de la session intact
	tc.outer = t.turnCtx()
	text, _, provErr, err := tc.send(llm.Request{Messages: []llm.Message{
		{Role: llm.System, Content: i18n.T(d.Lang, "hooks.prompt_system")},
		{Role: llm.User, Content: prompt},
	}}, false)
	if err != nil {
		return "", err
	}
	if errors.Is(provErr, errNotSent) {
		return "", errors.New("provider not allowed")
	}
	if provErr != nil {
		return "", provErr
	}
	return text, nil
}

// evalAgent mène un sous-agent en lecture seule dans un tour imbriqué, sans
// hooks ; ses événements portent parent_call_id = runID.
func (t *turn) evalAgent(ctx context.Context, runID, prompt string) (string, error) {
	d := t.e.d
	h := []llm.Message{
		{Role: llm.System, Content: d.BasePrompt + "\n\n" + i18n.T(d.Lang, "hooks.agent_preamble")},
		{Role: llm.User, Content: prompt},
	}
	child := &turn{e: t.e, s: t.s, ctx: ctx, h: &h, tools: d.Tools.Only(hookTools), parent: runID, noHooks: true, outer: t.turnCtx()}
	res, err := child.loop()
	if err != nil {
		return "", err
	}
	if res.ProvErr != nil {
		return "", res.ProvErr
	}
	if res.Reason != "done" {
		return "", fmt.Errorf("agent hook stopped: %s", res.Reason)
	}
	return res.Final, nil
}

// turnCtx renvoie le contexte du tour, hors délai d'un hook.
func (t *turn) turnCtx() context.Context {
	if t.outer != nil {
		return t.outer
	}
	return t.ctx
}

// toolFields construit l'entrée commune de PreToolUse et PostToolUse.
func toolFields(c llm.ToolCall) map[string]any {
	args := safeArgs(c.Args)
	return map[string]any{
		"tool_name":   hooks.ClaudeName(c.Name),
		"tool_input":  hooks.ToClaude(c.Name, args),
		"tenon_tool":  c.Name,
		"tenon_input": args,
		"tool_use_id": c.ID,
	}
}

// cutBytes coupe s à n octets au plus, sur une frontière de rune.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

var _ hooks.Evaluator = hookEval{}

// hookContextHeader sépare un message du contexte ajouté par des hooks.
const hookContextHeader = "\n\n[hook context]\n"

// withHookContext joint à text le contexte ctx renvoyé par des hooks
// (UserPromptSubmit, SessionStart), dans un bloc séparé ; text si ctx est vide.
// Partagé par run, compact et Restore.
func withHookContext(text, ctx string) string {
	if ctx == "" {
		return text
	}
	return text + hookContextHeader + ctx
}

// joinContext joint les contextes non vides par une ligne vide.
func joinContext(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

// notifyPermission exécute les hooks Notification pour la demande p (si
// Deps.Notify, c'est-à-dire dans l'interface). Messages en anglais : ils
// partent au hook, pas à l'interface.
func (t *turn) notifyPermission(p event.PermissionRequestedPayload) error {
	if !t.e.d.Notify {
		return nil
	}
	msg := "tenon needs your permission to use " + p.Tool
	if p.Kind == "jurisdiction" && p.Provider != nil {
		msg = "tenon needs your permission to send data to " + p.Provider.ID
	}
	_, err := t.runHooks(hooks.Notification, "", map[string]any{
		"message": msg, "notification_type": "permission_prompt",
	}, "permission_prompt")
	return err
}

// stopFeedback est le préfixe du message renvoyé au modèle quand un hook Stop
// ou SubagentStop bloque la fin (destiné au modèle : hors i18n).
const stopFeedback = "Stop hook feedback: "

// stopHook exécute Stop (tour principal) ou SubagentStop (sous-agent) quand
// la boucle se termine avec la raison done. Sur un blocage, il ajoute le
// message de retour à l'historique, l'émet, et renvoie again = vrai : la
// boucle continue.
func (t *turn) stopHook(active bool) (again bool, err error) {
	if t.noHooks {
		return false, nil
	}
	ev := hooks.Stop
	fields := map[string]any{"stop_hook_active": active}
	if t.parent != "" {
		ev = hooks.SubagentStop
		fields["agent"] = t.agent
		fields["tool_use_id"] = t.parent
	}
	out, err := t.runHooks(ev, "", fields)
	if err != nil || t.stopReason != "" || !out.Block {
		return false, err
	}
	// Classification : le retour du hook entre dans l'historique.
	reason, err := t.scanHookText(out.Reason)
	if err != nil {
		return false, err
	}
	msg := stopFeedback + reason
	*t.h = append(*t.h, llm.Message{Role: llm.User, Content: msg})
	if err := t.emit(event.UserMessage, event.UserMessagePayload{Text: msg, Hook: string(ev)}); err != nil {
		return false, err
	}
	return true, nil
}
