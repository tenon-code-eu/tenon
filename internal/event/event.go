// Package event defines the session event log model.
package event

import (
	"encoding/json"
	"fmt"
	"time"
)

// Type names the kind of an event. Its value is the constant name.
type Type string

const (
	SessionStarted      Type = "SessionStarted"
	UserMessage         Type = "UserMessage"
	ContextCompacted    Type = "ContextCompacted"
	JurisdictionWarning Type = "JurisdictionWarning"
	RedactionApplied    Type = "RedactionApplied"
	AssistantDelta      Type = "AssistantDelta"
	AssistantMessage    Type = "AssistantMessage"
	ToolCallRequested   Type = "ToolCallRequested"
	PermissionRequested Type = "PermissionRequested"
	PermissionResolved  Type = "PermissionResolved"
	ToolResult          Type = "ToolResult"
	McpServerDown       Type = "McpServerDown"
	TurnCompleted       Type = "TurnCompleted"
	Error               Type = "Error"
	SubagentStarted     Type = "SubagentStarted"
	SubagentCompleted   Type = "SubagentCompleted"
	HookStarted         Type = "HookStarted"
	HookCompleted       Type = "HookCompleted"
	Rewound             Type = "Rewound"
	SessionClassified   Type = "SessionClassified"
)

// Event is one entry of a session log.
type Event struct {
	ID      string          `json:"id"` // "<session>-<seq>"
	Session string          `json:"session"`
	Seq     uint64          `json:"seq"`
	TS      time.Time       `json:"ts"`
	Type    Type            `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// New builds an event, marshalling payload into Payload.
func New(session string, seq uint64, ts time.Time, t Type, payload any) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal %s payload: %w", t, err)
	}
	return Event{
		ID:      fmt.Sprintf("%s-%d", session, seq),
		Session: session,
		Seq:     seq,
		TS:      ts,
		Type:    t,
		Payload: raw,
	}, nil
}

// Sink receives events as they are produced.
type Sink interface {
	Write(e Event) error
}
