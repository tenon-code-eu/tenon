package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/classify"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/routing"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// reMarker reconnaît un marqueur de masquage (voir policy.Redactor).
var reMarker = regexp.MustCompile(`\[[A-Z_]+_\d+\]`)

// turn porte l'état d'un tour ; s.turn est tenu pendant toute sa durée.
// Un sous-agent utilise un second turn : historique local, outils restreints,
// parent = identifiant de l'appel d'outil agent, sans deltas.
type turn struct {
	e      *Engine
	s      *session
	ctx    context.Context
	h      *[]llm.Message  // historique du tour (session : &s.history)
	tools  *tools.Registry // outils proposés et exécutables (peut être nil)
	parent string          // call_id de l'appel agent parent ("" pour le tour principal)
	deltas bool            // émettre AssistantDelta
	// noHooks : tour d'un hook agent, dont les appels ne déclenchent aucun hook.
	noHooks bool
	// stopReason : non vide après un hook continue:false ; le tour s'arrête
	// après l'outil en cours (ou au prochain point de contrôle de la boucle)
	// avec cette raison.
	stopReason string
	// jurSkipped : messages des candidats sautés faute d'accord de
	// juridiction pendant l'envoi en cours (remis à zéro par sendWith).
	jurSkipped []string
	// remote : la dernière réponse reçue par sendWith vient d'un fournisseur
	// non local (llm.Message.Remote des messages qu'elle produit).
	remote bool
	// agent : nom de l'agent d'un sous-agent (entrée de SubagentStop).
	agent string
	// outer : contexte du tour qui exécute le hook, pour la requête d'un hook
	// prompt ou le tour d'un hook agent (ctx porte alors le délai du hook) ;
	// nil sinon.
	outer context.Context
	// shaped : derniers ensembles émis par ContextShaped pour un tour
	// imbriqué (sous-agent, hook agent) ; le tour principal utilise
	// session.shaped.
	shaped Shaped
	// noShape : requête hors conversation (évaluation d'un hook prompt) ;
	// la vue s'applique sans lire ni écrire shaped, sans ContextShaped.
	noShape bool
}

// loopResult résume la fin de loop. Reason vaut l'une des raisons de
// TurnCompleted, ou "error" quand ProvErr n'est pas nil.
type loopResult struct {
	Iterations int
	Reason     string
	Text       string // dernier texte d'assistant non vide (même incomplet)
	Final      string // texte du dernier message d'assistant (raison done)
	ProvErr    error
}

func (t *turn) emit(typ event.Type, payload any) error {
	return t.e.emit(t.s, typ, event.WithParent(payload, t.parent))
}

// emitSeq est emit, et renvoie le seq de l'événement émis.
func (t *turn) emitSeq(typ event.Type, payload any) (uint64, error) {
	return t.e.emitSeq(t.s, typ, event.WithParent(payload, t.parent))
}

func (t *turn) done(iter int, reason string) error {
	return t.emit(event.TurnCompleted, event.TurnCompletedPayload{Iterations: iter, Reason: reason})
}

func (t *turn) run(in Input) error {
	d := t.e.d
	info := d.Provider.Info()
	if !t.s.started {
		started := event.SessionStartedPayload{
			Provider: info, ProjectRoot: d.ProjectRoot, Resumed: t.s.resumed, Instructions: d.Instructions,
			MCP: d.MCP, Profile: string(d.Profile), Sandbox: d.Sandbox, Routing: d.Routing,
		}
		if d.Policy != nil {
			started.Policy = &event.PolicyRef{ID: d.Policy.ID, Version: d.Policy.Version, KeyID: d.Policy.KeyID}
		}
		if err := t.emit(event.SessionStarted, started); err != nil {
			return err
		}
		t.s.started = true
		for _, u := range d.UnavailableMCP {
			if err := t.emit(event.McpServerDown, u); err != nil {
				return err
			}
		}
		source := "startup"
		if t.s.resumed {
			source = "resume"
		}
		out, err := t.runHooks(hooks.SessionStart, "", map[string]any{"source": source}, source)
		if err != nil {
			return t.hookFailed(err)
		}
		// Un contexte restauré (prompt bloqué avant la reprise) reste en tête.
		t.s.startCtx = joinContext(t.s.startCtx, out.Context)
	}
	prompt := in.Typed
	if prompt == "" {
		prompt = in.Text
	}
	// Classification : avant les hooks UserPromptSubmit (un hook prompt
	// enverrait le texte au fournisseur).
	if blocked, err := t.checkUser(in.Text, in.Typed); blocked || err != nil {
		return err
	}
	out, err := t.runHooks(hooks.UserPromptSubmit, "", map[string]any{"prompt": prompt})
	if err != nil {
		return t.hookFailed(err)
	}
	um := event.UserMessagePayload{Text: in.Text, Typed: in.Typed, Skill: in.Skill, SkillSHA256: in.SkillSHA256}
	// Prompt bloqué : ni historique, ni requête ; le message est journalisé
	// comme bloqué, avec le contexte SessionStart encore en attente (pour la
	// reprise).
	if out.Block {
		um.Blocked = true
		um.HookContext = t.s.startCtx
		if err := t.emit(event.UserMessage, um); err != nil {
			return err
		}
		return t.done(0, "blocked_by_hook")
	}
	// Contexte des hooks (SessionStart, UserPromptSubmit) joint au message :
	// un marquage rend la session sensible avec un fournisseur local ; sans,
	// le contexte seul est retenu (classifiedWithheld) et le prompt part.
	hc, err := t.scanHookTextFrom(joinContext(t.s.startCtx, out.Context), sourceUser)
	if err != nil {
		return err
	}
	um.HookContext = hc
	t.s.startCtx = ""
	histLen := len(*t.h)
	*t.h = append(*t.h, llm.Message{Role: llm.User, Content: withHookContext(in.Text, um.HookContext)})
	seq, err := t.emitSeq(event.UserMessage, um)
	if err != nil {
		return err
	}
	// Point de restauration : message principal accepté (run ne sert qu'au
	// tour principal).
	t.s.rw.addPoint(seq, prompt, histLen)
	// continue:false de SessionStart ou UserPromptSubmit : fin avant toute requête.
	if t.stopReason != "" {
		return t.done(0, t.stopReason)
	}

	res, err := t.loop()
	if res.Reason == "cancelled" {
		t.cancelled(res.Iterations) // au mieux
		return err
	}
	if err != nil {
		return err
	}
	if res.ProvErr != nil {
		return res.ProvErr
	}
	return t.done(res.Iterations, res.Reason)
}

