package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/tenon-code-eu/tenon/internal/acp"
	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/session"
	"github.com/tenon-code-eu/tenon/internal/skills"
)

// runACP sert l'Agent Client Protocol sur stdin/stdout (« tenon acp ») :
// stdout ne porte que du JSON-RPC, stderr les journaux techniques. Chaque
// session suit le démarrage de tenon en ligne de commande (startEngine).
// Code 0 à la fin de stdin (toutes les sessions fermées) ou à l'annulation.
func runACP(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, globalPath string) int {
	if stdin == nil || stdout == nil {
		fmt.Fprintln(stderr, "tenon acp: stdin and stdout are required")
		return 1
	}
	// L'éditeur qui ferme stdout ne doit pas tuer tenon par SIGPIPE avant
	// endSession (hooks SessionEnd, fermeture de l'audit) : l'écriture
	// échoue simplement.
	signal.Ignore(syscall.SIGPIPE)
	lang := globalLang(getenv, globalPath)
	b := &acpBackend{getenv: getenv, globalPath: globalPath, stderr: stderr}
	err := acp.NewServer(b, version, lang).Serve(ctx, stdin, stdout)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "tenon acp:", err)
	}
	return 0
}

// globalLang est la langue de la configuration globale (anglais par
// défaut) : libellés des demandes de permission, communs aux sessions.
func globalLang(getenv func(string) string, globalPath string) i18n.Lang {
	l := config.Defaults(getenv).Lang
	pol, err := orgpolicy.Load(orgpolicy.Resolve(getenv))
	if err != nil {
		pol = nil
	}
	if cfg, err := config.LoadPolicy(globalPath, "", getenv, pol); err == nil {
		l = cfg.Lang
	}
	return i18n.Parse(l)
}

// acpBackend ouvre une session tenon (startEngine) par session ACP.
type acpBackend struct {
	getenv     func(string) string
	globalPath string
	stderr     io.Writer
}

// NewSession implémente acp.Backend.
func (b *acpBackend) NewSession(ctx context.Context, cwd string, mcp []acp.MCPServer, client *acp.Implementation) (acp.Session, error) {
	return b.open(ctx, cwd, "", mcp, client)
}

// LoadSession implémente acp.Backend : une session absente ou d'un autre
// projet est inconnue (même contrôle que --resume).
func (b *acpBackend) LoadSession(ctx context.Context, id, cwd string, mcp []acp.MCPServer, client *acp.Implementation) (acp.Session, []event.Event, error) {
	wd := filepath.Clean(cwd)
	unknown := &acp.Error{Code: acp.CodeUnknownSession, Message: "unknown session: " + id}
	sdir := sessionsDir(b.getenv)
	if session.Exists(sdir, id) != nil {
		return nil, nil, unknown
	}
	if evs, _, err := session.Load(sdir, id); err != nil || !sameProject(evs, wd) {
		return nil, nil, unknown
	}
	s, err := b.open(ctx, wd, id, mcp, client)
	if err != nil {
		return nil, nil, err
	}
	return s, s.st.history, nil
}

