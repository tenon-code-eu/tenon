package compliance

import "time"

// Disclaimer est l'avertissement affiché en tête du rapport (texte exact).
const Disclaimer = "This report describes the activity recorded by tenon. It is not legal advice and does not certify compliance."

// GitHint est la commande qui retrouve le code marqué comme généré : le
// marquage git n'est pas dans le journal.
const GitHint = "git log --grep \"AI-Assisted-By\""

// Options règle la construction du rapport.
type Options struct {
	// Redact masque un texte issu du journal ; nil = identité.
	Redact func(string) string
	// RedactJSON masque un texte JSON chaîne par chaîne une fois décodé (clés
	// comprises) ; nil = Redact appliqué à chaque chaîne décodée.
	RedactJSON func(string) string
	// Details inclut les textes des commandes et la chronologie.
	Details bool
	// Generator vaut "tenon <version>".
	Generator string
	// Now est la date de génération.
	Now time.Time
}

// PolicyUse compte les sessions placées sous une politique d'organisation
// (identifiant, version, clé de signature).
type PolicyUse struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	KeyID    string `json:"key_id"`
	Sessions int    `json:"sessions"`
}

// Report est le modèle pur du rapport ; ses balises JSON forment le schéma stable.
type Report struct {
	Generator                string            `json:"generator"`
	GeneratedAt              time.Time         `json:"generated_at"`
	Period                   Period            `json:"period"`
	Scope                    Scope             `json:"scope"`
	Details                  bool              `json:"details"`
	Disclaimer               string            `json:"disclaimer"`
	Integrity                Integrity         `json:"integrity"`
	ContentAvailable         bool              `json:"content_available"`
	ExcludedMetadataSessions int               `json:"excluded_metadata_sessions"`
	Summary                  Summary           `json:"summary"`
	Providers                []ProviderUse     `json:"providers"`
	Profiles                 []string          `json:"profiles"`
	Policies                 []PolicyUse       `json:"policies"`
	Consents                 Consents          `json:"jurisdiction_consents"`
	WarningSessions          []string          `json:"jurisdiction_warning_sessions"`
	MCPServers               []MCPUse          `json:"mcp_servers"`
	MCPFailures              []MCPFailure      `json:"mcp_failures"`
	Redactions               map[string]int    `json:"redactions"`
	Permissions              []ToolPermissions `json:"permissions"`
	Files                    []FileChange      `json:"files"`
	Commands                 []SessionCommands `json:"commands"`
	Subagents                []SubagentRun     `json:"subagents"`
	Hooks                    []HookIssue       `json:"hooks"`
	Rewinds                  []RewindUse       `json:"rewinds"`
	Incidents                Incidents         `json:"incidents"`
	Mapping                  []MappingRow      `json:"regulatory_mapping"`
	GitHint                  string            `json:"git_marking_hint"`
	Timeline                 []SessionTimeline `json:"timeline,omitempty"`
}

// Period est la période couverte, bornes incluses, au format YYYY-MM-DD.
type Period struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// Scope est le périmètre : Kind vaut "project", "all" ou "session".
type Scope struct {
	Kind    string `json:"kind"`
	Project string `json:"project,omitempty"`
	Session string `json:"session,omitempty"`
}

// Integrity résume la chaîne entière et la plage exportée.
type Integrity struct {
	Intact     bool   `json:"intact"`
	Error      string `json:"error,omitempty"`
	TotalLines int    `json:"total_lines"`
	FinalHash  string `json:"final_hash,omitempty"`
	RangeLines int    `json:"range_lines"`
	FirstHash  string `json:"first_hash,omitempty"`
	LastHash   string `json:"last_hash,omitempty"`
	Level      string `json:"level"`
	// Timestamps : ancrage RFC 3161 de la chaîne (toujours présent).
	Timestamps Timestamps `json:"timestamps"`
}

// Timestamps résume les jetons d'horodatage RFC 3161. LastTime est en RFC 3339
// UTC ; Errors n'est jamais nil.
type Timestamps struct {
	Count      int      `json:"count"`
	Valid      int      `json:"valid"`
	LastFile   string   `json:"last_file,omitempty"`
	LastLine   int      `json:"last_line,omitempty"`
	LastTime   string   `json:"last_time,omitempty"`
	TSA        string   `json:"tsa,omitempty"`
	LinesAfter int      `json:"lines_after"`
	Errors     []string `json:"errors"`
}

// Summary donne les comptes principaux ; Jurisdictions liste les juridictions
// distinctes des fournisseurs, triées.
type Summary struct {
	Sessions      int      `json:"sessions"`
	Turns         int      `json:"turns"`
	ToolCalls     int      `json:"tool_calls"`
	FilesModified int      `json:"files_modified"`
	Denials       int      `json:"denials"`
	Jurisdictions []string `json:"jurisdictions"`
	// UnknownProviderSessions compte les sessions de la plage dont le
	// fournisseur est inconnu (premier SessionStarted réduit ou absent) : leurs
	// fournisseurs et juridictions manquent aux comptes.
	UnknownProviderSessions int `json:"unknown_provider_sessions"`
}

