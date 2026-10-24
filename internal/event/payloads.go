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
	// Routing décrit le routage configuré ([routing]) : omis sans lui.
	Routing *RoutingInfo `json:"routing,omitempty"`
	// Kind distingue les sessions courtes hors conversation (« cra ») ; omis
	// pour une conversation.
	Kind string `json:"kind,omitempty"`
}

// CRAScanPayload résume un scan CRA (tenon cra scan) : SBOM, composants et
// constats par statut VEX ; DBDate est la date (RFC 3339) de la base OSV.
// Unchecked compte les composants non vérifiés (purl absent, illisible ou de
// type non pris en charge) et Conflicts les conflits VEX/OSV.
type CRAScanPayload struct {
	SBOMs       int    `json:"sboms"`
	Components  int    `json:"components"`
	Open        int    `json:"open"`
	ToVerify    int    `json:"to_verify"`
	NotAffected int    `json:"not_affected"`
	Fixed       int    `json:"fixed"`
	DBDate      string `json:"db_date"`
	Unchecked   int    `json:"unchecked,omitempty"`
	Conflicts   int    `json:"conflicts,omitempty"`
}

// CRAReportPayload enregistre une étape d'un signalement CRA : Step vaut
// open, early-warning, notification, final ou fix-available ; At est la
// date-heure (RFC 3339, UTC) de l'étape.
type CRAReportPayload struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Step string `json:"step"`
	At   string `json:"at"`
}

// RoutingInfo décrit le routage de la session : fournisseur local des
// contenus classés et fournisseurs de repli, dans l'ordre d'essai.
type RoutingInfo struct {
	Local    string   `json:"local,omitempty"`
	Fallback []string `json:"fallback,omitempty"`
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

// ContextShapedPayload records a change in the projected view sent to the
// model: the call_ids of the tool results hidden (Masked) or replaced by a
// reference to a later identical result (Deduplicated), and the estimated
// tokens saved on that request. The history itself is never changed.
type ContextShapedPayload struct {
	Masked       []string `json:"masked"`
	Deduplicated []string `json:"deduplicated"`
	SavedTokens  int      `json:"saved_tokens"`
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
	// Network is true when the allowed command runs with network access, granted
	// by the reply ("Allow with network") or by the matched rule; a reply alone
	// never grants it to a tool that is not a confined shell command.
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

// UsagePayload records the tokens of one model call. Provider and Model
// are those of the provider that actually answered (after a fallback).
// Purpose is "turn" (a turn iteration, or a prompt or agent hook evaluated
// inside a turn), "compaction" (summary request) or "subagent" (an
// iteration of an "agent" tool call). Input, Output and Cached come from the
// provider's usage report; when the stream carried none, Input and Output are
// estimated from the request sent and the reply (Estimated, Cached 0). The
// session token counter is the sum of Input + Output.
type UsagePayload struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Purpose   string `json:"purpose"`
	Input     int    `json:"input"`
	Output    int    `json:"output"`
	Cached    int    `json:"cached"`
	Estimated bool   `json:"estimated"`
	// ParentCallID is the call_id of the "agent" tool call that produced this
	// event inside a sub-agent run (or the run id of an agent hook); empty
	// for the main agent.
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// BudgetWarningPayload is emitted once per session, when the session token
// counter (Used) first reaches the warning threshold of the budget (Max).
type BudgetWarningPayload struct {
	Used int64 `json:"used"`
	Max  int64 `json:"max"`
}

// TurnCompletedPayload ends a turn. Reason is "done", "max_iterations", "incomplete", "blocked", "cancelled", "context_too_large" or "budget_exceeded" (the session token budget was used up before the turn: nothing was sent).
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
	// Purpose is "compaction" or "subagent" when the selection comes from a
	// dedicated router (a small model): not a switch of the session provider.
	// Empty for the turns.
	Purpose string `json:"purpose,omitempty"`
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
	case ContextShapedPayload:
		p.ParentCallID = parentCallID
		return p
	case UsagePayload:
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
