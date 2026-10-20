package compliance

import (
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
	filePaths []string // session et chemin bruts de chaque r.Files, pour les rewinds
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
		Integrity:                integrityOf(ds),
		ContentAvailable:         ds.Level != LevelMetadata,
		ExcludedMetadataSessions: ds.ExcludedMetadataSessions,
		Summary:                  Summary{Jurisdictions: []string{}},
		Providers:                []ProviderUse{},
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
		Mapping: []MappingRow{},
		GitHint: GitHint,
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
		perms:    map[string]*ToolPermissions{},
		permTool: map[sessKey]string{},
		jurisReq: map[sessKey]bool{},
		agents:   map[sessKey]string{},
		warned:   map[string]bool{},
		commands: map[string]*SessionCommands{},
	}
	for _, l := range ds.Lines {
		if IsMetadataPayload(l.Payload) {
			continue
		}
		b.line(l)
	}
	b.finish()
	b.providers()
	return r
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

func integrityOf(ds Dataset) Integrity {
	in := Integrity{
		Intact:     ds.Chain.Intact,
		Error:      ds.Chain.Error,
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
			b.agents[sessKey{l.Session, p.CallID}] = p.Agent
		}
	case event.Rewound:
		var p event.RewoundPayload
		if decode(l, &p) {
			b.rewound(l.Session, p)
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
	_ = json.Unmarshal(args, &a)
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
