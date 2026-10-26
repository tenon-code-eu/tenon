package acp

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// Command est une commande (skill) annoncée à l'éditeur.
type Command struct{ Name, Description string }

// Session est une session d'agent côté tenon.
type Session interface {
	ID() string
	ProjectRoot() string
	Subscribe() <-chan event.Event
	Submit(ctx context.Context, in engine.Input) error
	Respond(permissionID string, r engine.Reply) error
	Notices() []string
	Commands() []Command
	OldText(hash string) (string, bool)
	Close()
}

// Backend crée ou recharge des sessions. Une erreur *Error est transmise
// telle quelle ; toute autre devient CodeTenon avec son message.
type Backend interface {
	NewSession(ctx context.Context, cwd string, mcp []MCPServer, client *Implementation) (Session, error)
	LoadSession(ctx context.Context, id, cwd string, mcp []MCPServer, client *Implementation) (Session, []event.Event, error)
}

// Server est l'agent ACP : il route les requêtes de l'éditeur vers le Backend.
type Server struct {
	b       Backend
	version string
	lang    i18n.Lang

	// conn est la connexion servie (fixée par Serve avant toute requête).
	conn *Conn

	mu       sync.Mutex
	client   *Implementation
	sessions map[string]*entry
}

// entry est une session enregistrée, son pont et son créneau de tour.
type entry struct {
	s Session
	b *bridge
	// ready est fermé quand notices, commandes et rejeu ont été envoyés et
	// la pompe lancée.
	ready     chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	busy   bool
	cancel context.CancelFunc
}

// NewServer crée un serveur ; rien n'est lu avant Serve.
func NewServer(b Backend, version string, lang i18n.Lang) *Server {
	return &Server{b: b, version: version, lang: lang, sessions: map[string]*entry{}}
}

// Serve sert la connexion jusqu'à EOF ou annulation de ctx, puis ferme toutes
// les sessions.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	conn := NewConn(r, w, s)
	s.conn = conn
	err := conn.Serve(ctx)
	s.mu.Lock()
	all := s.sessions
	s.sessions = map[string]*entry{}
	s.mu.Unlock()
	for _, e := range all {
		e.close()
	}
	return err
}

// close arrête le pont puis ferme la session (une seule fois).
func (e *entry) close() {
	e.closeOnce.Do(func() {
		e.b.close()
		e.s.Close()
	})
}

// Handle implémente Handler.
func (s *Server) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		return s.initialize(params)
	case "session/new":
		return s.newSession(ctx, params)
	case "session/load":
		return s.loadSession(ctx, params)
	case "session/prompt":
		return s.prompt(ctx, params)
	case "session/cancel":
		s.cancelTurn(params)
		return nil, nil
	}
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}

func decode(params json.RawMessage, v any) error {
	if err := json.Unmarshal(params, v); err != nil {
		return &Error{Code: CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return nil
}

func (s *Server) initialize(params json.RawMessage) (any, error) {
	var req InitializeRequest
	if err := decode(params, &req); err != nil {
		return nil, err
	}
	if req.ProtocolVersion != 1 {
		return nil, &Error{Code: CodeInvalidParams, Message: "unsupported protocolVersion (tenon speaks 1)"}
	}
	s.mu.Lock()
	s.client = req.ClientInfo
	s.mu.Unlock()
	return map[string]any{
		"protocolVersion": 1,
		"agentCapabilities": map[string]any{
			"loadSession":        true,
			"promptCapabilities": map[string]any{"image": false, "audio": false, "embeddedContext": true},
			"mcpCapabilities":    map[string]any{"http": true, "sse": false},
		},
		"authMethods": []any{},
		"agentInfo":   map[string]any{"name": "tenon", "version": s.version},
	}, nil
}

func (s *Server) clientInfo() *Implementation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

func backendErr(err error) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeTenon, Message: err.Error()}
}

func checkCwd(cwd string) error {
	if !filepath.IsAbs(cwd) {
		return &Error{Code: CodeInvalidParams, Message: "cwd must be an absolute path"}
	}
	return nil
}

func (s *Server) newSession(ctx context.Context, params json.RawMessage) (any, error) {
	var req NewSessionRequest
	if err := decode(params, &req); err != nil {
		return nil, err
	}
	if err := checkCwd(req.Cwd); err != nil {
		return nil, err
	}
	sess, err := s.b.NewSession(ctx, req.Cwd, req.MCPServers, s.clientInfo())
	if err != nil {
		return nil, backendErr(err)
	}
	return s.register(sess, nil, map[string]any{"sessionId": sess.ID()}), nil
}

