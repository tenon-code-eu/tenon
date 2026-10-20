package compliance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
)

// logb construit une suite d'événements : une seconde entre deux événements,
// numéros de séquence par session.
type logb struct {
	t    *testing.T
	base time.Time
	seq  map[string]uint64
	evs  []event.Event
}

func newLog(t *testing.T) *logb {
	return &logb{t: t, base: since.Add(time.Hour), seq: map[string]uint64{}}
}

// add ajoute un événement et renvoie sa date.
func (b *logb) add(sid string, typ event.Type, payload any) time.Time {
	b.t.Helper()
	b.seq[sid]++
	ts := b.base.Add(time.Duration(len(b.evs)) * time.Second)
	b.evs = append(b.evs, ev(b.t, sid, b.seq[sid], ts, typ, payload))
	return ts
}

func (b *logb) start(sid string, prov llm.ProviderInfo, mcp ...event.MCPServerInfo) time.Time {
	return b.add(sid, event.SessionStarted, event.SessionStartedPayload{Provider: prov, ProjectRoot: "/p", MCP: mcp})
}

// dataset écrit le journal au niveau full et le charge sur tous les projets.
func (b *logb) dataset() Dataset {
	b.t.Helper()
	return loadAll(b.t, writeLog(b.t, audit.Full, b.evs...))
}

