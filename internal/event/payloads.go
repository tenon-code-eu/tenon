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
	// Profile est le profil de conformité actif (omis sans profil).
	Profile string `json:"profile,omitempty"`
	// Policy identifie la politique d'organisation active (omis sans politique).
	Policy *PolicyRef `json:"policy,omitempty"`
	// Sandbox décrit l'isolation des commandes de la session (omis si inconnue).
	Sandbox *SandboxInfo `json:"sandbox,omitempty"`
}

// SandboxInfo décrit l'isolation des commandes : mode effectif ("auto",
// "required", "off"), mécanisme ("landlock", "sandbox-exec", "none"),
// isolation réseau ("full", "tcp", "none") et raison d'une indisponibilité
// ou d'une isolation partielle.
type SandboxInfo struct {
	Mode      string `json:"mode"`
	Mechanism string `json:"mechanism"`
	Network   string `json:"network"`
	Reason    string `json:"reason,omitempty"`
}

// PolicyRef identifie la politique d'organisation appliquée à la session.
type PolicyRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	KeyID   string `json:"key_id"`
}

// MCPServerInfo describes an active MCP server (values from the configuration).
type MCPServerInfo struct {
	ID           string           `json:"id"`
	Jurisdiction llm.Jurisdiction `json:"jurisdiction"`
	Country      string           `json:"country,omitempty"`
	// Sandbox records how a stdio server was started (omitted for http).
	Sandbox *SandboxUse `json:"sandbox,omitempty"`
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
	// HookContext is the additional context a hook returned for the model, if any.
	HookContext string `json:"hook_context,omitempty"`
	// Blocked is true when a hook blocked the message (feedback message).
	Blocked bool `json:"blocked,omitempty"`
	// Hook names the event that produced a feedback message: "Stop" or "SubagentStop".
	Hook string `json:"hook,omitempty"`
	// ParentCallID is the call_id of the "agent" tool call whose sub-agent
	// received this message (SubagentStop feedback); empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// ContextCompactedPayload records a context compaction: the first Replaced
// conversation messages (the system prompt excluded) were replaced by one user
// message carrying Summary. Token counts are estimates.
type ContextCompactedPayload struct {
	Summary      string `json:"summary"`
	Replaced     int    `json:"replaced"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`
	// HookContext is the additional context the SessionStart hooks (source
	// "compact") returned after the compaction, if any.
	HookContext string `json:"hook_context,omitempty"`
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
	// HookReason is the reason a PreToolUse hook gave for asking, if any.
	HookReason string `json:"hook_reason,omitempty"`
	// Args holds the arguments that will run when a PreToolUse hook's
	// updatedInput changed them (omitted otherwise): UIs preview these
	// instead of the model's original arguments.
	Args json.RawMessage `json:"args,omitempty"`
	// Sandboxed is true when the shell command will run confined: UIs then
	// offer "Allow with network" besides "Allow".
	Sandboxed bool `json:"sandboxed,omitempty"`
	// RuleNetwork is true when the matched permission rule grants network:
	// approving the request then runs the command with network access.
	RuleNetwork bool `json:"rule_network,omitempty"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// PermissionResolvedPayload records the answer to a permission request.
type PermissionResolvedPayload struct {
	PermissionID string `json:"permission_id"`
	Allowed      bool   `json:"allowed"`
	Remember     bool   `json:"remember"`
	// Network is true when the user allowed the confined command with network
	// access ("Allow with network"); never remembered.
	Network bool `json:"network,omitempty"`
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
	// NoCheckpoint is true when the call changed files but no checkpoint could be saved for them.
	NoCheckpoint bool `json:"no_checkpoint,omitempty"`
	// Untracked is true when the call may have changed files outside the checkpoint tracking (e.g. bash).
	Untracked bool `json:"untracked,omitempty"`
	// Sandbox records how a shell command ran (omitted for other tools).
	Sandbox *SandboxUse `json:"sandbox,omitempty"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// SandboxUse records how a command ran.
type SandboxUse struct {
	// Confined: the command ran confined by the system sandbox.
	Confined bool `json:"confined"`
	// Network: the command had network access (always true when not confined).
	Network bool `json:"network"`
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

// SessionClassifiedPayload records that the session became sensitive (once,
// permanently). Reason is "path" or "marker"; Source is "tool", "shell",
// "mcp", "user" or "outbound". CallID is the tool call that triggered it,
// Path the classified path (relative to the project root when inside it) and
// Marker the classification marker as configured; empty fields are omitted.
// Session-wide, so it carries no ParentCallID even when a sub-agent triggers it.
type SessionClassifiedPayload struct {
	Reason string `json:"reason"`
	Source string `json:"source"`
	CallID string `json:"call_id,omitempty"`
	Path   string `json:"path,omitempty"`
	Marker string `json:"marker,omitempty"`
}

// ProviderSelectedPayload records a change of the provider that receives the
// model requests (including the first send of a session with routing).
// Reason is "default", "fallback" (an earlier candidate failed or was refused
// during this send) or "sensitive" (the session is sensitive: local provider
// only). Error is the failure of the previous candidate on a fallback.
// Emitted before the request is sent. Session-wide: no ParentCallID.
type ProviderSelectedPayload struct {
	Provider llm.ProviderInfo `json:"provider"`
	Reason   string           `json:"reason"`
	Error    string           `json:"error,omitempty"`
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

// HookStartedPayload opens a hook run. RunID is the ID of this event
// ("<session>-<seq>"); HookCompleted carries it back to pair the two events.
// Type is "command", "prompt" or "agent". Command and Prompt are truncated
// to 4096 bytes.
type HookStartedPayload struct {
	RunID   string `json:"run_id"`
	HookID  string `json:"hook_id"`
	Event   string `json:"event"`
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	CallID  string `json:"call_id,omitempty"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// HookCompletedPayload closes a hook run started by the HookStarted event RunID.
// Status is "ok", "blocked", "error", "timeout" or "cancelled". Stdout and
// Stderr are truncated to 4096 bytes.
type HookCompletedPayload struct {
	RunID         string          `json:"run_id"`
	HookID        string          `json:"hook_id"`
	Event         string          `json:"event"`
	Status        string          `json:"status"`
	ExitCode      *int            `json:"exit_code,omitempty"`
	Decision      string          `json:"decision,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	UpdatedInput  json.RawMessage `json:"updated_input,omitempty"`
	SystemMessage string          `json:"system_message,omitempty"`
	Stop          bool            `json:"stop,omitempty"`
	StopReason    string          `json:"stop_reason,omitempty"`
	Error         string          `json:"error,omitempty"`
	DurationMS    int64           `json:"duration_ms"`
	Stdout        string          `json:"stdout,omitempty"`
	Stderr        string          `json:"stderr,omitempty"`
	CallID        string          `json:"call_id,omitempty"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run; empty for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// RewoundPayload records a rewind to the checkpoint taken before event ToSeq.
// Mode is the rewind mode (e.g. "code"). Files lists the per-file outcome;
// UntrackedCalls counts tool calls whose file changes were not tracked.
// Emitted by the main agent only, so it carries no ParentCallID.
type RewoundPayload struct {
	ToSeq          uint64        `json:"to_seq"`
	Mode           string        `json:"mode"`
	Files          []RewoundFile `json:"files,omitempty"`
	UntrackedCalls int           `json:"untracked_calls,omitempty"`
	Note           string        `json:"note,omitempty"`
}

// RewoundFile is the outcome of restoring one file during a rewind. Action
// describes what was done to the file and Status its result. Hash is the
// content hash after the rewind, empty if the file was deleted or unchanged.
type RewoundFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Status string `json:"status"`
	Hash   string `json:"hash,omitempty"`
	Error  string `json:"error,omitempty"`
}

// WithParent returns a copy of payload (a value type) with ParentCallID set.
// Other payload types, and an empty parentCallID, return payload unchanged.
func WithParent(payload any, parentCallID string) any {
	if parentCallID == "" {
		return payload
	}
	switch p := payload.(type) {
	case UserMessagePayload:
		p.ParentCallID = parentCallID
		return p
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
	case HookStartedPayload:
		p.ParentCallID = parentCallID
		return p
	case HookCompletedPayload:
		p.ParentCallID = parentCallID
		return p
	}
	return payload
}