// ProviderUse regroupe les sessions d'un même fournisseur et modèle.
type ProviderUse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Model        string `json:"model"`
	Country      string `json:"country"`
	Jurisdiction string `json:"jurisdiction"`
	// Qualifications déclarées (jamais vérifiées par tenon), jamais nil.
	Qualifications []string  `json:"qualifications"`
	Sessions       int       `json:"sessions"`
	First          time.Time `json:"first"`
	Last           time.Time `json:"last"`
}

// Consents compte les consentements de juridiction accordés et refusés.
type Consents struct {
	Granted int `json:"granted"`
	Denied  int `json:"denied"`
}

// MCPUse décrit un serveur MCP actif et le nombre de sessions qui l'ont eu.
type MCPUse struct {
	ID           string `json:"id"`
	Jurisdiction string `json:"jurisdiction"`
	Country      string `json:"country"`
	Sessions     int    `json:"sessions"`
}

// MCPFailure est une panne de serveur MCP (McpServerDown).
type MCPFailure struct {
	Session string    `json:"session"`
	Server  string    `json:"server"`
	Error   string    `json:"error"`
	TS      time.Time `json:"ts"`
}

// ToolPermissions compte les demandes et décisions de permission d'un outil.
type ToolPermissions struct {
	Tool           string `json:"tool"`
	Requested      int    `json:"requested"`
	AllowedOnce    int    `json:"allowed_once"`
	AllowedSession int    `json:"allowed_session"`
	DeniedUser     int    `json:"denied_user"`
	DeniedPolicy   int    `json:"denied_policy"`
	DeniedHook     int    `json:"denied_hook"`
}

// FileChange est une modification de fichier (ToolResult avec path et hash_after).
// Author vaut "main" ou le nom de l'agent du sous-agent ; RewindStatus est
// l'état laissé par le dernier rewind ultérieur qui concerne ce fichier.
type FileChange struct {
	TS           time.Time `json:"ts"`
	Session      string    `json:"session"`
	Path         string    `json:"path"`
	Tool         string    `json:"tool"`
	HashBefore   string    `json:"hash_before"`
	HashAfter    string    `json:"hash_after"`
	Author       string    `json:"author"`
	NoCheckpoint bool      `json:"no_checkpoint"`
	RewindStatus string    `json:"rewind_status,omitempty"`
}

// SessionCommands compte les appels shell d'une session ; Commands n'est
// rempli qu'avec Details.
type SessionCommands struct {
	Session  string   `json:"session"`
	Count    int      `json:"count"`
	Commands []string `json:"commands,omitempty"`
}

// SubagentRun est une exécution de sous-agent.
type SubagentRun struct {
	TS      time.Time `json:"ts"`
	Session string    `json:"session"`
	CallID  string    `json:"call_id"`
	Agent   string    `json:"agent"`
	Status  string    `json:"status"`
}

// HookIssue est une exécution de hook notable (bloquée, en erreur, expirée ou
// qui demande une confirmation).
type HookIssue struct {
	TS       time.Time `json:"ts"`
	Session  string    `json:"session"`
	HookID   string    `json:"hook_id"`
	Event    string    `json:"event"`
	Status   string    `json:"status"`
	Decision string    `json:"decision,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

// RewindUse est un rewind et son bilan par état de fichier.
type RewindUse struct {
	TS             time.Time `json:"ts"`
	Session        string    `json:"session"`
	ToSeq          uint64    `json:"to_seq"`
	Mode           string    `json:"mode"`
	Restored       int       `json:"restored"`
	Deleted        int       `json:"deleted"`
	Kept           int       `json:"kept"`
	Skipped        int       `json:"skipped"`
	Failed         int       `json:"failed"`
	Unchanged      int       `json:"unchanged"`
	UntrackedCalls int       `json:"untracked_calls"`
}

// Incidents regroupe erreurs, tours interrompus et lignes illisibles.
type Incidents struct {
	Errors     []ErrorIncident `json:"errors"`
	Turns      []TurnIncident  `json:"turns"`
	Unreadable []Unreadable    `json:"unreadable"`
}

// ErrorIncident est un événement Error.
type ErrorIncident struct {
	TS      time.Time `json:"ts"`
	Session string    `json:"session"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

// TurnIncident est un tour terminé pour une raison notable.
type TurnIncident struct {
	TS      time.Time `json:"ts"`
	Session string    `json:"session"`
	Reason  string    `json:"reason"`
}

// MappingRow relie un élément du rapport aux cadres qu'il aide à documenter.
type MappingRow struct {
	Element    string   `json:"element"`
	Frameworks []string `json:"frameworks"`
}

// SessionTimeline est la chronologie d'une session (Details seulement).
type SessionTimeline struct {
	Session string          `json:"session"`
	Entries []TimelineEntry `json:"entries"`
}

// TimelineEntry est une entrée de la chronologie.
type TimelineEntry struct {
	TS   time.Time `json:"ts"`
	Kind string    `json:"kind"`
	Tool string    `json:"tool,omitempty"`
	Text string    `json:"text"`
}
