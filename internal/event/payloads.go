package event

import (
	"encoding/json"

	"codeberg.org/tenon/tenon/internal/llm"
)

// SessionStartedPayload opens a session.
type SessionStartedPayload struct {
	Provider    llm.ProviderInfo `json:"provider"`
	ProjectRoot string           `json:"project_root"`
}

// UserMessagePayload carries text typed by the user.
type UserMessagePayload struct {
	Text string `json:"text"`
}

// AssistantDeltaPayload carries a streamed fragment of an assistant reply.
type AssistantDeltaPayload struct {
	Text string `json:"text"`
}

// AssistantMessagePayload carries a complete assistant reply.
type AssistantMessagePayload struct {
	Text       string         `json:"text"`
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	Incomplete bool           `json:"incomplete"`
}

// ToolCallRequestedPayload records a tool call requested by the model.
type ToolCallRequestedPayload struct {
	CallID string          `json:"call_id"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
}

// PermissionRequestedPayload asks for approval of a tool or jurisdiction action.
// Kind is "tool" or "jurisdiction".
type PermissionRequestedPayload struct {
	PermissionID string            `json:"permission_id"`
	Kind         string            `json:"kind"`
	Tool         string            `json:"tool"`
	Paths        []string          `json:"paths,omitempty"`
	Command      string            `json:"command"`
	Provider     *llm.ProviderInfo `json:"provider,omitempty"`
}

// PermissionResolvedPayload records the answer to a permission request.
type PermissionResolvedPayload struct {
	PermissionID string `json:"permission_id"`
	Allowed      bool   `json:"allowed"`
	Remember     bool   `json:"remember"`
}

// ToolResultPayload records the outcome of a tool run.
type ToolResultPayload struct {
	CallID     string `json:"call_id"`
	Tool       string `json:"tool"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
	Path       string `json:"path"`
	HashBefore string `json:"hash_before"`
	HashAfter  string `json:"hash_after"`
}

// JurisdictionWarningPayload warns that a provider lies outside the preferred jurisdiction.
type JurisdictionWarningPayload struct {
	Provider llm.ProviderInfo `json:"provider"`
}

// RedactionAppliedPayload counts secrets redacted before sending.
type RedactionAppliedPayload struct {
	Counts map[string]int `json:"counts"`
}

// TurnCompletedPayload ends a turn. Reason is "done", "max_iterations", "incomplete", "blocked" or "cancelled".
type TurnCompletedPayload struct {
	Iterations int    `json:"iterations"`
	Reason     string `json:"reason"`
}

// ErrorPayload reports a failure.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