// hookFailed traite l'erreur d'un hook de run avant la boucle : après une
// annulation, le tour est clos (au mieux) avec la raison cancelled.
func (t *turn) hookFailed(err error) error {
	if t.ctx.Err() != nil {
		t.cancelled(0)
	}
	return err
}

// loop exécute les itérations du tour sur *t.h. Il n'émet pas TurnCompleted :
// l'appelant le fait à partir du résultat. Une erreur du fournisseur est
// signalée par un événement Error puis par ProvErr ; l'error renvoyée est
// fatale (audit, annulation du contexte).
func (t *turn) loop() (loopResult, error) {
	d := t.e.d
	var last, final string
	res := func(iter int, reason string) loopResult {
		return loopResult{Iterations: iter, Reason: reason, Text: last, Final: final}
	}
	// stopActive : le tour a déjà été relancé par un hook Stop/SubagentStop.
	stopActive := false
	for iter := 1; iter <= d.MaxIterations; iter++ {
		var defs []llm.ToolDef
		if t.tools != nil {
			defs = t.tools.Defs()
		}
		if stop, reason, err := t.compactOrStop(defs, false, iter-1); stop || err != nil {
			return res(iter-1, reason), err
		}
		// continue:false d'un hook de compaction (PreCompact, SessionStart).
		if t.stopReason != "" {
			return res(iter-1, t.stopReason), nil
		}

		var (
			text      string
			calls     []llm.ToolCall
			provErr   error
			err       error
			compacted bool
		)
		for {
			text, calls, provErr, err = t.send(llm.Request{Messages: *t.h, Tools: defs}, t.deltas)
			if err != nil {
				return res(iter, ""), err
			}
			if errors.Is(provErr, errNotSent) {
				// continue:false d'un hook Notification (demande de juridiction).
				if t.stopReason != "" {
					return res(iter-1, t.stopReason), nil
				}
				return res(iter-1, "blocked"), nil
			}
			// Contexte trop long pour le fournisseur : une compaction forcée,
			// puis une seule nouvelle tentative.
			if compacted || !errors.Is(provErr, provider.ErrContextLength) || t.ctx.Err() != nil {
				break
			}
			compacted = true
			if stop, reason, err := t.compactOrStop(defs, true, iter); stop || err != nil {
				return res(iter, reason), err
			}
			if t.stopReason != "" {
				return res(iter, t.stopReason), nil
			}
		}
		if provErr != nil {
			if cerr := t.ctx.Err(); cerr != nil {
				return res(iter, "cancelled"), cerr
			}
			if errors.Is(provErr, provider.ErrIncomplete) {
				if text != "" {
					*t.h = append(*t.h, llm.Message{Role: llm.Assistant, Content: text, Remote: t.remote})
					last = text
				}
				if err := t.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: text, Incomplete: true}); err != nil {
					return res(iter, ""), err
				}
				return res(iter, "incomplete"), nil
			}
			if err := t.emit(event.Error, event.ErrorPayload{Code: "provider", Message: provErr.Error()}); err != nil {
				return res(iter, ""), err
			}
			r := res(iter, "error")
			r.ProvErr = provErr
			return r, nil
		}

		*t.h = append(*t.h, llm.Message{Role: llm.Assistant, Content: text, ToolCalls: calls, Remote: t.remote})
		if text != "" {
			last = text
		}
		if err := t.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: text, ToolCalls: safeCalls(calls)}); err != nil {
			t.closeCalls(calls, err)
			return res(iter, ""), err
		}
		if len(calls) == 0 {
			final = text
			// Annulation arrivée avec la fin du flux : pas de hook Stop.
			if cerr := t.ctx.Err(); cerr != nil {
				return res(iter, "cancelled"), cerr
			}
			// Stop / SubagentStop : seulement pour une fin done. Un blocage
			// relance la boucle (l'itération compte pour MaxIterations).
			again, err := t.stopHook(stopActive)
			if err != nil {
				if t.ctx.Err() != nil {
					return res(iter, "cancelled"), err
				}
				return res(iter, ""), err
			}
			if t.stopReason != "" {
				return res(iter, t.stopReason), nil
			}
			if again {
				stopActive = true
				continue
			}
			return res(iter, "done"), nil
		}
		for i, c := range calls {
			tr, err := t.runTool(c)
			if err != nil {
				t.closeCalls(calls[i:], err)
				if t.ctx.Err() != nil {
					return res(iter, "cancelled"), err
				}
				return res(iter, ""), err
			}
			*t.h = append(*t.h, llm.Message{Role: llm.Tool, Content: tr.Content, ToolCallID: c.ID})
			if t.stopReason != "" {
				t.closeCalls(calls[i+1:], errors.New("stopped by hook"))
				return res(iter, t.stopReason), nil
			}
		}
	}
	return res(d.MaxIterations, "max_iterations"), nil
}

