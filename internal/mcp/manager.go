//go:build unix

// Package mcp connecte tenon à des serveurs MCP (stdio et HTTP streamable) et
// expose leurs outils comme des tools.Tool.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"codeberg.org/tenon/tenon/internal/config"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/tools"
)

const (
	defaultConnectTimeout = 15 * time.Second
	defaultCallTimeout    = 2 * time.Minute
	// killDelay sépare SIGTERM de SIGKILL à la fermeture d'un serveur stdio.
	killDelay = 2 * time.Second
	// maxSchema est la taille maximale (octets) d'un inputSchema accepté.
	maxSchema = 64 << 10
	// maxDescription est la longueur maximale (caractères) d'une description.
	maxDescription = 1024
	// maxSchemaDepth est la profondeur d'imbrication maximale d'un inputSchema.
	maxSchemaDepth = 32
	// maxToolsPerServer est le nombre maximal d'outils exposés par serveur.
	maxToolsPerServer = 128
	// maxListed borne le nombre d'outils lus dans tools/list (allowlist comprise).
	maxListed = 4096
)

// maxBody est la taille maximale (octets) d'un corps de réponse HTTP (variable pour les tests).
var maxBody int64 = 16 << 20

// Options règle la connexion aux serveurs MCP.
type Options struct {
	Getenv         func(string) string // nil : os.Getenv
	CallTimeout    time.Duration       // shell_timeout (2 min par défaut)
	ConnectTimeout time.Duration       // 15 s par défaut
	OutputLimit    int
	Consent        func(serverID string) bool // juridiction ; nil = toujours vrai
	Root           string                     // racine du projet : répertoire de travail des serveurs stdio

	// AllowNonEU est le consentement d'avance (--allow-non-eu) : sans lui, les
	// outils d'un serveur hors UE exigent une validation humaine (ForceAsk).
	AllowNonEU bool

	// dial remplace le transport réel (tests).
	dial func(config.MCPServer) (sdk.Transport, error)
}

// Down signale un serveur injoignable au démarrage, ou non lancé.
type Down struct {
	Server, Error string
	// NonEUSkipped : serveur hors UE (ou de juridiction inconnue) ni lancé
	// ni contacté, faute de consentement d'avance (AllowNonEU).
	NonEUSkipped bool
}

// skipped indique qu'un serveur ne doit pas être contacté : hors UE ou de
// juridiction inconnue, sans consentement d'avance. Aucun octet ne doit lui
// parvenir (ni initialize, ni jeton, ni variable d'environnement).
func skipped(decl config.MCPServer, opts Options) bool {
	if opts.AllowNonEU {
		return false
	}
	switch llm.Jurisdiction(decl.Jurisdiction) {
	case llm.EU, llm.Local:
		return false
	}
	return true
}

// Manager détient les sessions MCP ouvertes et les outils adaptés.
type Manager struct {
	servers  []*server
	tools    []tools.Tool
	secrets  []string
	warnings []string
}

// server est l'état d'une connexion à un serveur MCP.
type server struct {
	decl     config.MCPServer
	session  *sdk.ClientSession
	proc     *procTransport // nil hors stdio
	down     atomic.Bool
	closing  atomic.Bool // Manager.Close en cours : pas de ServerDown
	reported atomic.Bool
	once     sync.Once
	closeErr error
}

// Start se connecte en parallèle à chaque serveur non désactivé. Un serveur en
// échec est signalé dans []Down et ignoré.
func Start(ctx context.Context, servers []config.MCPServer, opts Options) (*Manager, []Down) {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = defaultConnectTimeout
	}
	if opts.CallTimeout <= 0 {
		opts.CallTimeout = defaultCallTimeout
	}
	m := &Manager{secrets: secretsOf(servers, opts.Getenv)}

	type outcome struct {
		srv   *server
		tools []*sdk.Tool
		err   error
	}
	results := make([]outcome, len(servers))
	var wg sync.WaitGroup
	for i, decl := range servers {
		if decl.Disabled || skipped(decl, opts) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv, list, err := connect(ctx, decl, opts)
			results[i] = outcome{srv, list, err}
		}()
	}
	wg.Wait()

	var down []Down
	taken := map[string]bool{}
	entries := map[string]regEntry{}
	for i, decl := range servers {
		if decl.Disabled {
			continue
		}
		if skipped(decl, opts) {
			down = append(down, Down{Server: decl.ID, Error: "serveur hors UE ignoré sans --allow-non-eu", NonEUSkipped: true})
			continue
		}
		r := results[i]
		if r.err != nil {
			down = append(down, Down{Server: decl.ID, Error: cleanText(r.err.Error())})
			continue
		}
		m.servers = append(m.servers, r.srv)
		exposed := 0
		for _, t := range r.tools {
			if len(decl.Tools) > 0 && !slices.Contains(decl.Tools, t.Name) {
				continue
			}
			if exposed == maxToolsPerServer {
				m.warnings = append(m.warnings, fmt.Sprintf("serveur MCP %s : plus de %d outils, les suivants sont ignorés", decl.ID, maxToolsPerServer))
				break
			}
			sch, err := validSchema(t.InputSchema)
			if err != nil {
				m.warnings = append(m.warnings, fmt.Sprintf("serveur MCP %s : outil %q ignoré (%v)", decl.ID, cleanText(t.Name), err))
				continue
			}
			exposed++
			name := ToolName(decl.ID, t.Name, taken)
			m.tools = append(m.tools, &mcpTool{
				srv:    r.srv,
				name:   name,
				tool:   t.Name,
				desc:   cutRunes(cleanText(t.Description), maxDescription),
				schema: sch,
				risk:   riskOf(decl),
				opts:   opts,
			})
			entries[name] = regEntry{server: decl.ID, jurisdiction: llm.Jurisdiction(decl.Jurisdiction), country: decl.Country}
		}
	}
	registerTools(servers, entries)
	return m, down
}

