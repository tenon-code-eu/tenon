// Package llm defines the provider-neutral types exchanged with LLM backends.
package llm

import "encoding/json"

// Role identifies the author of a message.
type Role string

const (
	System    Role = "system"
	User      Role = "user"
	Assistant Role = "assistant"
	Tool      Role = "tool"
)

// Message is one entry of a conversation.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a model request to run a tool.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ToolDef describes a tool offered to the model.
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Request is a completion request sent to a provider.
type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolDef
}

// Chunk is one piece of a streamed provider response.
type Chunk struct {
	Text      string
	ToolCalls []ToolCall
	Done      bool
	Err       error
}

// Jurisdiction is the legal zone in which a provider processes data.
type Jurisdiction string

const (
	EU    Jurisdiction = "eu"
	NonEU Jurisdiction = "non-eu"
	Local Jurisdiction = "local"
)

// ProviderInfo describes a configured LLM provider.
type ProviderInfo struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	BaseURL      string       `json:"base_url"`
	Model        string       `json:"model"`
	Country      string       `json:"country"`
	Jurisdiction Jurisdiction `json:"jurisdiction"`
	// Qualifications déclarées (ex. "secnumcloud") ; jamais déduites.
	Qualifications []string `json:"qualifications,omitempty"`
}