func loadAll(t *testing.T, dir string) Dataset {
	t.Helper()
	ds, err := Load(dir, Filter{Since: since, Until: until, AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

var (
	provEU   = llm.ProviderInfo{ID: "mistral", Name: "Mistral", Model: "large", Country: "FR", Jurisdiction: llm.EU}
	provUS   = llm.ProviderInfo{ID: "openai", Name: "OpenAI", Model: "gpt", Country: "US", Jurisdiction: llm.NonEU}
	buildNow = time.Date(2026, 1, 13, 8, 0, 0, 0, time.FixedZone("x", 3600))
)

func opts() Options { return Options{Generator: "tenon test", Now: buildNow} }

func TestBuildHeaderAndIntegrity(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	b.add("s", event.UserMessage, event.UserMessagePayload{Text: "x"})
	ds := b.dataset()
	r := Build(ds, opts())

	if r.Generator != "tenon test" || !r.GeneratedAt.Equal(buildNow) || r.GeneratedAt.Location() != time.UTC {
		t.Fatalf("generator %q at %v", r.Generator, r.GeneratedAt)
	}
	if r.Period != (Period{Since: "2026-01-10", Until: "2026-01-12"}) || r.Scope != (Scope{Kind: "all"}) {
		t.Fatalf("period %+v scope %+v", r.Period, r.Scope)
	}
	if r.Disclaimer != Disclaimer || r.GitHint != GitHint || r.Details {
		t.Fatalf("disclaimer %q hint %q", r.Disclaimer, r.GitHint)
	}
	want := Integrity{
		Intact: true, TotalLines: 2, FinalHash: ds.Chain.FinalHash, RangeLines: 2,
		FirstHash: ds.Lines[0].Hash, LastHash: ds.Lines[1].Hash, Level: LevelFull,
	}
	if r.Integrity != want || want.FinalHash == "" || want.FirstHash == want.LastHash {
		t.Fatalf("integrity %+v want %+v", r.Integrity, want)
	}
	if !r.ContentAvailable {
		t.Fatal("content must be available at full level")
	}
}

func TestBuildScopeKind(t *testing.T) {
	cases := []struct {
		f    Filter
		want Scope
	}{
		{Filter{Project: "/p"}, Scope{Kind: "project", Project: "/p"}},
		{Filter{AllProjects: true}, Scope{Kind: "all"}},
		{Filter{Session: "s1"}, Scope{Kind: "session", Session: "s1"}},
	}
	for _, c := range cases {
		if got := Build(Dataset{Filter: c.f, Level: LevelNone}, opts()).Scope; got != c.want {
			t.Fatalf("scope %+v want %+v", got, c.want)
		}
	}
}

func TestBuildIntegrityBroken(t *testing.T) {
	ds := Dataset{Chain: Chain{Intact: false, Error: "line 3: hash mismatch", Lines: 3}, Level: LevelNone}
	r := Build(ds, opts())
	if r.Integrity.Intact || r.Integrity.Error != "line 3: hash mismatch" || r.Integrity.TotalLines != 3 {
		t.Fatalf("integrity %+v", r.Integrity)
	}
}

func TestBuildIntegrityErrorCleaned(t *testing.T) {
	const key = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	ds := Dataset{Chain: Chain{Error: "line 2: prev " + key + "\x1b[2J does not match"}, Level: LevelNone}
	red, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.Redact = func(s string) string { out, _ := red.Redact(s); return out }
	e := Build(ds, o).Integrity.Error
	if strings.Contains(e, key) || !strings.Contains(e, "[SECRET_") || strings.ContainsRune(e, 0x1b) {
		t.Fatalf("chain error not cleaned: %q", e)
	}
}

func TestBuildSummary(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	for i, tool := range []string{"read", "write", "shell"} {
		b.add("s", event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: string(rune('a' + i)), Tool: tool, Args: json.RawMessage(`{}`)})
	}
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "b", Tool: "write", Denied: "policy"})
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "c", Tool: "shell", Denied: "user"})
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "a", Tool: "read", Path: "/p/a.go", HashBefore: "h0", HashAfter: "h1"})
	b.add("s", event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
	b.add("s", event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
	r := Build(b.dataset(), opts())

	want := Summary{Sessions: 1, Turns: 2, ToolCalls: 3, FilesModified: 1, Denials: 2, Jurisdictions: []string{"eu"}}
	if !reflect.DeepEqual(r.Summary, want) {
		t.Fatalf("summary %+v want %+v", r.Summary, want)
	}
}

func TestBuildProvidersAndTransfers(t *testing.T) {
	b := newLog(t)
	mcp := event.MCPServerInfo{ID: "fs", Jurisdiction: llm.EU, Country: "DE"}
	first := b.start("a", provEU, mcp)
	b.start("u", provUS)
	b.add("u", event.JurisdictionWarning, event.JurisdictionWarningPayload{Provider: provUS})
	b.add("u", event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "j1", Kind: "jurisdiction"})
	b.add("u", event.PermissionResolved, event.PermissionResolvedPayload{PermissionID: "j1", Allowed: true})
	b.add("u", event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "j2", Kind: "jurisdiction"})
	b.add("u", event.PermissionResolved, event.PermissionResolvedPayload{PermissionID: "j2", Allowed: false})
	b.start("b", provEU, mcp)
	down := b.add("b", event.McpServerDown, event.McpServerDownPayload{Server: "fs", Error: "connection refused"})
	last := b.add("a", event.UserMessage, event.UserMessagePayload{Text: "x"})
	r := Build(b.dataset(), opts())

	if len(r.Providers) != 2 {
		t.Fatalf("providers %+v", r.Providers)
	}
	eu := r.Providers[0]
	if eu.ID != "mistral" || eu.Name != "Mistral" || eu.Model != "large" || eu.Country != "FR" ||
		eu.Jurisdiction != "eu" || eu.Sessions != 2 || !eu.First.Equal(first) || !eu.Last.Equal(last) {
		t.Fatalf("eu provider %+v", eu)
	}
	if r.Providers[1].ID != "openai" || r.Providers[1].Sessions != 1 {
		t.Fatalf("us provider %+v", r.Providers[1])
	}
	if !reflect.DeepEqual(r.Summary.Jurisdictions, []string{"eu", "non-eu"}) {
		t.Fatalf("jurisdictions %v", r.Summary.Jurisdictions)
	}
	if r.Consents != (Consents{Granted: 1, Denied: 1}) {
		t.Fatalf("consents %+v", r.Consents)
	}
	if !reflect.DeepEqual(r.WarningSessions, []string{"u"}) {
		t.Fatalf("warning sessions %v", r.WarningSessions)
	}
	if !reflect.DeepEqual(r.MCPServers, []MCPUse{{ID: "fs", Jurisdiction: "eu", Country: "DE", Sessions: 2}}) {
		t.Fatalf("mcp %+v", r.MCPServers)
	}
	wantDown := []MCPFailure{{Session: "b", Server: "fs", Error: "connection refused", TS: down}}
	if len(r.MCPFailures) != 1 || r.MCPFailures[0].Session != "b" || r.MCPFailures[0].Server != "fs" ||
		r.MCPFailures[0].Error != "connection refused" || !r.MCPFailures[0].TS.Equal(down) {
		t.Fatalf("mcp failures %+v want %+v", r.MCPFailures, wantDown)
	}
}

func TestBuildRedactionsSummed(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	b.add("s", event.RedactionApplied, event.RedactionAppliedPayload{Counts: map[string]int{"SECRET": 2, "EMAIL": 1}})
	b.add("s", event.RedactionApplied, event.RedactionAppliedPayload{Counts: map[string]int{"SECRET": 1}})
	r := Build(b.dataset(), opts())
	if !reflect.DeepEqual(r.Redactions, map[string]int{"SECRET": 3, "EMAIL": 1}) {
		t.Fatalf("redactions %v", r.Redactions)
	}
}