// closeCalls ajoute à l'historique une réponse synthétique pour chaque appel
// d'outil resté sans résultat : sans elle, la requête suivante de la session
// contiendrait des tool_calls sans réponse, refusés par les API compatibles
// OpenAI (HTTP 400).
func (t *turn) closeCalls(calls []llm.ToolCall, cause error) {
	for _, c := range calls {
		*t.h = append(*t.h, llm.Message{
			Role: llm.Tool, Content: "cancelled: " + cause.Error(), ToolCallID: c.ID,
		})
	}
}

// cancelled clôt le tour dans le journal après une annulation du contexte.
// Au mieux : une erreur d'émission (audit déjà en échec) est ignorée.
func (t *turn) cancelled(iter int) {
	_ = t.done(iter, "cancelled")
}

// compactOrStop appelle compact. stop indique que la compaction termine le
// tour, avec reason ("cancelled", "blocked", "context_too_large"...) ; err est
// l'erreur à renvoyer. Le tour est clos par l'appelant, avec iter itérations.
func (t *turn) compactOrStop(defs []llm.ToolDef, force bool, iter int) (stop bool, reason string, err error) {
	reason, err = t.compact(defs, force)
	switch {
	case reason == reasonCancelled:
		return true, "cancelled", err
	case err != nil:
		return true, "", err
	case reason != "":
		return true, reason, nil
	}
	return false, "", nil
}

// errNotSent : aucune requête n'est partie. Le tour est bloqué (l'événement
// qui l'explique est déjà émis), ou un hook a demandé l'arrêt (t.stopReason).
var errNotSent = errors.New("request not sent")

// prepared est une requête préparée pour un candidat : messages masqués et
// décomptes de masquage, émis seulement si la requête part vers lui. scan,
// s'il n'est pas nil, remplace msgs pour le contrôle sortant des marquages
// (messages masqués dont le texte de msgs est tiré, rendu compris). raw sont
// les mêmes messages avant masquage, contrôlés eux aussi (un masquage peut
// couper un marquage).
type prepared struct {
	msgs   []llm.Message
	counts []map[string]int
	scan   []llm.Message
	raw    []llm.Message
}

// prepFunc prépare la requête pour le fournisseur info (contrôle sortant).
// ok vaut false si le candidat est refusé (événement déjà émis).
type prepFunc func(info llm.ProviderInfo) (p prepared, ok bool, err error)

// outboundCheck applique les garde-fous d'envoi (profil, politique, liste
// blanche, accord de juridiction) au fournisseur info et renvoie la copie
// masquée de in avec ses décomptes. ok vaut false si le fournisseur est
// refusé : l'événement du refus est émis, le tour n'échoue pas (le candidat
// suivant est essayé). err est fatale (audit, annulation).
func (t *turn) outboundCheck(info llm.ProviderInfo, in []llm.Message) (msgs []llm.Message, counts map[string]int, ok bool, err error) {
	d := t.e.d
	for asked := false; ; asked = true {
		msgs, counts, err = d.Outbound.Check(info, in)
		if err == nil {
			return msgs, counts, true, nil
		}
		code := "outbound"
		switch {
		case errors.Is(err, profile.ErrProfile):
			code = "profile"
		case errors.Is(err, orgpolicy.ErrPolicy):
			code = "policy"
		case errors.Is(err, policy.ErrAllowlist):
			code = "allowlist"
		case errors.Is(err, policy.ErrJurisdiction):
			code = ""
		}
		if code != "" {
			// Fournisseur refusé : aucun envoi vers lui.
			if eerr := t.emit(event.Error, event.ErrorPayload{Code: code, Message: err.Error()}); eerr != nil {
				return nil, nil, false, eerr
			}
			return nil, nil, false, nil
		}
		if asked {
			return nil, nil, false, nil
		}
		// Accord déjà refusé dans cette session : candidat sauté sans
		// nouvelle demande.
		if t.s.consentRefused(info.ID) {
			t.jurSkipped = append(t.jurSkipped, fmt.Sprintf("provider %s not used: jurisdiction consent was refused earlier in this session", info.ID))
			return nil, nil, false, nil
		}
		p := info
		r, err := t.ask(event.PermissionRequestedPayload{Kind: "jurisdiction", Provider: &p}, false)
		if err != nil {
			return nil, nil, false, err
		}
		if !r.Allow {
			t.s.refuseConsent(info.ID)
			t.jurSkipped = append(t.jurSkipped, fmt.Sprintf("provider %s not used: jurisdiction consent refused", info.ID))
			return nil, nil, false, nil
		}
		d.Outbound.Consent(info.ID)
	}
}

