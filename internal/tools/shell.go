//go:build unix

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
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
	return "Exécute une commande bash dans le répertoire du projet (sortie standard et erreur combinées)."
}
func (s *shellTool) Risk() Risk { return Exec }
func (s *shellTool) Schema() json.RawMessage {
	return schema(`"command":{"type":"string","description":"commande bash à exécuter"},`+
		`"timeout_seconds":{"type":"integer","description":"délai maximal en secondes (optionnel)"}`, "command")
}

func (s *shellTool) parse(args json.RawMessage) (shellArgs, error) {
	var a shellArgs
	if err := decode(args, &a); err != nil {
		return a, err
	}
	if a.Command == "" {
		return a, errors.New("arguments invalides : command requis")
	}
	if a.TimeoutSeconds != nil && *a.TimeoutSeconds <= 0 {
		return a, errors.New("arguments invalides : timeout_seconds doit être > 0")
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

	var buf bytes.Buffer
	cmd := exec.CommandContext(tctx, "bash", "-c", a.Command)
	cmd.Dir = s.root
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error { return killGroup(cmd) }

	if err := cmd.Start(); err != nil {
		return fail("impossible de lancer la commande : %v", err), nil
	}
	waitErr := cmd.Wait()
	// Le processus principal est terminé : aucun processus d'arrière-plan ne doit survivre.
	_ = killGroup(cmd)

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	out := Truncate(buf.String(), s.limit)
	if tctx.Err() == context.DeadlineExceeded {
		return Result{IsError: true, Content: fmt.Sprintf("%s\n[délai dépassé après %s]", out, timeout)}, nil
	}
	code := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
		} else {
			return fail("%s\n[erreur d'exécution : %v]", out, waitErr), nil
		}
	}
	return Result{
		Content: fmt.Sprintf("%s\n[code de sortie: %d]", out, code),
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
