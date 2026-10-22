package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// interruptedMsg répond aux appels d'outil restés sans résultat dans le
// journal d'une session reprise.
const interruptedMsg = "cancelled: session interrupted"

// Restore reconstruit l'état en mémoire d'une session à partir de ses
// événements persistés (session.Load), dans l'ordre. À appeler avant tout
// Submit de cette session ; renvoie ErrSessionActive si la session a déjà un
// état dans cet Engine (y compris après un premier Restore).
//
// L'historique est reconstruit depuis UserMessage, AssistantMessage et
// ToolResult, tels quels (payloads masqués) ; ContextCompacted remplace les
// Replaced premiers messages (prompt système exclu) par le message de résumé,
// et un Replaced hors bornes rend le journal invalide (erreur). Tout appel d'outil resté sans
// ToolResult reçoit une réponse synthétique avant le message suivant de
// l'utilisateur ou de l'assistant, et après le dernier événement. Le compteur
// de séquence et celui des demandes de permission reprennent aux plus grandes
// valeurs lues ; les autorisations « pour la session » et l'avertissement de
// juridiction ne sont pas restaurés. Le contexte des hooks (hook_context) est
// rejoint aux messages utilisateur et au résumé comme pendant le tour ; un
// UserMessage bloqué par un hook est ignoré, un message de retour de hook Stop
// est un message utilisateur comme un autre ; HookStarted et HookCompleted
// sont ignorés. Les événements portant parent_call_id
// (sous-agents) sont ignorés, sauf PermissionRequested pour le compteur et
// ToolResult pour l'état de rewind. L'état de rewind (points, écritures,
// appels non annulables) est rejoué comme en direct ; un Rewound des modes
// conversation et both tronque l'historique à la longueur du point, celui du
// mode code met sa note en attente (jointe au prochain UserMessage accepté).
// Un SessionClassified rend la session sensible (définitivement, quel que soit
// son parent_call_id). Les autres types sont ignorés.
func (e *Engine) Restore(sessionID string, events []event.Event) error {
	s := e.session(sessionID)
	s.turn.Lock()
	defer s.turn.Unlock()
	if s.started || s.resumed || len(s.history) > 1 {
		return fmt.Errorf("%w : %s", ErrSessionActive, sessionID)
	}

	history := s.history
	seq := s.seq
	permN := s.permN
	var rw rewindState         // points, écritures, appels non annulables
	var pending []llm.ToolCall // appels du dernier message assistant sans résultat
	// startCtx : contexte SessionStart encore en attente (dernier prompt bloqué).
	var startCtx string
	// sensitive : la session a été classée (SessionClassified).
	var sensitive bool
	var sensitiveReason string
	closePending := func() {
		for _, c := range pending {
			history = append(history, llm.Message{Role: llm.Tool, Content: interruptedMsg, ToolCallID: c.ID})
		}
		pending = nil
	}

	for _, ev := range events {
		if ev.Session != "" && ev.Session != sessionID {
			continue
		}
		if ev.Seq > seq {
			seq = ev.Seq
		}
		// Les événements d'un sous-agent (parent_call_id) n'entrent pas dans
		// l'historique de l'agent principal ; seul leur compteur de permissions
		// et leur seq comptent.
		var meta struct {
			ParentCallID string `json:"parent_call_id"`
		}
		// Payload illisible : on laisse chaque cas décider (types ignorés tolérés).
		_ = json.Unmarshal(ev.Payload, &meta)
		// Écritures et appels non annulables : agent principal et sous-agents.
		if ev.Type == event.ToolResult {
			var p event.ToolResultPayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			rw.addResult(ev.Seq, p)
		}
		if meta.ParentCallID != "" && ev.Type != event.PermissionRequested && ev.Type != event.SessionClassified {
			continue
		}
		switch ev.Type {
		case event.UserMessage:
			var p event.UserMessagePayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			// Prompt bloqué par un hook : jamais entré dans l'historique ; son
			// hook_context est le contexte SessionStart encore en attente.
			if p.Blocked {
				startCtx = p.HookContext
				continue
			}
			startCtx = ""
			closePending()
			// Point de restauration : message principal accepté (un retour de
			// hook Stop n'en est pas un).
			if p.Hook == "" {
				prompt := p.Typed
				if prompt == "" {
					prompt = p.Text
				}
				rw.addPoint(ev.Seq, prompt, len(history))
			}
			history = append(history, llm.Message{Role: llm.User, Content: withHookContext(p.Text, p.HookContext)})
		case event.AssistantMessage:
			var p event.AssistantMessagePayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			closePending()
			// Comme dans la boucle : une réponse incomplète vide n'entre pas
			// dans l'historique.
			if p.Incomplete && p.Text == "" && len(p.ToolCalls) == 0 {
				continue
			}
			history = append(history, llm.Message{Role: llm.Assistant, Content: p.Text, ToolCalls: p.ToolCalls})
			pending = append([]llm.ToolCall(nil), p.ToolCalls...)
		case event.ToolResult:
			var p event.ToolResultPayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			// Un résultat sans appel en attente rendrait l'historique invalide
			// pour les API compatibles OpenAI : il est ignoré.
			for i, c := range pending {
				if c.ID == p.CallID {
					history = append(history, llm.Message{Role: llm.Tool, Content: p.Content, ToolCallID: p.CallID})
					pending = append(pending[:i], pending[i+1:]...)
					break
				}
			}
		case event.ContextCompacted:
			var p event.ContextCompactedPayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			closePending()
			// history[0] est le prompt système, jamais compacté.
			if p.Replaced < 1 || p.Replaced > len(history)-1 {
				return fmt.Errorf("invalid event %s (%s): %d messages replaced out of %d",
					ev.ID, ev.Type, p.Replaced, len(history)-1)
			}
			rest := history[1+p.Replaced:]
			nh := make([]llm.Message, 0, 2+len(rest))
			nh = append(nh, history[0], summaryMessage(p.Summary, p.HookContext))
			history = append(nh, rest...)
			rw.compacted()
		case event.Rewound:
			var p event.RewoundPayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			// Comme en direct : conversation tronquée à la longueur du point,
			// ou note en attente pour le prochain message accepté.
			if RewindMode(p.Mode) == RewindCode {
				startCtx = joinContext(startCtx, p.Note)
			} else {
				closePending()
				pt, ok := rw.point(p.ToSeq)
				if !ok || pt.HistoryLen < 1 || pt.HistoryLen > len(history) {
					return fmt.Errorf("invalid event %s (%s): unknown checkpoint %d", ev.ID, ev.Type, p.ToSeq)
				}
				history = append(history[:0:0], history[:pt.HistoryLen]...)
			}
			rw.addRewound(ev.Seq, p)
		case event.SessionClassified:
			var p event.SessionClassifiedPayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			// Définitif : le premier SessionClassified fixe la raison.
			if !sensitive {
				sensitive, sensitiveReason = true, p.Reason
			}
		case event.PermissionRequested:
			var p event.PermissionRequestedPayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			if n := permNumber(sessionID, p.PermissionID); n > permN {
				permN = n
			}
		}
	}
	closePending()

	s.history = history
	s.startCtx = startCtx
	s.rw.mu.Lock()
	s.rw.st, s.rw.prompts = rw.st, rw.prompts
	s.rw.mu.Unlock()
	s.seq = seq
	s.permN = permN
	s.started = false
	s.resumed = true
	s.warned = false
	s.remembered = map[string]bool{}
	s.classMu.Lock()
	s.sensitive, s.sensitiveReason = sensitive, sensitiveReason
	s.classMu.Unlock()
	return nil
}

func unmarshalPayload(ev event.Event, v any) error {
	if err := json.Unmarshal(ev.Payload, v); err != nil {
		return fmt.Errorf("unreadable event %s (%s): %w", ev.ID, ev.Type, err)
	}
	return nil
}

// permNumber extrait n d'un identifiant "<session>-p<n>" ; 0 sinon.
func permNumber(sessionID, id string) int {
	rest, ok := strings.CutPrefix(id, sessionID+"-p")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