func TestBuildPermissions(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	req := func(id, tool string) {
		b.add("s", event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: id, Kind: "tool", Tool: tool})
	}
	res := func(id string, allowed, remember bool) {
		b.add("s", event.PermissionResolved, event.PermissionResolvedPayload{PermissionID: id, Allowed: allowed, Remember: remember})
	}
	denied := func(tool, by string) {
		b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "c", Tool: tool, Denied: by})
	}
	req("1", "shell")
	res("1", true, false)
	req("2", "shell")
	res("2", true, true)
	req("3", "shell")
	res("3", false, false)
	denied("shell", "user")
	denied("shell", "policy")
	denied("write", "hook")
	r := Build(b.dataset(), opts())

	want := []ToolPermissions{
		{Tool: "shell", Requested: 3, AllowedOnce: 1, AllowedSession: 1, DeniedUser: 1, DeniedPolicy: 1},
		{Tool: "write", DeniedHook: 1},
	}
	if !reflect.DeepEqual(r.Permissions, want) {
		t.Fatalf("permissions %+v want %+v", r.Permissions, want)
	}
}

func TestBuildFiles(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	t1 := b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "c1", Tool: "write", Path: "/p/main.go", HashBefore: "h0", HashAfter: "h1"})
	b.add("s", event.SubagentStarted, event.SubagentStartedPayload{CallID: "ag1", Agent: "reviewer"})
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "c2", Tool: "edit", Path: "/p/sub.go", HashAfter: "h2", ParentCallID: "ag1"})
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "c3", Tool: "edit", Path: "/p/orphan.go", HashAfter: "h3", ParentCallID: "zz", NoCheckpoint: true})
	// Résultat sans hash_after : pas une modification.
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "c4", Tool: "read", Path: "/p/main.go"})
	b.add("s", event.Rewound, event.RewoundPayload{ToSeq: 2, Mode: "code", Files: []event.RewoundFile{{Path: "/p/main.go", Status: "kept"}}})
	b.add("s", event.Rewound, event.RewoundPayload{ToSeq: 2, Mode: "code", Files: []event.RewoundFile{{Path: "/p/main.go", Status: "restored"}}})
	// Modification postérieure au rewind : non concernée.
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "c5", Tool: "write", Path: "/p/main.go", HashBefore: "h1", HashAfter: "h4"})
	r := Build(b.dataset(), opts())

	if len(r.Files) != 4 {
		t.Fatalf("files %+v", r.Files)
	}
	f := r.Files[0]
	if !f.TS.Equal(t1) || f.Session != "s" || f.Path != "/p/main.go" || f.Tool != "write" || f.HashBefore != "h0" ||
		f.HashAfter != "h1" || f.Author != "main" || f.NoCheckpoint || f.RewindStatus != "restored" {
		t.Fatalf("main file %+v", f)
	}
	if r.Files[1].Author != "reviewer" || r.Files[1].RewindStatus != "" {
		t.Fatalf("sub-agent file %+v", r.Files[1])
	}
	if r.Files[2].Author != "subagent" || !r.Files[2].NoCheckpoint {
		t.Fatalf("orphan file %+v", r.Files[2])
	}
	if r.Files[3].RewindStatus != "" {
		t.Fatalf("later file %+v", r.Files[3])
	}
	if r.Summary.FilesModified != 3 {
		t.Fatalf("files modified %d", r.Summary.FilesModified)
	}
}

func TestBuildCommands(t *testing.T) {
	b := newLog(t)
	b.start("s", provEU)
	b.add("s", event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "1", Tool: "shell", Args: json.RawMessage(`{"command":"go test ./..."}`)})
	b.add("s", event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "2", Tool: "read", Args: json.RawMessage(`{"path":"x"}`)})
	b.add("s", event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "3", Tool: "shell", Args: json.RawMessage(`{"command":"ls"}`)})
	// Arguments malformés : l'appel compte, aucun texte vide n'est ajouté.
	b.add("s", event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "4", Tool: "shell", Args: json.RawMessage(`{"command":42}`)})
	ds := b.dataset()

	r := Build(ds, opts())
	if !reflect.DeepEqual(r.Commands, []SessionCommands{{Session: "s", Count: 3}}) {
		t.Fatalf("commands without details %+v", r.Commands)
	}
	o := opts()
	o.Details = true
	r = Build(ds, o)
	want := []SessionCommands{{Session: "s", Count: 3, Commands: []string{"go test ./...", "ls"}}}
	if !reflect.DeepEqual(r.Commands, want) || !r.Details {
		t.Fatalf("commands with details %+v", r.Commands)
	}
}

