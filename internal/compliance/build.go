package compliance

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/event"
)

// clean masque un texte issu du journal puis remplace ses caractères de
// contrôle et de format par des espaces.
func (o Options) clean(s string) string {
	if o.Redact != nil {
		s = o.Redact(s)
	}
	return spaceClean(s)
}

// spaceClean remplace les caractères de contrôle (unicode.IsControl) et de
// format (unicode.Cf) par des espaces.
func spaceClean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}

// lineTS renvoie la date UTC d'une ligne (Load a déjà validé le format).
func lineTS(l audit.Line) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, l.TS)
	return t.UTC()
}

// sessKey identifie un objet propre à une session (permission, appel).
type sessKey struct{ session, id string }

// builder accumule les sections au fil des lignes.
type builder struct {
	o  Options
	ds Dataset
	r  *Report

	perms     map[string]*ToolPermissions
	permTool  map[sessKey]string // demande de permission d'outil → outil
	jurisReq  map[sessKey]bool   // demande de consentement de juridiction
	agents    map[sessKey]string // appel "agent" → nom du sous-agent
	warned    map[string]bool
	commands  map[string]*SessionCommands
	filePaths []string        // session et chemin bruts de chaque r.Files, pour les rewinds
	subagents map[sessKey]int // appel "agent" → indice dans r.Subagents
}

// mapping est la table de correspondance réglementaire, fixe et sans numéro d'article.
func mapping() []MappingRow {
	return []MappingRow{
		{Element: "providers_jurisdictions_consents", Frameworks: []string{"GDPR"}},
		{Element: "redactions_permissions", Frameworks: []string{"GDPR", "NIS2"}},
		{Element: "audit_chain_incidents", Frameworks: []string{"NIS2", "DORA"}},
		{Element: "ai_usage_transparency", Frameworks: []string{"AI Act"}},
	}
}

// incidentReasons sont les raisons de fin de tour signalées comme incidents.
var incidentReasons = map[string]bool{
	"blocked": true, "blocked_by_hook": true, "stopped_by_hook": true,
	"context_too_large": true, "max_iterations": true, "incomplete": true, "cancelled": true,
}

// hookIssueStatus sont les états de hook signalés (en plus de la décision "ask").
var hookIssueStatus = map[string]bool{"blocked": true, "error": true, "timeout": true}

// maxDetailRunes est la longueur gardée des réponses, arguments et résultats.
const maxDetailRunes = 500

// cut nettoie s puis le coupe à maxDetailRunes runes, « … » ajouté si coupé.
// La coupe suit le masquage : aucun préfixe de secret ne peut subsister.
func (o Options) cut(s string) string {
	return truncate(o.clean(s))
}

// truncate coupe s à maxDetailRunes runes, « … » ajouté si coupé.
func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxDetailRunes {
		return s
	}
	return string(r[:maxDetailRunes]) + "…"
}

// args masque les arguments d'un appel d'outil. En JSON brut un saut de ligne
// s'écrit `\n` : les chaînes sont donc masquées une fois décodées (règles
// ancrées en début de ligne, valeurs sous une clé sensible), le résultat est
// ré-encodé de façon compacte, nettoyé puis coupé. Des arguments qui ne sont
// pas du JSON valide sont masqués en texte brut.
func (o Options) args(raw json.RawMessage) string {
	if !json.Valid(raw) {
		return o.cut(string(raw))
	}
	var s string
	if o.RedactJSON != nil {
		s = o.RedactJSON(string(raw))
	} else {
		s = o.cleanLeaves(raw)
	}
	var buf bytes.Buffer
	if json.Compact(&buf, []byte(s)) == nil {
		s = buf.String()
	}
	return truncate(spaceClean(s))
}