// send envoie req (messages non masqués) au premier candidat du routeur qui
// l'accepte, après contrôle sortant puis vue projetée (view) ; voir sendWith.
func (t *turn) send(req llm.Request, deltas bool) (text string, calls []llm.ToolCall, provErr, err error) {
	return t.sendWith(req, deltas, func(info llm.ProviderInfo) (prepared, bool, error) {
		msgs, counts, ok, err := t.outboundCheck(info, req.Messages)
		if err != nil || !ok {
			return prepared{}, ok, err
		}
		// Vue projetée sur les messages masqués réellement envoyés ; le
		// contrôle des marquages porte sur la requête complète (scan, raw).
		out, err := t.shapeRequest(msgs, req.Tools)
		if err != nil {
			return prepared{}, false, err
		}
		return prepared{msgs: out, counts: []map[string]int{counts}, scan: msgs, raw: req.Messages}, true, nil
	})
}

// sendWith essaie les candidats du routeur dans l'ordre (session sensible :
// le fournisseur local seul). Pour chacun : préparation par prep (garde-fous,
// masquage ; un refus passe au suivant), contrôle sortant des marquages avant
// un fournisseur non local (trouvé : session sensible et nouvelle sélection,
// ou blocage sans local), puis envoi. Une erreur réessayable reçue avant tout
// chunk utile (routing.Retryable, délai du premier chunk, armé seulement
// s'il reste un candidat à essayer) passe au candidat suivant ; jamais après
// le premier chunk utile. req.Model est remplacé par le
// modèle du candidat. provErr vaut errNotSent si rien n'est parti (aucun
// candidat accepté, ou arrêt demandé par un hook), sinon l'erreur du dernier
// fournisseur essayé ; err est fatale (audit, annulation).
func (t *turn) sendWith(req llm.Request, deltas bool, prep prepFunc) (text string, calls []llm.ToolCall, provErr, err error) {
	if t.e.router == nil {
		return "", nil, nil, errors.New("no provider")
	}
	stop := t.stopReason
	t.jurSkipped = nil
	tried := map[string]bool{}
	var failed error // dernière erreur réessayable de cet envoi
	for {
		sens := t.s.isSensitive()
		cands := t.e.router.Candidates(sens)
		if sens && len(cands) == 0 {
			// Session sensible sans fournisseur local : aucun envoi.
			if _, err := t.blockSensitive(); err != nil {
				return "", nil, nil, err
			}
			return "", nil, errNotSent, nil
		}
		restart := false
		for i, c := range cands {
			if tried[c.ID] {
				continue
			}
			info := c.Provider.Info()
			p, ok, err := prep(info)
			if err != nil {
				return "", nil, nil, err
			}
			// continue:false d'un hook Notification (demande de juridiction).
			if t.stopReason != stop {
				return "", nil, errNotSent, nil
			}
			if !ok {
				tried[c.ID] = true
				continue
			}
			if info.Jurisdiction != llm.Local {
				scan := p.scan
				if scan == nil {
					scan = p.msgs
				}
				m, found := requestMarker(t.e.d.Classify, p.raw)
				if !found {
					m, found = requestMarker(t.e.d.Classify, scan)
				}
				if found {
					if !t.e.hasLocal() {
						msg := fmt.Sprintf("classification marker in the request (%s): %s; use /rewind to remove it from the conversation", m, classifiedRefused)
						if err := t.emit(event.Error, event.ErrorPayload{Code: deniedClassification, Message: msg}); err != nil {
							return "", nil, nil, err
						}
						return "", nil, errNotSent, nil
					}
					if err := t.classify("marker", sourceOutbound, "", "", m); err != nil {
						return "", nil, nil, err
					}
					restart = true
					break
				}
			}
			r := req
			r.Model = info.Model
			r.Messages = p.msgs
			res, err := t.try(c, info, r, p.counts, i > 0, failed, deltas, laterCandidate(cands[i+1:], tried))
			if err != nil {
				return "", nil, nil, err
			}
			if !res.sent {
				// La session est devenue sensible entre la sélection et l'envoi.
				restart = true
				break
			}
			tried[c.ID] = true
			if res.provErr != nil && !res.started && t.ctx.Err() == nil && routing.Retryable(res.provErr) {
				failed = res.provErr
				continue
			}
			t.remote = info.Jurisdiction != llm.Local
			return res.text, res.calls, res.provErr, nil
		}
		if !restart {
			break
		}
	}
	if failed != nil {
		return "", nil, failed, nil
	}
	// Aucun envoi : un candidat sauté faute d'accord de juridiction est
	// expliqué (le refus seul n'émet rien d'autre).
	for _, msg := range t.jurSkipped {
		if err := t.emit(event.Error, event.ErrorPayload{Code: "jurisdiction", Message: msg}); err != nil {
			return "", nil, nil, err
		}
	}
	return "", nil, errNotSent, nil
}

// laterCandidate indique qu'un candidat de rest n'a pas encore été essayé
// dans cet envoi : seul un repli possible justifie le délai du premier chunk.
func laterCandidate(rest []routing.Candidate, tried map[string]bool) bool {
	for _, c := range rest {
		if !tried[c.ID] {
			return true
		}
	}
	return false
}

