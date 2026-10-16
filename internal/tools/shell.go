//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// Shell renvoie l'outil d'exécution de commandes, ancré sur root.
func Shell(root string, timeout time.Duration, limit int) Tool {
	// Résout les liens symboliques pour que $PWD (utilisé par bash) soit le chemin réel.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	return &shellTool{root: root, timeout: timeout, limit: limit}
}

type shellTool struct {
	root    string
	timeout time.Duration
	limit   int
}

type shellArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds *int   `json:"timeout_seconds"`
}

func (s *shellTool) Name() string { return "shell" }
func (s *shellTool) Description() string {
	return "Runs a bash command in the project directory (stdout and stderr combined)."
}
func (s *shellTool) Risk() Risk { return Exec }
func (s *shellTool) Schema() json.RawMessage {
	return schema(`"command":{"type":"string","description":"bash command to run"},`+
		`"timeout_seconds":{"type":"integer","description":"maximum duration in seconds (optional)"}`, "command")
}

func (s *shellTool) parse(args json.RawMessage) (shellArgs, error) {
	var a shellArgs
	if err := decode(args, &a); err != nil {
		return a, err
	}
	if a.Command == "" {
		return a, errors.New("invalid arguments: command required")
	}
	if a.TimeoutSeconds != nil && *a.TimeoutSeconds <= 0 {
		return a, errors.New("invalid arguments: timeout_seconds must be > 0")
	}
	return a, nil
}

func (s *shellTool) Target(args json.RawMessage) (Target, error) {
	a, err := s.parse(args)
	if err != nil {
		return Target{}, err
	}
	return Target{Command: a.Command}, nil
}

func (s *shellTool) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	a, err := s.parse(args)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	timeout := s.timeout
	if a.TimeoutSeconds != nil {
		timeout = time.Duration(*a.TimeoutSeconds) * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	buf := &capWriter{max: s.limit * utf8.UTFMax}
	cmd := exec.CommandContext(tctx, "bash", "-c", a.Command)
	cmd.Dir = s.root
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error { return killGroup(cmd) }

	if err := cmd.Start(); err != nil {
		return fail("cannot start command: %v", err), nil
	}
	waitErr := cmd.Wait()
	// Le processus principal est terminé : aucun processus d'arrière-plan ne doit survivre.
	_ = killGroup(cmd)

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	out := buf.String(s.limit)
	if tctx.Err() == context.DeadlineExceeded {
		return Result{IsError: true, Content: fmt.Sprintf("%s\n[timed out after %s]", out, timeout)}, nil
	}
	code := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.Is(waitErr, exec.ErrWaitDelay) {
			// Un processus d'arrière-plan gardait le tube ouvert ; la commande elle-même a réussi.
			code = 0
		} else if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
		} else {
			return fail("%s\n[execution error: %v]", out, waitErr), nil
		}
	}
	return Result{
		Content: fmt.Sprintf("%s\n[exit code: %d]", out, code),
		IsError: code != 0,
	}, nil
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

// capWriter garde au plus max octets (le début) et jette le reste sans erreur,
// pour ne pas tuer le processus par EPIPE ni saturer la mémoire.
type capWriter struct {
	mu      sync.Mutex
	max     int // <= 0 : illimité
	buf     []byte
	dropped bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.max <= 0 {
		w.buf = append(w.buf, p...)
		return len(p), nil
	}
	room := w.max - len(w.buf)
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		w.buf = append(w.buf, p[:room]...)
	}
	if room < len(p) {
		w.dropped = true
	}
	return len(p), nil
}

// String renvoie la sortie tronquée à limit caractères, avec le marqueur de troncature.
func (w *capWriter) String(limit int) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.buf
	if w.dropped {
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	return Truncate(string(b), limit)
}