func TestBuildRedactsAndCleans(t *testing.T) {
	const key = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	b := newLog(t)
	b.start("s", provEU)
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "1", Tool: "write", Path: "/p/" + key + "/x.go", HashAfter: "h"})
	b.add("s", event.ToolResult, event.ToolResultPayload{CallID: "2", Tool: "write", Path: "/p/\x1b[31mred.go", HashAfter: "h"})
	b.add("s", event.McpServerDown, event.McpServerDownPayload{Server: "fs", Error: "auth failed with " + key})
	red, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.Redact = func(s string) string { out, _ := red.Redact(s); return out }
	r := Build(b.dataset(), o)

	if strings.Contains(r.Files[0].Path, key) || !strings.Contains(r.Files[0].Path, "[SECRET_") {
		t.Fatalf("path not redacted: %q", r.Files[0].Path)
	}
	if r.Files[1].Path != "/p/ [31mred.go" {
		t.Fatalf("path not cleaned: %q", r.Files[1].Path)
	}
	if strings.Contains(r.MCPFailures[0].Error, key) || !strings.Contains(r.MCPFailures[0].Error, "[SECRET_") {
		t.Fatalf("error not redacted: %q", r.MCPFailures[0].Error)
	}
}

func TestSpaceClean(t *testing.T) {
	if got := spaceClean("a\tb\nc\u200bd\u202ee\x7f"); got != "a b c d e " {
		t.Fatalf("spaceClean = %q", got)
	}
}

func TestBuildMetadataLevel(t *testing.T) {
	d := since.Add(time.Hour)
	dir := writeLog(t, audit.Metadata,
		started(t, "s", d, "/proj"),
		ev(t, "s", 2, d.Add(time.Second), event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "1", Tool: "shell", Args: json.RawMessage(`{"command":"ls"}`)}),
		ev(t, "s", 3, d.Add(2*time.Second), event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}),
	)
	r := Build(loadAll(t, dir), opts())

	if r.ContentAvailable || r.Integrity.Level != LevelMetadata {
		t.Fatalf("content available %v level %s", r.ContentAvailable, r.Integrity.Level)
	}
	if !reflect.DeepEqual(r.Summary, Summary{Sessions: 1, Jurisdictions: []string{}}) {
		t.Fatalf("summary %+v", r.Summary)
	}
	if len(r.Providers)+len(r.Files)+len(r.Commands)+len(r.Permissions)+len(r.MCPServers) != 0 {
		t.Fatalf("content slices not empty: %+v", r)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`"files":[]`, `"providers":[]`, `"commands":[]`, `"permissions":[]`, `"redactions":{}`,
		`"jurisdiction_warning_sessions":[]`, `"mcp_servers":[]`, `"mcp_failures":[]`, `"jurisdictions":[]`,
		`"subagents":[]`, `"hooks":[]`, `"rewinds":[]`, `"errors":[]`, `"turns":[]`, `"unreadable":[]`,
		`"regulatory_mapping":[]`,
	} {
		if !strings.Contains(string(out), s) {
			t.Fatalf("JSON lacks %s: %s", s, out)
		}
	}
	if strings.Contains(string(out), "null") || strings.Contains(string(out), `"timeline"`) {
		t.Fatalf("JSON has null or timeline: %s", out)
	}
}

func TestBuildMixedLevelSkipsMetadataPayloads(t *testing.T) {
	d := since.Add(time.Hour)
	dir := writeLog(t, audit.Full,
		ev(t, "s", 1, d, event.SessionStarted, event.SessionStartedPayload{Provider: provEU, ProjectRoot: "/p"}),
		ev(t, "s", 2, d.Add(time.Second), event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}),
	)
	w, err := audit.Open(dir, audit.Metadata, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ev(t, "s", 3, d.Add(2*time.Second), event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ev(t, "s", 4, d.Add(3*time.Second), event.ToolResult, event.ToolResultPayload{Tool: "write", Path: "/p/x", HashAfter: "h"})); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := Build(loadAll(t, dir), opts())
	if !r.ContentAvailable || r.Integrity.Level != LevelMixed {
		t.Fatalf("content %v level %s", r.ContentAvailable, r.Integrity.Level)
	}
	// Les lignes réduites sont ignorées, comme au niveau metadata : seul le
	// premier TurnCompleted compte et le ToolResult réduit n'est pas décodé.
	if r.Summary.Sessions != 1 || r.Summary.Turns != 1 || len(r.Files) != 0 || len(r.Providers) != 1 {
		t.Fatalf("summary %+v files %+v providers %+v", r.Summary, r.Files, r.Providers)
	}
}
