package event

import (
	"encoding/json"

	"codeberg.org/tenon/tenon/internal/llm"
)

// SessionStartedPayload opens a session.
type SessionStartedPayload struct {
	Provider    llm.ProviderInfo `json:"provider"`
	ProjectRoot string           `json:"project_root"`
	// Resumed is true when the session was rebuilt from its persisted events.
	Resumed bool `json:"resumed,omitempty"`
	// Instructions lists the project instruction files loaded (e.g. AGENTS.md).
	Instructions []string `json:"instructions,omitempty"`
}

// UserMessagePayload carries text typed by the user.
type UserMessagePayload struct {
	Text string `json:"text"`
}

// ContextCompactedPayload records a context compaction: the first Replaced
// conversation messages (the system prompt excluded) were replaced by one user
// message carrying Summary. Token counts are estimates.
type ContextCompactedPayload struct {
	Summary      string `json:"summary"`
	Replaced     int    `json:"replaced"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`
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

// ToolResultPayload records the outcome of a tool run. Denied is "policy"
// when the permission engine or a policy guard refused the call, "user" when
// the user refused a permission request, and empty otherwise.
type ToolResultPayload struct {
	CallID     string `json:"call_id"`
	Tool       string `json:"tool"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
	Path       string `json:"path"`
	HashBefore string `json:"hash_before"`
	HashAfter  string `json:"hash_after"`
	Denied     string `json:"denied,omitempty"`
}

// McpServerDownPayload reports an MCP server that is unreachable (at start or
// during a call). Error is user-facing text.
type McpServerDownPayload struct {
	Server string `json:"server"`
	Error  string `json:"error"`
}

// JurisdictionWarningPayload warns that a provider lies outside the preferred jurisdiction.
type JurisdictionWarningPayload struct {
	Provider llm.ProviderInfo `json:"provider"`
}

// RedactionAppliedPayload counts secrets redacted before sending.
type RedactionAppliedPayload struct {
	Counts map[string]int `json:"counts"`
}

// TurnCompletedPayload ends a turn. Reason is "done", "max_iterations", "incomplete", "blocked", "cancelled" or "context_too_large".
type TurnCompletedPayload struct {
	Iterations int    `json:"iterations"`
	Reason     string `json:"reason"`
}

// ErrorPayload reports a failure.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