// Tools renvoie les outils de tous les serveurs joignables au démarrage.
func (m *Manager) Tools() []tools.Tool { return slices.Clone(m.tools) }

// Secrets renvoie les valeurs non vides des variables env_from et bearer_env.
func (m *Manager) Secrets() []string { return slices.Clone(m.secrets) }

// Warnings renvoie les avertissements du démarrage (outils ignorés).
func (m *Manager) Warnings() []string { return slices.Clone(m.warnings) }

// Close ferme les sessions et termine les processus stdio (SIGTERM au groupe,
// SIGKILL après 2 s). Idempotent.
func (m *Manager) Close() error {
	errs := make([]error, len(m.servers))
	var wg sync.WaitGroup
	for _, s := range m.servers {
		s.closing.Store(true)
	}
	for i, s := range m.servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.close()
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// Kill tue sur-le-champ (SIGKILL) le groupe de processus de chaque serveur
// stdio, sans attendre : réservé à une sortie immédiate du processus (second
// Ctrl+C). Les sessions HTTP ne sont pas fermées.
func (m *Manager) Kill() {
	for _, s := range m.servers {
		s.closing.Store(true)
		if s.proc != nil {
			s.proc.signal(syscall.SIGKILL)
		}
	}
}

func (s *server) close() error {
	s.once.Do(func() {
		if s.proc == nil {
			s.closeErr = s.session.Close()
			return
		}
		s.proc.signal(syscall.SIGTERM)
		t := time.AfterFunc(killDelay, func() { s.proc.signal(syscall.SIGKILL) })
		err := s.session.Close()
		t.Stop()
		// Aucun processus du groupe ne doit survivre au serveur.
		s.proc.signal(syscall.SIGKILL)
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			s.closeErr = err
		}
	})
	return s.closeErr
}

// markDown marque le serveur hors service et renvoie vrai au premier signalement.
func (s *server) markDown() bool {
	s.down.Store(true)
	if !s.reported.CompareAndSwap(false, true) {
		return false
	}
	go func() { _ = s.close() }()
	return true
}

// connect ouvre la session et liste les outils dans le délai ConnectTimeout.
func connect(ctx context.Context, decl config.MCPServer, opts Options) (*server, []*sdk.Tool, error) {
	cctx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
	defer cancel()
	srv := &server{decl: decl}

	var transport sdk.Transport
	switch {
	case opts.dial != nil:
		t, err := opts.dial(decl)
		if err != nil {
			return nil, nil, err
		}
		transport = t
	case decl.Transport == "stdio":
		if len(decl.Command) == 0 {
			return nil, nil, errors.New("commande vide")
		}
		cmd := exec.Command(decl.Command[0], decl.Command[1:]...)
		cmd.Env = minimalEnv(decl, opts.Getenv)
		cmd.Dir = opts.Root
		cmd.SysProcAttr = procAttr()
		srv.proc = &procTransport{inner: &sdk.CommandTransport{Command: cmd, TerminateDuration: killDelay}}
		transport = srv.proc
	case decl.Transport == "http":
		transport = &sdk.StreamableClientTransport{
			Endpoint:             decl.URL,
			HTTPClient:           httpClient(opts.Getenv(decl.BearerEnv), opts),
			DisableStandaloneSSE: true,
		}
	default:
		return nil, nil, fmt.Errorf("transport inconnu %q", decl.Transport)
	}

	// Un serveur stdio muet ne doit pas survivre au délai de connexion.
	stop := func() bool { return true }
	if srv.proc != nil {
		stop = context.AfterFunc(cctx, func() { srv.proc.signal(syscall.SIGKILL) })
	}
	defer stop()

	client := sdk.NewClient(&sdk.Implementation{Name: "tenon", Version: "1"}, nil)
	session, err := client.Connect(cctx, transport, nil)
	if err != nil {
		srv.killAfterFailure()
		return nil, nil, timeoutErr(cctx, err)
	}
	srv.session = session
	var list []*sdk.Tool
	for t, err := range session.Tools(cctx, nil) {
		if err != nil {
			srv.killAfterFailure()
			_ = srv.close()
			return nil, nil, timeoutErr(cctx, err)
		}
		list = append(list, t)
		if len(list) >= maxListed {
			break // pagination sans fin d'un serveur hostile
		}
	}
	return srv, list, nil
}