func (s *Server) loadSession(ctx context.Context, params json.RawMessage) (any, error) {
	var req LoadSessionRequest
	if err := decode(params, &req); err != nil {
		return nil, err
	}
	if err := checkCwd(req.Cwd); err != nil {
		return nil, err
	}
	// Une session vivante en plein tour n'est pas remplacée.
	s.mu.Lock()
	live := s.sessions[req.SessionID]
	s.mu.Unlock()
	if live != nil {
		live.mu.Lock()
		busy := live.busy
		live.mu.Unlock()
		if busy {
			return nil, &Error{Code: CodeTenon, Message: "turn in progress"}
		}
	}
	sess, replay, err := s.b.LoadSession(ctx, req.SessionID, req.Cwd, req.MCPServers, s.clientInfo())
	if err != nil {
		return nil, backendErr(err)
	}
	return s.register(sess, replay, nil), nil
}

// register enregistre la session et l'abonne à son pont ; une session de
// même id est fermée et remplacée. Le résultat renvoyé (result) est suivi,
// une fois écrit, des notices, des commandes et du rejeu : le client ne
// connaît l'identifiant qu'en lisant la réponse.
func (s *Server) register(sess Session, replay []event.Event, result any) AfterReply {
	e := &entry{s: sess, b: newBridge(s.conn, s.lang, sess), ready: make(chan struct{})}
	s.mu.Lock()
	old := s.sessions[sess.ID()]
	s.sessions[sess.ID()] = e
	s.mu.Unlock()
	if old != nil && old.s != sess {
		old.close()
	}
	return AfterReply{Result: result, Then: func() {
		e.b.start(sess.Notices(), sess.Commands(), replay)
		close(e.ready)
	}}
}

func (s *Server) entry(id string) (*entry, error) {
	s.mu.Lock()
	e := s.sessions[id]
	s.mu.Unlock()
	if e == nil {
		return nil, &Error{Code: CodeUnknownSession, Message: "unknown session: " + id}
	}
	return e, nil
}

func (s *Server) prompt(ctx context.Context, params json.RawMessage) (any, error) {
	var req PromptRequest
	if err := decode(params, &req); err != nil {
		return nil, err
	}
	e, err := s.entry(req.SessionID)
	if err != nil {
		return nil, err
	}
	in, err := buildInput(req.Prompt, e.s.Commands())
	if err != nil {
		return nil, err
	}

	select {
	case <-e.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		return nil, &Error{Code: CodeTenon, Message: "turn in progress"}
	}
	e.b.beginTurn()
	e.busy, e.cancel = true, cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.busy, e.cancel = false, nil
		e.mu.Unlock()
	}()

	serr := e.s.Submit(pctx, in)
	// Les mises à jour du tour précèdent la réponse.
	reason := e.b.flush()
	// Un tour annulé par session/cancel reste cancelled, même sans
	// TurnCompleted.
	if pctx.Err() != nil || errors.Is(serr, context.Canceled) {
		return map[string]any{"stopReason": "cancelled"}, nil
	}
	if serr != nil {
		return nil, &Error{Code: CodeTenon, Message: serr.Error()}
	}
	return map[string]any{"stopReason": stopReason(reason)}, nil
}

func (s *Server) cancelTurn(params json.RawMessage) {
	var req CancelNotification
	if json.Unmarshal(params, &req) != nil {
		return
	}
	e, err := s.entry(req.SessionID)
	if err != nil {
		return
	}
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		e.b.cancel()
	}
}

// buildInput assemble le texte du prompt et détecte une commande /name.
func buildInput(blocks []ContentBlock, cmds []Command) (engine.Input, error) {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource_link":
			parts = append(parts, "@"+uriToPath(b.URI))
		case "resource":
			if b.Resource == nil {
				return engine.Input{}, &Error{Code: CodeInvalidParams, Message: "resource block without resource"}
			}
			parts = append(parts, "<resource uri=\""+html.EscapeString(b.Resource.URI)+"\">\n"+b.Resource.Text+"\n</resource>")
		default:
			return engine.Input{}, &Error{Code: CodeInvalidParams, Message: "unsupported content block type: " + b.Type}
		}
	}
	text := strings.Join(parts, "\n\n")
	if strings.HasPrefix(text, "/") {
		name, rest := text[1:], ""
		if i := strings.IndexFunc(name, unicode.IsSpace); i >= 0 {
			name, rest = name[:i], name[i:]
		}
		for _, c := range cmds {
			if c.Name == name {
				return engine.Input{Skill: name, Text: strings.TrimSpace(rest), Typed: text}, nil
			}
		}
	}
	return engine.Input{Text: text}, nil
}

// uriToPath convertit une URI file:// en chemin ; toute autre URI est rendue
// telle quelle.
func uriToPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return raw
	}
	return u.Path
}