// requestMarker cherche un marquage dans le texte de chaque message de la
// requête (contenu et arguments d'appels d'outils). Les appels glob et leurs
// résultats sont exclus : des noms de fichiers ne classent jamais la session.
// Les messages produits par un fournisseur non local (llm.Message.Remote :
// réponses, arguments de leurs appels, résumés) sont exclus aussi : ce texte
// vient du fournisseur distant, le lui renvoyer ne divulgue rien (sans quoi
// un marquage qu'il écrit lui-même bloquerait la session). Les résultats
// d'outils et messages utilisateur restent contrôlés.
func requestMarker(rules classify.Rules, msgs []llm.Message) (string, bool) {
	globs := map[string]bool{}
	for _, m := range msgs {
		if m.Role == llm.Tool && globs[m.ToolCallID] {
			continue
		}
		for _, c := range m.ToolCalls {
			if c.Name == "glob" {
				globs[c.ID] = true
			}
		}
		if m.Remote {
			continue
		}
		if mk, ok := rules.FindMarker(m.Content); ok {
			return mk, true
		}
		for _, c := range m.ToolCalls {
			if c.Name == "glob" {
				continue
			}
			if mk, ok := argsMarker(rules, c.Args); ok {
				return mk, true
			}
		}
	}
	return "", false
}

// tryResult est l'issue d'un envoi à un candidat. sent vaut false si rien
// n'est parti (la session est devenue sensible et le candidat n'est pas
// local) ; started indique qu'un chunk utile a été reçu (texte, appels ou fin
// normale), après quoi aucun repli n'est permis. Un battement de cœur
// (llm.Chunk.Heartbeat) n'est pas un chunk utile.
type tryResult struct {
	text    string
	calls   []llm.ToolCall
	provErr error
	sent    bool
	started bool
}

