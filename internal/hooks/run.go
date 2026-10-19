//go:build unix

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// maxOutput borne stdout et stderr d'un hook command (64 Kio chacun).
const maxOutput = 65536

// Délais par défaut (si Hook.Timeout est nul) et plafond de SessionEnd.
const (
	defaultCommandTimeout = 60 * time.Second
	defaultPromptTimeout  = 30 * time.Second
	sessionEndCap         = 5 * time.Second
)

// Env regroupe le contexte d'exécution commun à tous les hooks.
type Env struct {
	SessionID, TranscriptPath, ProjectRoot string
}

// Evaluator exécute les hooks prompt et agent : il renvoie le texte du modèle.
type Evaluator interface {
	Evaluate(ctx context.Context, h Hook, prompt string) (text string, err error)
}

// Status est l'état final d'un hook.
type Status string

const (
	StatusOK        Status = "ok"
	StatusBlocked   Status = "blocked"
	StatusError     Status = "error"
	StatusTimeout   Status = "timeout"
	StatusCancelled Status = "cancelled"
)

// Result est le résultat d'un hook.
type Result struct {
	Hook     Hook
	Status   Status
	ExitCode int // -1 si ce n'est pas un hook command (ou lancement impossible)
	// Decision : "", "allow", "ask", "deny" ou "block".
	Decision      string
	Reason        string
	Context       string
	SystemMessage string
	StopReason    string
	Stdout        string
	Stderr        string
	Err           string
	UpdatedInput  json.RawMessage // format Claude, non traduit
	Stop          bool
	Duration      time.Duration
}

// Outcome est le résultat combiné d'un point de hook.
// Après combinaison, au plus un de Block, Ask, Allow est vrai.
type Outcome struct {
	Results    []Result
	Block      bool
	Ask        bool
	Allow      bool
	Reason     string
	Context    string
	StopReason string
	// UpdatedInput : valeur unique demandée par les hooks ; nil en cas de conflit.
	UpdatedInput json.RawMessage
	Conflict     bool
	Stop         bool
}

// Run exécute en parallèle les hooks hs pour l'événement ev et combine les résultats.
// L'entrée JSON est fields plus les champs communs ; Results suit l'ordre de hs.
func Run(ctx context.Context, hs []Hook, ev Event, fields map[string]any, env Env, eval Evaluator) Outcome {
	in := make(map[string]any, len(fields)+4)
	for k, v := range fields {
		in[k] = v
	}
	in["session_id"] = env.SessionID
	in["transcript_path"] = env.TranscriptPath
	in["cwd"] = env.ProjectRoot
	in["hook_event_name"] = string(ev)
	input, merr := json.Marshal(in)

	results := make([]Result, len(hs))
	var wg sync.WaitGroup
	for i := range hs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := hs[i]
			start := time.Now()
			var r Result
			if merr != nil {
				r = Result{Hook: h, Status: StatusError, ExitCode: -1, Err: "cannot encode hook input: " + merr.Error()}
			} else {
				r = runOne(ctx, h, ev, input, env, eval)
			}
			r.Hook = h
			r.Duration = time.Since(start)
			results[i] = r
		}(i)
	}
	wg.Wait()
	return combine(results)
}

// timeoutFor renvoie le délai effectif d'un hook.
func timeoutFor(h Hook, ev Event) time.Duration {
	d := h.Timeout
	if d <= 0 {
		d = defaultCommandTimeout
		if h.Type == Prompt {
			d = defaultPromptTimeout
		}
	}
	if ev == SessionEnd && d > sessionEndCap {
		d = sessionEndCap
	}
	return d
}

func runOne(ctx context.Context, h Hook, ev Event, input []byte, env Env, eval Evaluator) Result {
	timeout := timeoutFor(h, ev)
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if h.Type == Command {
		return runCommand(ctx, tctx, h, ev, input, env, timeout)
	}
	return runEvaluator(ctx, tctx, h, ev, input, eval, timeout)
}

// ctxStatus classe une interruption : annulation du parent, sinon délai du hook.
func ctxStatus(parent, tctx context.Context, timeout time.Duration) (Status, string, bool) {
	if parent.Err() != nil {
		return StatusCancelled, "cancelled", true
	}
	if tctx.Err() != nil {
		return StatusTimeout, "timed out after " + timeout.String(), true
	}
	return "", "", false
}

