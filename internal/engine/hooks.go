package engine

import (
	"context"
	"errors"
	"fmt"
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
		// RunID est l'identifiant de cet événement HookStarted : seul ce
		// goroutine émet pour la session ici (s.turn tenu, hooks pas lancés).
		runID := fmt.Sprintf("%s-%d", t.s.id, t.s.seq+1)
		p := event.HookStartedPayload{
			RunID: runID, HookID: h.ID, Event: string(ev), Type: string(h.Type), CallID: callID,
		}
		if h.Type == hooks.Command {
			p.Command = cutBytes(h.Command, auditMax)
		} else {
			p.Prompt = cutBytes(h.Prompt, auditMax)
		}
		if err := t.emit(event.HookStarted, p); err != nil {
			return hooks.Outcome{}, err
		}
		runIDs[h.ID] = runID
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
	info := d.Provider.Info()
	msgs, ok, err := tc.outbound(info, []llm.Message{
		{Role: llm.System, Content: i18n.T(d.Lang, "hooks.prompt_system")},
		{Role: llm.User, Content: prompt},
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("provider not allowed")
	}
	text, _, provErr, err := tc.stream(llm.Request{Model: info.Model, Messages: msgs}, false)
	if err != nil {
		return "", err
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
	child := &turn{e: t.e, s: t.s, ctx: ctx, h: &h, tools: d.Tools.Only(hookTools), parent: runID, noHooks: true}
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