// try envoie req au candidat c et draine le flux. L'état sensible est relu
// sous classMu, tenu jusqu'au retour de Stream : un SessionClassified émis
// par un tour concurrent (hook agent) précède la vérification, ou suit
// l'envoi. ProviderSelected, l'avertissement de juridiction et les
// décomptes de masquage sont émis juste avant Stream.
//
// Délai du premier chunk : armé seulement si arm est vrai (un candidat
// suivant reste à essayer dans cet envoi : sans repli possible, un
// fournisseur lent n'est jamais coupé), et seulement après le retour de
// Stream (en-têtes reçus, réessais du fournisseur terminés). Tout chunk,
// battement de cœur compris, l'arrête. S'il expire sans chunk, le flux est
// annulé et provErr vaut routing.ErrNoFirstChunk. Un chunk déjà arrivé quand
// le délai expire l'emporte : un flux sain n'est jamais annulé. err est une
// erreur d'émission (fatale).
func (t *turn) try(c routing.Candidate, info llm.ProviderInfo, req llm.Request, counts []map[string]int, fellBack bool, failed error, deltas bool, arm bool) (res tryResult, err error) {
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()

	s := t.s
	s.classMu.Lock()
	if s.sensitive && info.Jurisdiction != llm.Local {
		s.classMu.Unlock()
		return res, nil
	}
	reason := "default"
	switch {
	case s.sensitive:
		reason = "sensitive"
	case fellBack:
		reason = "fallback"
	}
	if err := t.selectProvider(info, reason, failed); err != nil {
		s.classMu.Unlock()
		return res, err
	}
	if err := t.sendNotices(info, counts); err != nil {
		s.classMu.Unlock()
		return res, err
	}
	ch, perr := c.Provider.Stream(ctx, req)
	s.classMu.Unlock()
	res.sent = true
	if perr != nil {
		res.provErr = perr
		return res, nil
	}
	// Délai armé après Stream (et donc après classMu : l'attente d'un envoi
	// concurrent n'en fait pas partie).
	var timeout <-chan time.Time
	if arm {
		timer := time.NewTimer(t.e.firstChunkTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	var sb strings.Builder
	final, timedOut := false, false
	for {
		var ck llm.Chunk
		var ok bool
		select {
		case ck, ok = <-ch:
		case <-timeout:
			timeout = nil
			// Un chunk déjà disponible l'emporte sur le délai.
			select {
			case ck, ok = <-ch:
			default:
				timedOut = true
				cancel()
				continue
			}
		}
		if !ok {
			break
		}
		// Après expiration du délai, le flux annulé est seulement drainé.
		if timedOut {
			continue
		}
		timeout = nil
		if ck.Heartbeat {
			continue
		}
		if !ck.Done || ck.Err == nil {
			res.started = true
		}
		if ck.Text != "" {
			sb.WriteString(ck.Text)
			if err := t.delta(deltas, ck.Text); err != nil {
				// Annule le flux puis le draine en arrière-plan pour libérer
				// le goroutine du fournisseur.
				cancel()
				go func() {
					for range ch {
					}
				}()
				return tryResult{sent: true, started: true}, err
			}
		}
		if ck.Done {
			final = true
			res.provErr = ck.Err
			res.calls = ck.ToolCalls
		}
	}
	switch {
	case timedOut:
		// Un fournisseur annulé par le délai clôt son flux avec l'erreur du
		// contexte (OpenAICompat) : c'est un délai avant le premier chunk,
		// donc un repli possible.
		res = tryResult{sent: true, provErr: routing.ErrNoFirstChunk}
		return res, nil
	case !final && res.provErr == nil:
		res.provErr = provider.ErrIncomplete
	}
	if res.provErr != nil {
		res.calls = nil
	}
	res.text = sb.String()
	return res, nil
}

// selectProvider émet ProviderSelected si le fournisseur retenu change (ou
// au premier envoi d'une session avec routage). classMu tenu.
func (t *turn) selectProvider(info llm.ProviderInfo, reason string, failed error) error {
	s := t.s
	if s.provider == info.ID {
		return nil
	}
	s.provider = info.ID
	p := event.ProviderSelectedPayload{Provider: info, Reason: reason}
	if reason == "fallback" && failed != nil {
		p.Error = failed.Error()
	}
	return t.e.emit(s, event.ProviderSelected, p)
}

// sendNotices émet, pour le fournisseur effectivement utilisé,
// l'avertissement de juridiction (une fois par session) et les décomptes de
// masquage non nuls.
func (t *turn) sendNotices(info llm.ProviderInfo, counts []map[string]int) error {
	if !t.s.warned && info.Jurisdiction != llm.EU && info.Jurisdiction != llm.Local {
		if err := t.emit(event.JurisdictionWarning, event.JurisdictionWarningPayload{Provider: info}); err != nil {
			return err
		}
		t.s.warned = true
	}
	for _, c := range counts {
		total := 0
		for _, n := range c {
			total += n
		}
		if total > 0 {
			if err := t.emit(event.RedactionApplied, event.RedactionAppliedPayload{Counts: c}); err != nil {
				return err
			}
		}
	}
	return nil
}

// delta émet AssistantDelta si emit est vrai (faux pour un résumé).
func (t *turn) delta(emit bool, text string) error {
	if !emit {
		return nil
	}
	return t.emit(event.AssistantDelta, event.AssistantDeltaPayload{Text: text})
}

// ask enregistre la demande, émet PermissionRequested, attend Respond ou
// l'annulation du contexte, puis émet PermissionResolved. ruleNet est le
// drapeau network de la règle retenue : une acceptation l'accorde aussi. Le
// Network de la réponse renvoyée est le réseau effectivement accordé (jamais
// pour une demande hors outil ni pour un refus).
func (t *turn) ask(p event.PermissionRequestedPayload, ruleNet bool) (Reply, error) {
	t.s.permN++
	id := fmt.Sprintf("%s-p%d", t.s.id, t.s.permN)
	p.PermissionID = id
	ch := make(chan Reply, 1)
	e := t.e
	e.mu.Lock()
	e.pending[id] = ch
	e.mu.Unlock()
	forget := func() {
		e.mu.Lock()
		delete(e.pending, id)
		e.mu.Unlock()
	}
	if err := t.emit(event.PermissionRequested, p); err != nil {
		forget()
		return Reply{}, err
	}
	// Notification : à l'ouverture du panneau de validation. Un continue:false
	// arrête le tour au prochain point de contrôle, après la réponse.
	if err := t.notifyPermission(p); err != nil {
		forget()
		return Reply{}, err
	}
	var r Reply
	select {
	case r = <-ch:
	case <-t.ctx.Done():
		forget()
		// Seul le délai du hook a expiré (le tour continue) : la demande est
		// résolue en refus, sans quoi l'interface la garderait affichée.
		// Au mieux : l'erreur de ctx est renvoyée de toute façon.
		if t.outer != nil && t.outer.Err() == nil {
			_ = t.emit(event.PermissionResolved, event.PermissionResolvedPayload{PermissionID: id})
		}
		return Reply{}, t.ctx.Err()
	}
	// Le réseau ne vient que d'une règle shell, ou d'une réponse à une demande
	// pour une commande isolée : jamais pour un autre outil.
	r.Network = r.Allow && p.Kind == "tool" && ((r.Network && p.Sandboxed) || ruleNet)
	if err := t.emit(event.PermissionResolved, event.PermissionResolvedPayload{
		PermissionID: id, Allowed: r.Allow, Remember: r.Remember, Network: r.Network,
	}); err != nil {
		return Reply{}, err
	}
	return r, nil
}

func toolError(format string, a ...any) tools.Result {
	return tools.Result{IsError: true, Content: fmt.Sprintf(format, a...)}
}

// runTool traite un appel d'outil : audit, garde, permissions, exécution,
// résultat. Une error renvoyée met fin au tour (audit, annulation).
func (t *turn) runTool(c llm.ToolCall) (tools.Result, error) {
	if err := t.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{
		CallID: c.ID, Tool: c.Name, Args: safeArgs(c.Args),
	}); err != nil {
		return tools.Result{}, err
	}
	res, denied, ran, args, err := t.decideAndRun(c)
	if err != nil {
		return tools.Result{}, err
	}
	// Classification du résultat complet, avant PostToolUse (un hook prompt
	// l'enverrait au fournisseur), l'enregistrement et l'historique. Un appel
	// non exécuté aussi : la raison d'un refus de hook y est recopiée.
	file := ""
	if ran && c.Name == "read_file" && !res.IsError {
		file = res.Path
	}
	if res.Content, err = t.scanResult(c.Name, c.ID, res.Content, res.Untruncated, file); err != nil {
		return tools.Result{}, err
	}
	res.Untruncated = ""
	// Appel exécuté hors du suivi des copies (shell, MCP write/exec).
	untracked := false
	if ran && res.Denied == "" && t.tools != nil {
		tool, _ := t.tools.Get(c.Name)
		untracked = isUntracked(c.Name, tool)
	}
	if denied == "" {
		denied = res.Denied // refus décidé par l'outil lui-même
	}
	// PostToolUse : seulement après une exécution réelle, jamais après un
	// refus ; son entrée porte les arguments exécutés (updatedInput compris).
	if ran && res.Denied == "" {
		executed := c
		executed.Args = args
		fields := toolFields(executed)
		fields["tool_response"] = map[string]any{"content": res.Content, "is_error": res.IsError}
		out, err := t.runHooks(hooks.PostToolUse, c.ID, fields, hooks.ClaudeName(c.Name), c.Name)
		if err != nil {
			return tools.Result{}, err
		}
		if out.Block {
			res.Content += "\n\nhook feedback: " + out.Reason
		}
		if out.Context != "" {
			res.Content += "\n\n" + out.Context
		}
		// Ajouts des hooks : contrôlés à leur tour.
		if out.Block || out.Context != "" {
			if res.Content, err = t.scanResult(c.Name, c.ID, res.Content, "", ""); err != nil {
				return tools.Result{}, err
			}
		}
	}
	trp := event.ToolResultPayload{
		CallID: c.ID, Tool: c.Name, Content: res.Content, IsError: res.IsError,
		Path: res.Path, HashBefore: res.HashBefore, HashAfter: res.HashAfter,
		Denied: denied, NoCheckpoint: res.NoCheckpoint, Untracked: untracked,
		Sandbox: res.Sandbox,
	}
	seq, err := t.emitSeq(event.ToolResult, trp)
	if err != nil {
		return tools.Result{}, err
	}
	t.s.rw.addResult(seq, trp)
	if res.ServerDown != "" {
		if err := t.emit(event.McpServerDown, event.McpServerDownPayload{
			Server: res.ServerDown, Error: res.Content,
		}); err != nil {
			return tools.Result{}, err
		}
	}
	return res, nil
}

// Valeurs de ToolResultPayload.Denied (deniedHook : hooks.go).
const (
	deniedPolicy = "policy"
	deniedUser   = "user"
)

// checkArgs applique les garde-fous de marqueurs puis Target. ok vaut false
// si l'appel est refusé : res est alors le résultat d'erreur et denied son
// origine (deniedPolicy pour un marqueur, "" pour des arguments invalides).
func checkArgs(tool tools.Tool, name string, args json.RawMessage) (target tools.Target, res tools.Result, denied string, ok bool) {
	// Les marqueurs de masquage ne sont jamais restaurés : les écrire
	// remplacerait la vraie valeur par le marqueur.
	if tool.Risk() == tools.Write {
		if m := findMarker(args); m != "" {
			return target, toolError("redacted content: cannot write a redaction marker (%s)", m), deniedPolicy, false
		}
	}
	// De même pour un outil MCP, quel que soit son risque déclaré : le
	// serveur recevrait le marqueur à la place de la valeur.
	if strings.HasPrefix(name, "mcp__") {
		if m := findMarker(args); m != "" {
			return target, toolError("redacted content: cannot send a redaction marker to an MCP server (%s)", m), deniedPolicy, false
		}
	}
	target, err := tool.Target(args)
	if err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "invalid arguments") {
			msg = "invalid arguments: " + msg
		}
		return target, tools.Result{IsError: true, Content: msg}, "", false
	}
	return target, tools.Result{}, "", true
}

