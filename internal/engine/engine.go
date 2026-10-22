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

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/checkpoint"
	"github.com/tenon-code-eu/tenon/internal/classify"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/hostmatch"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/routing"
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
	// Agents liste les sous-agents disponibles (nil : pas d'outil agent).
	Agents *agents.Catalog
	// BasePrompt est le prompt sans sections skills/agents, base du prompt des
	// sous-agents (vide : SystemPrompt).
	BasePrompt string
	// SkillsSection est la section des skills ajoutée au prompt d'un sous-agent
	// qui dispose de l'outil skill.
	SkillsSection string
	// Lang est la langue du préambule des sous-agents (zéro : FR).
	Lang i18n.Lang
	// OutputLimit tronque le résultat d'un sous-agent, en caractères (0 : non).
	OutputLimit int
	// Hooks est l'ensemble des hooks utilisateur (nil : aucun hook).
	Hooks *hooks.Set
	// TranscriptPath est le fichier de session transmis aux hooks.
	TranscriptPath string
	// Notify active l'événement de hook Notification (vrai dans l'interface).
	Notify bool
	// Checkpoints est le magasin des copies d'avant écriture (nil : aucune
	// restauration de contenu possible ; les suppressions restent possibles).
	Checkpoints *checkpoint.Store
	// Profile est le profil de conformité actif, recopié dans SessionStarted
	// (le contrôle avant envoi est porté par Outbound).
	Profile profile.Name
	// Policy est la politique d'organisation active (nil : aucune), recopiée
	// dans SessionStarted ; Outbound en revérifie les hôtes avant chaque envoi.
	Policy *orgpolicy.Policy
	// Allowlists sont les listes blanches de fournisseurs actives (globale,
	// projet) ; Outbound en revérifie l'hôte avant chaque envoi.
	Allowlists []hostmatch.List
	// ShellSandboxed indique que l'outil shell isole ses commandes : les
	// demandes qui le concernent portent alors Sandboxed (option « Autoriser
	// avec réseau »).
	ShellSandboxed bool
	// Sandbox décrit l'isolation de la session, recopiée dans SessionStarted
	// (nil : omise).
	Sandbox *event.SandboxInfo
	// Classify porte les règles de classification (chemins, marquages) ; les
	// marquages intégrés s'appliquent même avec des règles vides.
	Classify classify.Rules
	// Router ordonne les fournisseurs candidats de chaque envoi (nil : le
	// seul Provider, sans repli ni fournisseur local). Avec un fournisseur
	// local (Router.Local()), un contenu classé rend la session sensible au
	// lieu d'être refusé ou retenu. Provider reste le fournisseur courant
	// (SessionStarted).
	Router *routing.Router
}

// Reply répond à un PermissionRequested. Network accorde le réseau à la
// commande shell isolée pour cet appel seulement : Remember ne le mémorise
// jamais.
type Reply struct{ Allow, Remember, Network bool }

// Engine exécute les tours d'agent. Il est sûr en usage concurrent ; les tours
// d'une même session sont sérialisés.
type Engine struct {
	d Deps
	// router : Deps.Router, ou un routeur sur le seul Deps.Provider.
	router *routing.Router
	// firstChunkTimeout : délai avant le premier chunk (routing.FirstChunkTimeout ;
	// réduit par les tests).
	firstChunkTimeout time.Duration
	// tools est le jeu d'outils du tour principal : d.Tools plus agent.
	tools *tools.Registry

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
	// startCtx : contexte des hooks SessionStart (startup, resume) en attente,
	// joint au prochain message utilisateur accepté (un prompt bloqué ne le
	// consomme pas). La note d'un rewind code seul y est jointe.
	startCtx string
	// rw : points, écritures et appels non annulables de la session (rewind).
	rw rewindState

	// classMu protège sensitive et sensitiveReason : les tours de hooks
	// agent concurrents partagent la session.
	classMu sync.Mutex
	// sensitive : la session a touché un contenu classé (définitif, partagé
	// avec les sous-agents, restauré depuis SessionClassified).
	sensitive       bool
	sensitiveReason string
	// provider : identifiant du dernier fournisseur retenu (ProviderSelected),
	// protégé par classMu.
	provider string
}