// open démarre la session du dossier cwd comme « tenon » lancé dans ce
// dossier : chemin tel quel (un lien symbolique n'est pas résolu, comme le
// répertoire courant hérité du shell), langue de la configuration du projet.
func (b *acpBackend) open(ctx context.Context, cwd, resume string, mcp []acp.MCPServer, client *acp.Implementation) (*acpSession, error) {
	wd := filepath.Clean(cwd)
	if fi, err := os.Stat(wd); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("cwd: %s: not a directory", wd)
	}
	lang, _ := uiLang("", b.getenv, wd, b.globalPath)
	s, err := loadSetup(lang, b.getenv, wd, b.globalPath)
	if err != nil {
		return nil, err
	}
	var notices []string
	var extra []config.MCPServer
	if len(mcp) > 0 {
		if s.cfg.ACP.AcceptEditorMCP {
			var n []string
			extra, n = acp.EditorMCP(mcp, s.cfg.MCP.Servers, lang)
			notices = append(notices, n...)
		} else {
			notices = append(notices, i18n.T(lang, "acp.mcp_ignored"))
		}
	}
	var ci *event.ClientInfo
	if client != nil {
		ci = &event.ClientInfo{Name: client.Name, Version: client.Version}
	}
	disc := discoverSkills(lang, b.getenv, wd)
	st, err := startEngine(engineOptions{
		ctx: ctx, getenv: b.getenv, wd: wd, globalPath: b.globalPath, lang: lang,
		resume: resume, stderr: b.stderr, interactive: true, skills: &disc,
		extraMCP: extra, client: ci, transport: "acp",
	})
	if err != nil {
		return nil, err
	}
	// Avis du démarrage : ceux que tenon -p affiche sur stderr, puis ceux des
	// serveurs MCP de l'éditeur ; masqués comme en ligne de commande. Les
	// serveurs indisponibles sont signalés par l'événement McpServerDown du
	// premier tour (notice du pont), comme en mode non interactif.
	red := redactText(st.redactor)
	var all []string
	for _, n := range slices.Concat(st.notices, notices) {
		all = append(all, red(n))
	}
	return &acpSession{st: st, wd: wd, lang: lang, stderr: b.stderr, notices: all}, nil
}

// acpSession est une session tenon pilotée par l'éditeur.
type acpSession struct {
	st      started
	wd      string
	lang    i18n.Lang
	stderr  io.Writer
	notices []string
	once    sync.Once
}

func (s *acpSession) ID() string          { return s.st.sessionID }
func (s *acpSession) ProjectRoot() string { return s.wd }

// Subscribe renvoie le canal de l'engine lui-même : quand Submit rend la
// main, tous les événements du tour y sont déjà.
func (s *acpSession) Subscribe() <-chan event.Event { return s.st.eng.Subscribe() }

// Submit exécute un tour. Une commande /skill est développée comme en ligne
// de commande (corps du skill, empreinte).
func (s *acpSession) Submit(ctx context.Context, in engine.Input) error {
	if in.Skill != "" {
		exp, ok, err := s.st.skills.Expand(in.Typed)
		if err != nil {
			return err
		}
		if ok && exp.Skill != "" {
			in = engine.Input{Text: exp.Text, Skill: exp.Skill, SkillSHA256: exp.SHA256, Typed: in.Typed}
		}
	}
	return s.st.eng.SubmitInput(ctx, s.st.sessionID, in)
}

func (s *acpSession) Respond(permissionID string, r engine.Reply) error {
	return s.st.eng.Respond(permissionID, r)
}

func (s *acpSession) Notices() []string { return s.notices }

// Commands renvoie les skills invocables par /nom (sans ceux que masque une
// commande intégrée, comme dans la TUI).
func (s *acpSession) Commands() []acp.Command {
	var out []acp.Command
	for _, sk := range s.st.skills.List() {
		if !skills.IsBuiltin(sk.Name) {
			out = append(out, acp.Command{Name: sk.Name, Description: sk.Description})
		}
	}
	return out
}

// OldText lit une copie d'avant écriture du magasin de points de reprise ;
// hash est l'empreinte d'un ToolResult (« sha256:<hex> »).
func (s *acpSession) OldText(hash string) (string, bool) {
	h, ok := strings.CutPrefix(hash, "sha256:")
	if !ok || s.st.checkpoints == nil {
		return "", false
	}
	b, err := s.st.checkpoints.Load(h)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (s *acpSession) Redact(text string) string { return redactText(s.st.redactor)(text) }

// RedactPayload masque une charge utile d'événement comme --output json.
func (s *acpSession) RedactPayload(payload string) string {
	out, _ := s.st.redactor.RedactJSON(payload)
	return out
}

// Close exécute les hooks SessionEnd puis ferme serveurs MCP, session et
// journal d'audit (une seule fois). EndSession attend la fin d'un tour en
// cours (borné par endSessionTimeout).
func (s *acpSession) Close() {
	s.once.Do(func() {
		endSession(s.st, s.lang, "other", false, s.stderr)
		s.st.close()
	})
}