// decideAndRun renvoie aussi l'origine d'un refus (deniedPolicy, deniedUser,
// deniedHook, "" si l'appel n'a pas été refusé), ran, vrai si l'outil a été
// réellement exécuté, et args, les arguments exécutés (updatedInput compris).
func (t *turn) decideAndRun(c llm.ToolCall) (res tools.Result, denied string, ran bool, args json.RawMessage, err error) {
	d := t.e.d
	var tool tools.Tool
	ok := false
	if t.tools != nil {
		tool, ok = t.tools.Get(c.Name)
	}
	if !ok {
		return toolError("unknown tool: %s", c.Name), "", false, nil, nil
	}
	target, bad, denied, ok := checkArgs(tool, c.Name, c.Args)
	if !ok {
		return bad, denied, false, nil, nil
	}

	// PreToolUse : après la validation, avant les permissions.
	out, err := t.runHooks(hooks.PreToolUse, c.ID, toolFields(c), hooks.ClaudeName(c.Name), c.Name)
	if err != nil {
		return tools.Result{}, "", false, nil, err
	}
	switch {
	case out.Stop:
		return toolError("stopped by hook: %s", out.StopReason), deniedHook, false, nil, nil
	case out.Conflict:
		return toolError("conflicting updatedInput from hooks"), deniedHook, false, nil, nil
	case out.Block:
		return toolError("blocked by hook: %s", out.Reason), deniedHook, false, nil, nil
	}
	args = c.Args
	if out.UpdatedInput != nil {
		// Nouvelle entrée : retraduite, revalidée ; c'est elle qui est
		// vérifiée par les permissions puis exécutée.
		args, err = hooks.FromClaude(c.Name, out.UpdatedInput)
		if err != nil {
			return toolError("invalid updatedInput from hook: %s", err), deniedHook, false, nil, nil
		}
		if target, bad, _, ok = checkArgs(tool, c.Name, args); !ok {
			return toolError("invalid updatedInput from hook: %s", bad.Content), deniedHook, false, nil, nil
		}
	}

	// Un allow de hook vaut une règle allow globale : il ne lève qu'un Ask
	// levable (règle ask globale ou défaut), jamais un Deny. Un ask de hook
	// impose la demande, même si les règles autorisent.
	//
	// Réseau de l'appel : celui d'une règle allow retenue sans demande, ou
	// celui accordé par la réponse à la demande. Un Ask levé par un hook
	// n'ouvre jamais le réseau ; une approbation mémorisée n'ouvre que celui
	// de la règle ask (network = true), jamais celui d'une réponse.
	dec, liftable, ruleNet := d.Permissions.DecideNet(policy.Call{Tool: c.Name, Risk: tool.Risk(), Target: target})
	var prompt, network bool
	switch dec {
	case policy.Allow:
		prompt = out.Ask
		network = ruleNet && !prompt
	case policy.Ask:
		prompt = !(out.Allow && liftable && !out.Ask)
	default:
		return toolError("action denied by policy"), deniedPolicy, false, nil, nil
	}
	if prompt {
		key := c.Name + "\x00" + target.Command + "\x00" + strings.Join(target.Paths, "\x00")
		// Une approbation mémorisée ne dispense jamais d'une demande de hook,
		// et la réponse à celle-ci n'est pas mémorisée.
		if out.Ask || !t.s.remembered[key] {
			p := event.PermissionRequestedPayload{
				Kind: "tool", Tool: c.Name, Paths: target.Paths, Command: target.Command,
				Sandboxed: d.ShellSandboxed && c.Name == "shell",
				// Une acceptation simple accorde alors le réseau (ask).
				RuleNetwork: ruleNet,
			}
			if out.Ask {
				p.HookReason = out.Reason
				if p.HookReason == "" {
					p.HookReason = noHookReason
				}
			}
			if out.UpdatedInput != nil && !sameJSON(args, c.Args) {
				p.Args = safeArgs(args)
			}
			r, err := t.ask(p, ruleNet)
			if err != nil {
				return tools.Result{}, "", false, nil, err
			}
			if !r.Allow {
				return toolError("action denied by the user"), deniedUser, false, nil, nil
			}
			// La clé mémorisée ignore la réponse réseau : un appel ultérieur
			// approuvé par elle n'a que le réseau de sa règle.
			if r.Remember && !out.Ask {
				t.s.remembered[key] = true
			}
			network = r.Network
		} else {
			// Approbation mémorisée : la règle ask accorde son réseau.
			network = ruleNet
		}
	}
	// Classification : sur les arguments définitifs, après hooks et
	// permissions, juste avant l'exécution.
	refused, err := t.gateClassified(c, target, args)
	if err != nil {
		return tools.Result{}, "", false, nil, err
	}
	if refused {
		return toolError("%s", classifiedRefused), deniedClassification, false, nil, nil
	}
	if _, isAgent := tool.(agentTool); isAgent {
		run := c
		run.Args = args
		res, err = t.runSubagent(run)
	} else {
		res, err = tool.Run(tools.WithNetwork(t.ctx, network), args)
	}
	if err != nil {
		return tools.Result{}, "", false, nil, err
	}
	return res, "", true, args, nil
}