// isSensitive indique que la session est sensible.
func (s *session) isSensitive() bool {
	s.classMu.Lock()
	defer s.classMu.Unlock()
	return s.sensitive
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
	if d.BasePrompt == "" {
		d.BasePrompt = d.SystemPrompt
	}
	if d.Lang == "" {
		d.Lang = i18n.FR
	}
	// Deps.Profile est la seule source du profil : Outbound le revérifie
	// avant chaque envoi.
	if d.Outbound != nil {
		d.Outbound.SetProfile(d.Profile)
		d.Outbound.SetHostPolicy(d.Policy)
		d.Outbound.SetAllowlists(d.Allowlists)
	}
	reg := d.Tools
	if d.Agents != nil {
		reg = reg.With(agentTool{})
	}
	router := d.Router
	if router == nil && d.Provider != nil {
		router = routing.New(routing.Candidate{ID: d.Provider.Info().ID, Provider: d.Provider}, nil, nil)
	}
	return &Engine{
		d: d, tools: reg, router: router, firstChunkTimeout: routing.FirstChunkTimeout,
		sessions: map[string]*session{}, pending: map[string]chan Reply{},
	}
}

// initialProvider est le fournisseur retenu d'une nouvelle session : sans
// routage configuré, le fournisseur courant (déjà dans SessionStarted, aucun
// ProviderSelected tant qu'il ne change pas) ; avec, aucun (ProviderSelected
// dès le premier envoi).
func (e *Engine) initialProvider() string {
	if e.d.Router != nil || e.d.Provider == nil {
		return ""
	}
	return e.d.Provider.Info().ID
}

// hasLocal indique qu'un fournisseur local est disponible.
func (e *Engine) hasLocal() bool {
	if e.router == nil {
		return false
	}
	_, ok := e.router.Local()
	return ok
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
	t := &turn{e: e, s: s, ctx: ctx, h: &s.history, tools: e.tools, deltas: true}
	return t.run(in)
}

// EndSession exécute les hooks SessionEnd (reason) d'une session démarrée dans
// ce processus ; sans effet sinon. Le délai de chaque hook est plafonné à 5 s
// (hooks.Run). Un continue:false est sans effet. Aucun TurnCompleted n'est émis.
// À appeler après la fin du tour en cours (annulé ou terminé) : si un tour
// tient encore la session, EndSession l'attend au plus jusqu'à la fin de ctx,
// puis renvoie ctx.Err() sans exécuter de hook.
func (e *Engine) EndSession(ctx context.Context, sessionID, reason string) error {
	e.mu.Lock()
	s, ok := e.sessions[sessionID]
	e.mu.Unlock()
	if !ok {
		return nil
	}
	if err := lockCtx(ctx, &s.turn); err != nil {
		return err
	}
	defer s.turn.Unlock()
	if !s.started {
		return nil
	}
	if e.isFailed() {
		return ErrAuditFailure
	}
	// Tour éphémère : mêmes sinks et abonnés ; son stopReason est ignoré.
	t := &turn{e: e, s: s, ctx: ctx, h: &s.history, tools: e.tools}
	_, err := t.runHooks(hooks.SessionEnd, "", map[string]any{"reason": reason})
	return err
}

// lockPoll est l'intervalle entre deux tentatives de lockCtx.
const lockPoll = 10 * time.Millisecond

// lockCtx prend mu, ou renvoie ctx.Err() si ctx se termine avant.
func lockCtx(ctx context.Context, mu *sync.Mutex) error {
	for !mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPoll):
		}
	}
	return nil
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
			provider:   e.initialProvider(),
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
	return e.emitBuild(s, typ, func(string) any { return payload })
}

// emitSeq est emit, et renvoie le seq de l'événement émis (lu sous emitMu :
// sûr même pour les tours de hooks agent concurrents).
func (e *Engine) emitSeq(s *session, typ event.Type, payload any) (uint64, error) {
	var seq uint64
	err := e.emitBuild(s, typ, func(string) any {
		seq = s.seq + 1
		return payload
	})
	return seq, err
}

// emitBuild est emit, avec un payload construit sous emitMu à partir de
// l'identifiant de l'événement émis (RunID d'un HookStarted).
func (e *Engine) emitBuild(s *session, typ event.Type, build func(id string) any) error {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	if e.failed {
		return ErrAuditFailure
	}
	payload := build(fmt.Sprintf("%s-%d", s.id, s.seq+1))
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
