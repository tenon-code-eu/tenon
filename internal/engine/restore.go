package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
)

// interruptedMsg répond aux appels d'outil restés sans résultat dans le
// journal d'une session reprise.
const interruptedMsg = "annulé : session interrompue"

// Restore reconstruit l'état en mémoire d'une session à partir de ses
// événements persistés (session.Load), dans l'ordre. À appeler avant tout
// Submit de cette session ; renvoie ErrSessionActive si la session a déjà un
// état dans cet Engine.
//
// L'historique est reconstruit depuis UserMessage, AssistantMessage et
// ToolResult, tels quels (payloads masqués). Tout appel d'outil resté sans
// ToolResult reçoit une réponse synthétique avant le message suivant de
// l'utilisateur ou de l'assistant, et après le dernier événement. Le compteur
// de séquence et celui des demandes de permission reprennent aux plus grandes
// valeurs lues ; les autorisations « pour la session » et l'avertissement de
// juridiction ne sont pas restaurés. Les autres types sont ignorés.
func (e *Engine) Restore(sessionID string, events []event.Event) error {
	s := e.session(sessionID)
	s.turn.Lock()
	defer s.turn.Unlock()
	if s.started || len(s.history) > 1 {
		return fmt.Errorf("%w : %s", ErrSessionActive, sessionID)
	}

	history := s.history
	seq := s.seq
	permN := s.permN
	var pending []llm.ToolCall // appels du dernier message assistant sans résultat
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
		switch ev.Type {
		case event.UserMessage:
			var p event.UserMessagePayload
			if err := unmarshalPayload(ev, &p); err != nil {
				return err
			}
			closePending()
			history = append(history, llm.Message{Role: llm.User, Content: p.Text})
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
	s.seq = seq
	s.permN = permN
	s.started = false
	s.resumed = true
	s.warned = false
	s.remembered = map[string]bool{}
	return nil
}

func unmarshalPayload(ev event.Event, v any) error {
	if err := json.Unmarshal(ev.Payload, v); err != nil {
		return fmt.Errorf("événement %s (%s) illisible : %w", ev.ID, ev.Type, err)
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