// noHookReason remplace la raison absente d'un ask de hook : la demande reste
// marquée comme venant d'un hook (pas de « oui pour la session »).
const noHookReason = "(no reason given)"

// sameJSON indique que a et b sont deux JSON valides de même valeur.
func sameJSON(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// safeArgs garantit un JSON valide pour les payloads d'événements : des
// arguments invalides sont conservés sous forme de chaîne JSON.
func safeArgs(a json.RawMessage) json.RawMessage {
	if a == nil || json.Valid(a) {
		return a
	}
	b, _ := json.Marshal(string(a))
	return b
}

func safeCalls(cs []llm.ToolCall) []llm.ToolCall {
	if cs == nil {
		return nil
	}
	out := make([]llm.ToolCall, len(cs))
	for i, c := range cs {
		c.Args = safeArgs(c.Args)
		out[i] = c
	}
	return out
}

// findMarker cherche un marqueur de masquage dans les arguments bruts, puis
// dans leurs chaînes décodées (un marqueur peut être écrit avec des
// échappements JSON comme \u005b).
func findMarker(args json.RawMessage) string {
	if m := reMarker.FindString(string(args)); m != "" {
		return m
	}
	var v any
	if json.Unmarshal(args, &v) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case string:
			return reMarker.FindString(x)
		case []any:
			for _, e := range x {
				if m := walk(e); m != "" {
					return m
				}
			}
		case map[string]any:
			for k, e := range x {
				if m := reMarker.FindString(k); m != "" {
					return m
				}
				if m := walk(e); m != "" {
					return m
				}
			}
		}
		return ""
	}
	return walk(v)
}