func runCommand(parent, tctx context.Context, h Hook, ev Event, input []byte, env Env, timeout time.Duration) Result {
	stdout := &capBuf{max: maxOutput}
	stderr := &capBuf{max: maxOutput}
	cmd := exec.CommandContext(tctx, "bash", "-c", h.Command)
	cmd.Dir = env.ProjectRoot
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+env.ProjectRoot, "TENON_PROJECT_DIR="+env.ProjectRoot)
	// bytes.Reader : un EPIPE sur stdin (hook qui ne lit pas) est ignoré par os/exec.
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error { return killGroup(cmd) }

	r := Result{ExitCode: -1}
	if err := cmd.Start(); err != nil {
		r.Status, r.Err = StatusError, "cannot start hook: "+err.Error()
		return r
	}
	waitErr := cmd.Wait()
	// Le processus principal est terminé : aucun processus d'arrière-plan ne doit survivre.
	_ = killGroup(cmd)

	r.Stdout, r.Stderr = stdout.String(), stderr.String()
	if st, msg, ok := ctxStatus(parent, tctx, timeout); ok {
		r.Status, r.Err = st, msg
		return r
	}
	code := 0
	if waitErr != nil {
		var ee *exec.ExitError
		switch {
		case errors.Is(waitErr, exec.ErrWaitDelay):
			// Un processus d'arrière-plan gardait le tube ouvert ; le hook lui-même a réussi.
		case errors.As(waitErr, &ee):
			code = ee.ExitCode()
		default:
			r.Status, r.Err = StatusError, waitErr.Error()
			return r
		}
	}
	r.ExitCode = code
	switch code {
	case 0:
		interpretStdout(&r, ev)
	case 2:
		reason := strings.TrimSpace(r.Stderr)
		if blockable(ev) {
			r.Decision, r.Reason, r.Status = blockDecision(ev), reason, StatusBlocked
		} else {
			r.Status, r.Err = StatusError, "exit 2: "+reason
		}
	default:
		r.Status, r.Err = StatusError, fmt.Sprintf("exit status %d", code)
	}
	return r
}

func runEvaluator(parent, tctx context.Context, h Hook, ev Event, input []byte, eval Evaluator, timeout time.Duration) Result {
	r := Result{ExitCode: -1}
	if eval == nil {
		r.Status, r.Err = StatusError, "no evaluator for "+string(h.Type)+" hook"
		return r
	}
	text, err := eval.Evaluate(tctx, h, BuildPrompt(h.Prompt, input))
	if st, msg, ok := ctxStatus(parent, tctx, timeout); ok {
		r.Status, r.Err = st, msg
		return r
	}
	if err != nil {
		r.Status, r.Err = StatusError, err.Error()
		return r
	}
	ok, reason, perr := ParseVerdict(text)
	if perr != nil {
		r.Status, r.Err = StatusError, perr.Error()
		return r
	}
	r.Status = StatusOK
	if !ok && blockable(ev) {
		r.Decision, r.Reason, r.Status = blockDecision(ev), reason, StatusBlocked
	}
	return r
}

// blockable : l'événement accepte un refus (exit 2, ok:false, decision block).
func blockable(ev Event) bool {
	switch ev {
	case PreToolUse, PostToolUse, UserPromptSubmit, Stop, SubagentStop:
		return true
	}
	return false
}

func blockDecision(ev Event) string {
	if ev == PreToolUse {
		return "deny"
	}
	return "block"
}