// cleanLeaves nettoie (clean) chaque chaîne et chaque clé d'un JSON décodé
// puis le ré-encode ; les nombres sont gardés tels quels.
func (o Options) cleanLeaves(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return o.clean(string(raw))
	}
	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			return o.clean(t)
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
			return t
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, x := range t {
				out[o.clean(k)] = walk(x)
			}
			return out
		}
		return v
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(walk(v)) != nil {
		return o.clean(string(raw))
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// Build construit le rapport à partir du journal chargé. Pure : ne lit rien
// d'autre que ds. Les tranches ne sont jamais nil, la carte non plus.
func Build(ds Dataset, o Options) Report {
	r := Report{
		Generator:   o.Generator,
		GeneratedAt: o.Now.UTC(),
		Period: Period{
			Since: ds.Filter.Since.Format(time.DateOnly),
			Until: ds.Filter.Until.Format(time.DateOnly),
		},
		Scope:                    scopeOf(ds.Filter),
		Details:                  o.Details,
		Disclaimer:               Disclaimer,
		Integrity:                integrityOf(ds, o),
		ContentAvailable:         ds.Level != LevelMetadata,
		ExcludedMetadataSessions: ds.ExcludedMetadataSessions,
		Summary:                  Summary{Jurisdictions: []string{}},
		Providers:                []ProviderUse{},
		Profiles:                 []string{},
		WarningSessions:          []string{},
		MCPServers:               []MCPUse{},
		MCPFailures:              []MCPFailure{},
		Redactions:               map[string]int{},
		Permissions:              []ToolPermissions{},
		Files:                    []FileChange{},
		Commands:                 []SessionCommands{},
		Subagents:                []SubagentRun{},
		Hooks:                    []HookIssue{},
		Rewinds:                  []RewindUse{},
		Incidents: Incidents{
			Errors:     []ErrorIncident{},
			Turns:      []TurnIncident{},
			Unreadable: []Unreadable{},
		},
		Mapping: mapping(),
		GitHint: GitHint,
	}
	// Les lignes illisibles sont signalées à tout niveau d'audit.
	for _, u := range ds.Unreadable {
		r.Incidents.Unreadable = append(r.Incidents.Unreadable, Unreadable{File: o.clean(u.File), Line: u.Line, Error: o.clean(u.Error)})
	}

	sessions := map[string]bool{}
	for _, l := range ds.Lines {
		sessions[l.Session] = true
	}
	r.Summary.Sessions = len(sessions)
	if !r.ContentAvailable {
		return r
	}

	b := &builder{
		o: o, ds: ds, r: &r,
		perms:     map[string]*ToolPermissions{},
		permTool:  map[sessKey]string{},
		jurisReq:  map[sessKey]bool{},
		agents:    map[sessKey]string{},
		warned:    map[string]bool{},
		commands:  map[string]*SessionCommands{},
		subagents: map[sessKey]int{},
	}
	for _, l := range ds.Lines {
		if IsMetadataPayload(l.Payload) {
			continue
		}
		b.line(l)
	}
	b.finish()
	b.providers()
	r.Profiles = profilesOf(ds, o)
	if o.Details {
		r.Timeline = timeline(ds, o)
	}
	return r
}

// profilesOf liste les profils de conformité distincts des sessions de la
// plage : celui du premier SessionStarted de chaque session, et ceux des
// SessionStarted de reprise présents dans la plage. Triée, jamais nil.
func profilesOf(ds Dataset, o Options) []string {
	set := map[string]bool{}
	add := func(p string) {
		if p = o.clean(p); p != "" {
			set[p] = true
		}
	}
	inRange := map[string]bool{}
	for _, l := range ds.Lines {
		inRange[l.Session] = true
		if l.Type != event.SessionStarted || IsMetadataPayload(l.Payload) {
			continue
		}
		var p event.SessionStartedPayload
		if decode(l, &p) {
			add(p.Profile)
		}
	}
	for sid := range inRange {
		if st := ds.Sessions[sid].Started; st != nil {
			add(st.Profile)
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func scopeOf(f Filter) Scope {
	switch {
	case f.Session != "":
		return Scope{Kind: "session", Session: f.Session}
	case f.AllProjects:
		return Scope{Kind: "all"}
	}
	return Scope{Kind: "project", Project: f.Project}
}

// integrityOf nettoie aussi l'erreur de vérification : elle peut citer des
// valeurs lues dans un journal altéré.
func integrityOf(ds Dataset, o Options) Integrity {
	in := Integrity{
		Intact:     ds.Chain.Intact,
		Error:      o.clean(ds.Chain.Error),
		TotalLines: ds.Chain.Lines,
		FinalHash:  ds.Chain.FinalHash,
		RangeLines: len(ds.Lines),
		Level:      ds.Level,
	}
	if n := len(ds.Lines); n > 0 {
		in.FirstHash = ds.Lines[0].Hash
		in.LastHash = ds.Lines[n-1].Hash
	}
	return in
}

// decode lit la charge d'une ligne ; false si elle est illisible.
func decode(l audit.Line, v any) bool {
	return json.Unmarshal(l.Payload, v) == nil
}

func (b *builder) perm(tool string) *ToolPermissions {
	tool = b.o.clean(tool)
	p := b.perms[tool]
	if p == nil {
		p = &ToolPermissions{Tool: tool}
		b.perms[tool] = p
	}
	return p
}

// line traite une ligne à charge complète.
func (b *builder) line(l audit.Line) {
	r, o := b.r, b.o
	switch l.Type {
	case event.TurnCompleted:
		r.Summary.Turns++
		var p event.TurnCompletedPayload
		if decode(l, &p) && incidentReasons[p.Reason] {
			r.Incidents.Turns = append(r.Incidents.Turns, TurnIncident{TS: lineTS(l), Session: o.clean(l.Session), Reason: o.clean(p.Reason)})
		}
	case event.Error:
		var p event.ErrorPayload
		if decode(l, &p) {
			r.Incidents.Errors = append(r.Incidents.Errors, ErrorIncident{
				TS: lineTS(l), Session: o.clean(l.Session), Code: o.clean(p.Code), Message: o.clean(p.Message),
			})
		}
	case event.HookCompleted:
		var p event.HookCompletedPayload
		if decode(l, &p) && (hookIssueStatus[p.Status] || p.Decision == "ask") {
			r.Hooks = append(r.Hooks, HookIssue{
				TS: lineTS(l), Session: o.clean(l.Session), HookID: o.clean(p.HookID), Event: o.clean(p.Event),
				Status: o.clean(p.Status), Decision: o.clean(p.Decision), Reason: o.clean(p.Reason),
			})
		}
	case event.ToolCallRequested:
		r.Summary.ToolCalls++
		var p event.ToolCallRequestedPayload
		if decode(l, &p) && p.Tool == "shell" {
			b.shell(l.Session, p.Args)
		}
	case event.PermissionRequested:
		var p event.PermissionRequestedPayload
		if !decode(l, &p) {
			return
		}
		k := sessKey{l.Session, p.PermissionID}
		switch p.Kind {
		case "tool":
			b.perm(p.Tool).Requested++
			b.permTool[k] = p.Tool
		case "jurisdiction":
			b.jurisReq[k] = true
		}
	case event.PermissionResolved:
		var p event.PermissionResolvedPayload
		if !decode(l, &p) {
			return
		}
		k := sessKey{l.Session, p.PermissionID}
		if tool, ok := b.permTool[k]; ok {
			switch {
			case p.Allowed && p.Remember:
				b.perm(tool).AllowedSession++
			case p.Allowed:
				b.perm(tool).AllowedOnce++
			}
		} else if b.jurisReq[k] {
			if p.Allowed {
				r.Consents.Granted++
			} else {
				r.Consents.Denied++
			}
		}
	case event.ToolResult:
		var p event.ToolResultPayload
		if decode(l, &p) {
			b.toolResult(l, p)
		}
	case event.SubagentStarted:
		var p event.SubagentStartedPayload
		if decode(l, &p) {
			k := sessKey{l.Session, p.CallID}
			b.agents[k] = p.Agent
			b.subagents[k] = len(r.Subagents)
			r.Subagents = append(r.Subagents, SubagentRun{
				TS: lineTS(l), Session: o.clean(l.Session), CallID: o.clean(p.CallID), Agent: o.clean(p.Agent),
			})
		}
	case event.SubagentCompleted:
		var p event.SubagentCompletedPayload
		if !decode(l, &p) {
			return
		}
		if i, ok := b.subagents[sessKey{l.Session, p.CallID}]; ok {
			r.Subagents[i].Status = o.clean(p.Status)
		}
	case event.Rewound:
		var p event.RewoundPayload
		if decode(l, &p) {
			b.rewound(l.Session, p)
			r.Rewinds = append(r.Rewinds, rewindUse(l, p, o))
		}
	case event.JurisdictionWarning:
		b.warned[l.Session] = true
	case event.McpServerDown:
		var p event.McpServerDownPayload
		if decode(l, &p) {
			r.MCPFailures = append(r.MCPFailures, MCPFailure{
				Session: o.clean(l.Session), Server: o.clean(p.Server), Error: o.clean(p.Error), TS: lineTS(l),
			})
		}
	case event.RedactionApplied:
		var p event.RedactionAppliedPayload
		if decode(l, &p) {
			for k, n := range p.Counts {
				r.Redactions[o.clean(k)] += n
			}
		}
	}
}

// shell compte un appel shell ; le texte de la commande n'est gardé qu'avec Details.
func (b *builder) shell(session string, args json.RawMessage) {
	c := b.commands[session]
	if c == nil {
		c = &SessionCommands{Session: b.o.clean(session)}
		b.commands[session] = c
	}
	c.Count++
	if !b.o.Details {
		return
	}
	var a struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(args, &a) != nil || a.Command == "" {
		return
	}
	c.Commands = append(c.Commands, b.o.clean(a.Command))
}

func (b *builder) toolResult(l audit.Line, p event.ToolResultPayload) {
	switch p.Denied {
	case "user":
		b.r.Summary.Denials++
		b.perm(p.Tool).DeniedUser++
	case "policy":
		b.r.Summary.Denials++
		b.perm(p.Tool).DeniedPolicy++
	case "hook":
		b.r.Summary.Denials++
		b.perm(p.Tool).DeniedHook++
	}
	if p.Path == "" || p.HashAfter == "" {
		return
	}
	author := "main"
	if p.ParentCallID != "" {
		author = "subagent"
		if a, ok := b.agents[sessKey{l.Session, p.ParentCallID}]; ok {
			author = a
		}
	}
	o := b.o
	b.r.Files = append(b.r.Files, FileChange{
		TS: lineTS(l), Session: o.clean(l.Session), Path: o.clean(p.Path), Tool: o.clean(p.Tool),
		HashBefore: o.clean(p.HashBefore), HashAfter: o.clean(p.HashAfter), Author: o.clean(author),
		NoCheckpoint: p.NoCheckpoint,
	})
	b.filePaths = append(b.filePaths, l.Session+"\x00"+p.Path)
}

// rewound applique l'état de chaque fichier aux modifications antérieures de
// la même session et du même chemin ; un rewind ultérieur l'écrase.
func (b *builder) rewound(session string, p event.RewoundPayload) {
	status := map[string]string{}
	for _, f := range p.Files {
		status[session+"\x00"+f.Path] = f.Status
	}
	for i, k := range b.filePaths {
		if s, ok := status[k]; ok {
			b.r.Files[i].RewindStatus = b.o.clean(s)
		}
	}
}

// rewindUse compte les fichiers d'un rewind par état.
func rewindUse(l audit.Line, p event.RewoundPayload, o Options) RewindUse {
	u := RewindUse{TS: lineTS(l), Session: o.clean(l.Session), ToSeq: p.ToSeq, Mode: o.clean(p.Mode), UntrackedCalls: p.UntrackedCalls}
	for _, f := range p.Files {
		switch f.Status {
		case "restored":
			u.Restored++
		case "deleted":
			u.Deleted++
		case "kept":
			u.Kept++
		case "skipped":
			u.Skipped++
		case "failed":
			u.Failed++
		case "unchanged":
			u.Unchanged++
		}
	}
	return u
}

// finish trie et recopie les sections accumulées dans des cartes.
func (b *builder) finish() {
	r := b.r
	for _, p := range b.perms {
		r.Permissions = append(r.Permissions, *p)
	}
	sort.Slice(r.Permissions, func(i, j int) bool { return r.Permissions[i].Tool < r.Permissions[j].Tool })

	for s := range b.warned {
		r.WarningSessions = append(r.WarningSessions, b.o.clean(s))
	}
	sort.Strings(r.WarningSessions)

	for _, c := range b.commands {
		r.Commands = append(r.Commands, *c)
	}
	sort.Slice(r.Commands, func(i, j int) bool { return r.Commands[i].Session < r.Commands[j].Session })

	seen := map[string]bool{}
	for _, k := range b.filePaths {
		seen[k] = true
	}
	r.Summary.FilesModified = len(seen)
}

// providers regroupe les sessions de la plage par fournisseur et modèle, et
// par serveur MCP. First et Last sont la première et la dernière ligne de la
// plage des sessions concernées.
func (b *builder) providers() {
	type span struct{ first, last time.Time }
	spans := map[string]*span{}
	var order []string
	for _, l := range b.ds.Lines {
		t := lineTS(l)
		s := spans[l.Session]
		if s == nil {
			spans[l.Session] = &span{t, t}
			order = append(order, l.Session)
			continue
		}
		if t.Before(s.first) {
			s.first = t
		}
		if t.After(s.last) {
			s.last = t
		}
	}

	type pkey struct{ id, model string }
	provs := map[pkey]*ProviderUse{}
	mcps := map[MCPUse]int{}
	juris := map[string]bool{}
	o := b.o
	for _, sid := range order {
		st := b.ds.Sessions[sid].Started
		if st == nil {
			b.r.Summary.UnknownProviderSessions++
			continue
		}
		pv, sp := st.Provider, spans[sid]
		k := pkey{pv.ID, pv.Model}
		u := provs[k]
		if u == nil {
			u = &ProviderUse{
				ID: o.clean(pv.ID), Name: o.clean(pv.Name), Model: o.clean(pv.Model),
				Country: o.clean(pv.Country), Jurisdiction: o.clean(string(pv.Jurisdiction)),
				First: sp.first, Last: sp.last,
			}
			provs[k] = u
		}
		u.Sessions++
		if sp.first.Before(u.First) {
			u.First = sp.first
		}
		if sp.last.After(u.Last) {
			u.Last = sp.last
		}
		if u.Jurisdiction != "" {
			juris[u.Jurisdiction] = true
		}
		for _, m := range st.MCP {
			mcps[MCPUse{ID: o.clean(m.ID), Jurisdiction: o.clean(string(m.Jurisdiction)), Country: o.clean(m.Country)}]++
		}
	}

	r := b.r
	for _, u := range provs {
		r.Providers = append(r.Providers, *u)
	}
	sort.Slice(r.Providers, func(i, j int) bool {
		a, c := r.Providers[i], r.Providers[j]
		if a.ID != c.ID {
			return a.ID < c.ID
		}
		return a.Model < c.Model
	})
	for j := range juris {
		r.Summary.Jurisdictions = append(r.Summary.Jurisdictions, j)
	}
	sort.Strings(r.Summary.Jurisdictions)
	for m, n := range mcps {
		m.Sessions = n
		r.MCPServers = append(r.MCPServers, m)
	}
	sort.Slice(r.MCPServers, func(i, j int) bool {
		a, c := r.MCPServers[i], r.MCPServers[j]
		if a.ID != c.ID {
			return a.ID < c.ID
		}
		if a.Jurisdiction != c.Jurisdiction {
			return a.Jurisdiction < c.Jurisdiction
		}
		return a.Country < c.Country
	})
}

// timeline construit la chronologie détaillée : sessions dans l'ordre de leur
// première ligne, entrées dans l'ordre du journal. Les lignes réduites et les
// charges illisibles sont ignorées.
func timeline(ds Dataset, o Options) []SessionTimeline {
	resolved := map[sessKey]bool{}
	for _, l := range ds.Lines {
		var p event.PermissionResolvedPayload
		if l.Type == event.PermissionResolved && !IsMetadataPayload(l.Payload) && decode(l, &p) {
			resolved[sessKey{l.Session, p.PermissionID}] = p.Allowed
		}
	}

	out := []SessionTimeline{}
	index := map[string]int{}
	for _, l := range ds.Lines {
		i, ok := index[l.Session]
		if !ok {
			i = len(out)
			index[l.Session] = i
			out = append(out, SessionTimeline{Session: o.clean(l.Session), Entries: []TimelineEntry{}})
		}
		if IsMetadataPayload(l.Payload) {
			continue
		}
		if e, ok := timelineEntry(l, o, resolved); ok {
			e.TS = lineTS(l)
			out[i].Entries = append(out[i].Entries, e)
		}
	}
	return out
}

// timelineEntry traduit une ligne en entrée de chronologie ; false si la ligne
// n'en produit pas.
func timelineEntry(l audit.Line, o Options, resolved map[sessKey]bool) (TimelineEntry, bool) {
	switch l.Type {
	case event.UserMessage:
		var p event.UserMessagePayload
		if decode(l, &p) {
			return TimelineEntry{Kind: "prompt", Text: o.clean(p.Text)}, true
		}
	case event.AssistantMessage:
		var p event.AssistantMessagePayload
		if decode(l, &p) {
			return TimelineEntry{Kind: "response", Text: o.cut(p.Text)}, true
		}
	case event.ToolCallRequested:
		var p event.ToolCallRequestedPayload
		if decode(l, &p) {
			return TimelineEntry{Kind: "tool_call", Tool: o.clean(p.Tool), Text: o.args(p.Args)}, true
		}
	case event.ToolResult:
		var p event.ToolResultPayload
		if decode(l, &p) {
			return TimelineEntry{Kind: "tool_result", Tool: o.clean(p.Tool), Text: o.cut(p.Content)}, true
		}
	case event.PermissionRequested:
		var p event.PermissionRequestedPayload
		if !decode(l, &p) {
			break
		}
		parts := []string{}
		for _, s := range []string{p.Tool, p.Command} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		if allowed, ok := resolved[sessKey{l.Session, p.PermissionID}]; ok {
			if allowed {
				parts = append(parts, "allowed")
			} else {
				parts = append(parts, "denied")
			}
		}
		return TimelineEntry{Kind: "permission", Tool: o.clean(p.Tool), Text: o.clean(strings.Join(parts, " "))}, true
	case event.HookCompleted:
		var p event.HookCompletedPayload
		if decode(l, &p) {
			return TimelineEntry{Kind: "hook", Text: o.clean(p.HookID + " " + p.Status)}, true
		}
	}
	return TimelineEntry{}, false
}