func (s *server) killAfterFailure() {
	if s.proc != nil {
		s.proc.signal(syscall.SIGKILL)
	}
}

func timeoutErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("délai de connexion dépassé : %w", err)
	}
	return err
}

// minimalEnv : PATH, HOME, LANG et les seules variables env_from (non vides).
func minimalEnv(decl config.MCPServer, getenv func(string) string) []string {
	env := []string{}
	seen := map[string]bool{}
	for _, k := range append([]string{"PATH", "HOME", "LANG"}, decl.EnvFrom...) {
		if seen[k] {
			continue
		}
		seen[k] = true
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func secretsOf(servers []config.MCPServer, getenv func(string) string) []string {
	var out []string
	add := func(k string) {
		if k == "" {
			return
		}
		if v := getenv(k); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	for _, s := range servers {
		for _, k := range s.EnvFrom {
			// PATH, HOME, LANG sont transmises d'office et ne sont pas des secrets.
			if k != "PATH" && k != "HOME" && k != "LANG" {
				add(k)
			}
		}
		add(s.BearerEnv)
	}
	return out
}

func riskOf(decl config.MCPServer) tools.Risk {
	switch decl.Risk {
	case "read":
		return tools.Read
	case "write":
		return tools.Write
	case "exec":
		return tools.Exec
	case "network":
		return tools.Network
	}
	if decl.Transport == "stdio" {
		return tools.Exec
	}
	return tools.Network
}

// validSchema vérifie que s est un objet JSON Schema de type "object" d'au plus 64 Ko.
func validSchema(s any) (json.RawMessage, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, errors.New("schéma illisible")
	}
	if len(raw) > maxSchema {
		return nil, fmt.Errorf("schéma de plus de %d Ko", maxSchema>>10)
	}
	// Passe lexicale bon marché avant l'analyse complète, coûteuse sur un schéma très imbriqué.
	if jsonDepth(raw) > maxSchemaDepth {
		return nil, fmt.Errorf("schéma imbriqué sur plus de %d niveaux", maxSchemaDepth)
	}
	var sch jsonschema.Schema
	if err := json.Unmarshal(raw, &sch); err != nil {
		return nil, errors.New("schéma JSON invalide")
	}
	if sch.Type != "object" {
		return nil, errors.New(`schéma sans type "object"`)
	}
	return raw, nil
}

// jsonDepth renvoie la profondeur d'imbrication maximale de raw (JSON valide).
func jsonDepth(raw []byte) int {
	dec := json.NewDecoder(bytes.NewReader(raw))
	depth, maxDepth := 0, 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return maxDepth
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
			if depth > maxDepth {
				maxDepth = depth
				if maxDepth > maxSchemaDepth {
					return maxDepth
				}
			}
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
}

// httpClient : aucun suivi de redirection, délais bornés, jeton Bearer si défini.
func httpClient(token string, opts Options) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{Timeout: opts.ConnectTimeout}).DialContext
	base.TLSHandshakeTimeout = opts.ConnectTimeout
	base.ResponseHeaderTimeout = opts.CallTimeout + opts.ConnectTimeout
	var rt http.RoundTripper = &capBody{base: base, max: maxBody}
	if token != "" {
		rt = &bearer{base: rt, token: token}
	}
	return &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type bearer struct {
	base  http.RoundTripper
	token string
}

func (b *bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// capBody borne la taille des corps de réponse HTTP.
type capBody struct {
	base http.RoundTripper
	max  int64
}

func (c *capBody) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.base.RoundTrip(r)
	if err == nil && resp.Body != nil {
		resp.Body = &limitedBody{rc: resp.Body, left: c.max}
	}
	return resp, err
}

var errBodyTooLarge = fmt.Errorf("réponse HTTP de plus de %d Mo", maxBody>>20)

// limitedBody renvoie une erreur dès que plus de left octets ont été lus.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, errBodyTooLarge
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.rc.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return 0, errBodyTooLarge
	}
	return n, err
}

func (l *limitedBody) Close() error { return l.rc.Close() }

// procTransport enveloppe CommandTransport pour connaître le groupe de processus.
type procTransport struct {
	inner *sdk.CommandTransport
	mu    sync.Mutex
	pid   int
}

func (p *procTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	conn, err := p.inner.Connect(ctx)
	if p.inner.Command.Process != nil {
		p.pid = p.inner.Command.Process.Pid
	}
	return conn, err
}

// signal envoie sig à tout le groupe de processus (Setpgid : pgid == pid).
func (p *procTransport) signal(sig syscall.Signal) {
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid > 0 {
		_ = syscall.Kill(-pid, sig)
	}
}
