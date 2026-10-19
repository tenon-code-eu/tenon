// Package engine contient la boucle agentique : il relie le fournisseur, les
// outils, les permissions, le contrôle sortant et l'audit, et émet des
// événements typés consommés par les interfaces.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// DefaultMaxIterations s'applique quand Deps.MaxIterations vaut 0 ou moins.
const DefaultMaxIterations = 50

// subscriberBuffer est la taille du tampon de chaque abonnement.
const subscriberBuffer = 256

var (
	// ErrAuditFailure : un Sink a échoué ; l'engine refuse désormais tout tour.
	ErrAuditFailure = errors.New("audit log write failure")
	// ErrUnknownPermission : Respond avec un identifiant inconnu ou déjà résolu.
	ErrUnknownPermission = errors.New("unknown permission request")
	// ErrSessionActive : Restore sur une session déjà utilisée dans ce processus.
	ErrSessionActive = errors.New("session already active in this process")
)

// Deps regroupe les dépendances de l'engine.
type Deps struct {
	Provider      provider.Provider
	Tools         *tools.Registry
	Permissions   *policy.Permissions
	Outbound      *policy.Outbound
	Sinks         []event.Sink // audit en premier
	MaxIterations int
	SystemPrompt  string
	ProjectRoot   string
	Instructions  []string // fichiers d'instructions chargés, recopiés dans SessionStarted
	Now           func() time.Time
	// ContextWindow est la fenêtre de contexte du modèle, en jetons estimés
	// (0 : DefaultContextWindow).
	ContextWindow int
	// CompactionThreshold est la fraction de la fenêtre au-delà de laquelle
	// l'historique est compacté (0 : DefaultCompactionThreshold).
	CompactionThreshold float64
	// UnavailableMCP liste les serveurs MCP injoignables au démarrage ; un
	// McpServerDown est émis pour chacun juste après chaque SessionStarted.
	UnavailableMCP []event.McpServerDownPayload
	// MCP liste les serveurs MCP actifs, recopiés dans SessionStarted.
	MCP []event.MCPServerInfo
}

// Reply répond à un PermissionRequested.
type Reply struct{ Allow, Remember bool }

// Engine exécute les tours d'agent. Il est sûr en usage concurrent ; les tours
// d'une même session sont sérialisés.
type Engine struct {
	d Deps

	// emitMu sérialise l'émission (sinks puis abonnés) et protège subs et failed.
	emitMu sync.Mutex
	subs   []chan event.Event
	failed bool

	mu       sync.Mutex // protège sessions et pending
	sessions map[string]*session
	pending  map[string]chan Reply
}

// session est l'état en mémoire d'une session (persistance : plan 2).
type session struct {
	turn sync.Mutex // tenu pendant tout un Submit ; protège les champs suivants

	id         string
	started    bool
	resumed    bool // reconstruite par Restore : SessionStarted{Resumed:true}
	seq        uint64
	permN      int
	warned     bool
	remembered map[string]bool
	history    []llm.Message // non masqué ; le masquage a lieu à chaque envoi
}

// New construit un Engine.
func New(d Deps) *Engine {
	if d.MaxIterations <= 0 {
		d.MaxIterations = DefaultMaxIterations
	}
	if d.SystemPrompt == "" {
		d.SystemPrompt = DefaultSystemPrompt
	}
	if d.ContextWindow <= 0 {
		d.ContextWindow = DefaultContextWindow
	}
	if d.CompactionThreshold <= 0 {
		d.CompactionThreshold = DefaultCompactionThreshold
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Engine{d: d, sessions: map[string]*session{}, pending: map[string]chan Reply{}}
}

// Subscribe renvoie un canal recevant chaque événement émis après l'appel
// (tampon de 256). La livraison est bloquante quand le tampon est plein :
// l'abonné DOIT lire le canal en continu, sinon l'engine se bloque. Appeler
// avant Submit. Le canal n'est jamais fermé ; quand Submit rend la main, tous
// les événements du tour sont déjà dans le canal.
func (e *Engine) Subscribe() <-chan event.Event {
	ch := make(chan event.Event, subscriberBuffer)
	e.emitMu.Lock()
	e.subs = append(e.subs, ch)
	e.emitMu.Unlock()
	return ch
}

// Respond transmet la réponse à une demande de permission en attente.
func (e *Engine) Respond(permissionID string, r Reply) error {
	e.mu.Lock()
	ch, ok := e.pending[permissionID]
	delete(e.pending, permissionID)
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w : %s", ErrUnknownPermission, permissionID)
	}
	ch <- r // tampon de 1, une seule réponse possible : jamais bloquant
	return nil
}

// Input est un message utilisateur : Text part au modèle, les autres champs
// documentent un éventuel skill (Typed = saisie brute de l'utilisateur).
type Input struct {
	Text, Typed, Skill, SkillSHA256 string
}

// Submit exécute un tour complet sur un texte simple ; voir SubmitInput.
func (e *Engine) Submit(ctx context.Context, sessionID, input string) error {
	return e.SubmitInput(ctx, sessionID, Input{Text: input})
}

// SubmitInput exécute un tour complet et bloque jusqu'à sa fin (TurnCompleted,
// erreur du fournisseur, annulation du contexte ou échec d'audit).
func (e *Engine) SubmitInput(ctx context.Context, sessionID string, in Input) error {
	if e.isFailed() {
		return ErrAuditFailure
	}
	s := e.session(sessionID)
	s.turn.Lock()
	defer s.turn.Unlock()
	t := &turn{e: e, s: s, ctx: ctx, h: &s.history, tools: e.d.Tools, deltas: true}
	return t.run(in)
}

func (e *Engine) isFailed() bool {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	return e.failed
}

func (e *Engine) session(id string) *session {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok {
		s = &session{
			id:         id,
			remembered: map[string]bool{},
			history:    []llm.Message{{Role: llm.System, Content: e.d.SystemPrompt}},
		}
		e.sessions[id] = s
	}
	return s
}

// emit écrit l'événement dans chaque Sink, dans l'ordre, puis le diffuse aux
// abonnés. Au premier échec d'un Sink, l'engine passe définitivement en
// fermeture par défaut : seul un Error{audit_failure} est diffusé (jamais
// écrit dans les sinks) et toute émission ultérieure renvoie ErrAuditFailure.
// L'appelant doit tenir s.turn.
func (e *Engine) emit(s *session, typ event.Type, payload any) error {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	if e.failed {
		return ErrAuditFailure
	}
	ev, err := event.New(s.id, s.seq+1, e.d.Now(), typ, payload)
	if err != nil {
		return err
	}
	s.seq++
	for _, sk := range e.d.Sinks {
		if err := sk.Write(ev); err != nil {
			e.failed = true
			s.seq++
			msg := fmt.Sprintf("%v: %v; no action will be executed", ErrAuditFailure, err)
			if fe, ferr := event.New(s.id, s.seq, e.d.Now(), event.Error,
				event.ErrorPayload{Code: "audit_failure", Message: msg}); ferr == nil {
				e.deliver(fe)
			}
			return fmt.Errorf("%w : %v", ErrAuditFailure, err)
		}
	}
	e.deliver(ev)
	return nil
}

// deliver envoie aux abonnés ; bloquant si un tampon est plein. emitMu tenu.
func (e *Engine) deliver(ev event.Event) {
	for _, ch := range e.subs {
		ch <- ev
	}
}