// hookJSON est la sortie JSON d'un hook (champs communs et propres à l'événement).
type hookJSON struct {
	Continue           *bool  `json:"continue"`
	StopReason         string `json:"stopReason"`
	SystemMessage      string `json:"systemMessage"`
	Decision           string `json:"decision"`
	Reason             string `json:"reason"`
	HookSpecificOutput *struct {
		HookEventName            string          `json:"hookEventName"`
		PermissionDecision       string          `json:"permissionDecision"`
		PermissionDecisionReason string          `json:"permissionDecisionReason"`
		UpdatedInput             json.RawMessage `json:"updatedInput"`
		AdditionalContext        string          `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// interpretStdout lit la sortie d'un hook qui a terminé avec le code 0.
func interpretStdout(r *Result, ev Event) {
	r.Status = StatusOK
	trimmed := strings.TrimSpace(r.Stdout)
	if !strings.HasPrefix(strings.TrimLeft(r.Stdout, " \t\r\n"), "{") {
		if trimmed != "" && (ev == UserPromptSubmit || ev == SessionStart) {
			r.Context = trimmed
		}
		return
	}
	var j hookJSON
	if err := json.Unmarshal([]byte(trimmed), &j); err != nil {
		r.Status, r.Err = StatusError, "invalid JSON output: "+err.Error()
		return
	}
	if j.Continue != nil && !*j.Continue {
		r.Stop, r.StopReason = true, j.StopReason
	}
	r.SystemMessage = j.SystemMessage
	var hso = j.HookSpecificOutput
	if hso != nil && hso.HookEventName != string(ev) {
		hso = nil
	}
	switch ev {
	case PreToolUse:
		switch j.Decision { // ancien format
		case "approve":
			r.Decision, r.Reason = "allow", j.Reason
		case "block":
			r.Decision, r.Reason = "deny", j.Reason
		}
		if hso != nil {
			switch hso.PermissionDecision {
			case "allow", "ask", "deny":
				r.Decision, r.Reason = hso.PermissionDecision, hso.PermissionDecisionReason
			}
			if len(hso.UpdatedInput) > 0 && string(hso.UpdatedInput) != "null" {
				r.UpdatedInput = hso.UpdatedInput
			}
		}
	case PostToolUse, UserPromptSubmit, Stop, SubagentStop:
		if j.Decision == "block" {
			r.Decision, r.Reason = "block", j.Reason
		}
	}
	if hso != nil && (ev == PostToolUse || ev == UserPromptSubmit || ev == SessionStart) {
		r.Context = hso.AdditionalContext
	}
	if r.Decision == "deny" || r.Decision == "block" {
		r.Status = StatusBlocked
	}
}

// combine fusionne les résultats : deny/block > ask > allow.
func combine(results []Result) Outcome {
	o := Outcome{Results: results}
	var reasons = map[string][]string{}
	var ctxs, stops []string
	var updated []json.RawMessage
	var keys []string // formes compactées, pour la comparaison
	for _, r := range results {
		d := r.Decision
		if d == "deny" {
			d = "block"
		}
		if d != "" {
			if r.Reason != "" {
				reasons[d] = append(reasons[d], r.Reason)
			}
			switch d {
			case "block":
				o.Block = true
			case "ask":
				o.Ask = true
			case "allow":
				o.Allow = true
			}
		}
		if r.Context != "" {
			ctxs = append(ctxs, r.Context)
		}
		if r.Stop {
			o.Stop = true
			if r.StopReason != "" {
				stops = append(stops, r.StopReason)
			}
		}
		if len(r.UpdatedInput) > 0 && d != "block" {
			var b bytes.Buffer
			if json.Compact(&b, r.UpdatedInput) != nil {
				b.Reset()
				b.Write(r.UpdatedInput)
			}
			k := b.String()
			dup := false
			for _, e := range keys {
				if e == k {
					dup = true
				}
			}
			if !dup {
				keys = append(keys, k)
				updated = append(updated, r.UpdatedInput)
			}
		}
	}
	switch {
	case o.Block:
		o.Ask, o.Allow = false, false
		o.Reason = strings.Join(reasons["block"], "\n")
	case o.Ask:
		o.Allow = false
		o.Reason = strings.Join(reasons["ask"], "\n")
	case o.Allow:
		o.Reason = strings.Join(reasons["allow"], "\n")
	}
	o.Context = strings.Join(ctxs, "\n\n")
	o.StopReason = strings.Join(stops, "\n")
	switch len(updated) {
	case 0:
	case 1:
		o.UpdatedInput = updated[0]
	default:
		o.Conflict = true
	}
	return o
}

// BuildPrompt remplace $ARGUMENTS par input ; absent, input est ajouté après une ligne vide.
func BuildPrompt(tmpl string, input []byte) string {
	if strings.Contains(tmpl, "$ARGUMENTS") {
		return strings.ReplaceAll(tmpl, "$ARGUMENTS", string(input))
	}
	return tmpl + "\n\n" + string(input)
}

// ParseVerdict lit le verdict {"ok": bool, "reason": string} du premier « { » au dernier « } ».
func ParseVerdict(text string) (ok bool, reason string, err error) {
	i, k := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || k < i {
		return false, "", errors.New("invalid verdict: no JSON object")
	}
	var v struct {
		OK     *bool  `json:"ok"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(text[i:k+1]), &v); err != nil {
		return false, "", errors.New("invalid verdict: " + err.Error())
	}
	if v.OK == nil {
		return false, "", errors.New("invalid verdict: missing boolean ok")
	}
	return *v.OK, v.Reason, nil
}

// killGroup tue tout le groupe de processus de cmd (Setpgid : pgid == pid).
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// capBuf garde au plus max octets (le début) et jette le reste sans erreur.
type capBuf struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (w *capBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := w.max - len(w.buf); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		w.buf = append(w.buf, p[:room]...)
	}
	return len(p), nil
}

func (w *capBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}
