package event

import (
	"encoding/json"

	"github.com/tenon-code-eu/tenon/internal/llm"
)

// SessionStartedPayload opens a session.
type SessionStartedPayload struct {
	Provider    llm.ProviderInfo `json:"provider"`
	ProjectRoot string           `json:"project_root"`
	// Resumed is true when the session was rebuilt from its persisted events.
	Resumed bool `json:"resumed,omitempty"`
	// Instructions lists the project instruction files loaded (e.g. AGENTS.md).
	Instructions []string `json:"instructions,omitempty"`
	// MCP lists the MCP servers active for the session (omitted when none).
	MCP []MCPServerInfo `json:"mcp,omitempty"`
}

// MCPServerInfo describes an active MCP server (values from the configuration).
type MCPServerInfo struct {
	ID           string           `json:"id"`
	Jurisdiction llm.Jurisdiction `json:"jurisdiction"`
	Country      string           `json:"country,omitempty"`
}

// UserMessagePayload carries a user message.
type UserMessagePayload struct {
	// Text is what the model receives.
	Text string `json:"text"`
	// Typed is what the user actually typed; UIs show it instead of Text when set.
	Typed string `json:"typed,omitempty"`
	// Skill is the name of the skill that produced Text, if any.
	Skill string `json:"skill,omitempty"`
	// SkillSHA256 is the hash of the skill file content used, if any.
	SkillSHA256 string `json:"skill_sha256,omitempty"`
}

// ContextCompactedPayload records a context compaction: the first Replaced
// conversation messages (the system prompt excluded) were replaced by one user
// message carrying Summary. Token counts are estimates.
type ContextCompactedPayload struct {
	Summary      string `json:"summary"`
	Replaced     int    `json:"replaced"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
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
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// ToolCallRequestedPayload records a tool call requested by the model.
type ToolCallRequestedPayload struct {
	CallID string          `json:"call_id"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
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
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// PermissionResolvedPayload records the answer to a permission request.
type PermissionResolvedPayload struct {
	PermissionID string `json:"permission_id"`
	Allowed      bool   `json:"allowed"`
	Remember     bool   `json:"remember"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
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
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// McpServerDownPayload reports an MCP server that is unreachable (at start or
// during a call). Error is user-facing text.
type McpServerDownPayload struct {
	Server string `json:"server"`
	Error  string `json:"error"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// JurisdictionWarningPayload warns that a provider lies outside the preferred jurisdiction.
type JurisdictionWarningPayload struct {
	Provider llm.ProviderInfo `json:"provider"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// RedactionAppliedPayload counts secrets redacted before sending.
type RedactionAppliedPayload struct {
	Counts map[string]int `json:"counts"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
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
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// SubagentStartedPayload opens a sub-agent run launched by the "agent" tool call CallID.
type SubagentStartedPayload struct {
	CallID      string `json:"call_id"`
	Agent       string `json:"agent"`
	Description string `json:"description"`
}

// SubagentCompletedPayload closes a sub-agent run. Status is "ok", "error" or "cancelled".
type SubagentCompletedPayload struct {
	CallID     string `json:"call_id"`
	Status     string `json:"status"`
	Iterations int    `json:"iterations"`
}

// WithParent returns a copy of payload (a value type) with ParentCallID set.
// Other payload types, and an empty parentCallID, return payload unchanged.
func WithParent(payload any, parentCallID string) any {
	if parentCallID == "" {
		return payload
	}
	switch p := payload.(type) {
	case AssistantMessagePayload:
		p.ParentCallID = parentCallID
		return p
	case ToolCallRequestedPayload:
		p.ParentCallID = parentCallID
		return p
	case ToolResultPayload:
		p.ParentCallID = parentCallID
		return p
	case PermissionRequestedPayload:
		p.ParentCallID = parentCallID
		return p
	case PermissionResolvedPayload:
		p.ParentCallID = parentCallID
		return p
	case ContextCompactedPayload:
		p.ParentCallID = parentCallID
		return p
	case RedactionAppliedPayload:
		p.ParentCallID = parentCallID
		return p
	case JurisdictionWarningPayload:
		p.ParentCallID = parentCallID
		return p
	case ErrorPayload:
		p.ParentCallID = parentCallID
		return p
	case McpServerDownPayload:
		p.ParentCallID = parentCallID
		return p
	}
	return payload
}
