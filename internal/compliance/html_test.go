package compliance

import (
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

const cspMeta = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`

// richReport construit un rapport au niveau full dont chaque section est remplie.
func richReport(details bool) Report {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	r := Report{
		Generator:   "tenon test",
		GeneratedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Period:      Period{Since: "2026-10-01", Until: "2026-10-20"},
		Scope:       Scope{Kind: "project", Project: "/work/demo"},
		Details:     details,
		Disclaimer:  Disclaimer,
		Integrity: Integrity{
			Intact: true, TotalLines: 12, FinalHash: "sha256:final",
			RangeLines: 10, FirstHash: "sha256:first", LastHash: "sha256:last", Level: LevelFull,
		},
		ContentAvailable: true,
		Summary:          Summary{Sessions: 1, Turns: 2, ToolCalls: 3, FilesModified: 1, Denials: 1, Jurisdictions: []string{"eu"}},
		Providers: []ProviderUse{{
			ID: "p", Name: "Prov", Model: "m", Country: "FR", Jurisdiction: "eu",
			Sessions: 1, First: t0, Last: t0.Add(time.Hour),
		}},
		Consents:        Consents{Granted: 1, Denied: 2},
		WarningSessions: []string{"s-warn"},
		MCPServers:      []MCPUse{{ID: "mcp1", Jurisdiction: "non-eu", Country: "US", Sessions: 1}},
		MCPFailures:     []MCPFailure{{Session: "s1", Server: "mcp1", Error: "down", TS: t0}},
		Redactions:      map[string]int{"api_key": 3},
		Permissions:     []ToolPermissions{{Tool: "bash", Requested: 2, AllowedOnce: 1, DeniedUser: 1}},
		Files: []FileChange{{
			TS: t0, Session: "s1", Path: "</style><script>x</script>", Tool: "edit",
			HashBefore: "sha256:01", HashAfter: "sha256:02", Author: "main", NoCheckpoint: true, RewindStatus: "restored",
		}},
		Commands:  []SessionCommands{{Session: "s1", Count: 1}},
		Subagents: []SubagentRun{{TS: t0, Session: "s1", CallID: "c1", Agent: "explorer", Status: "completed"}},
		Hooks:     []HookIssue{{TS: t0, Session: "s1", HookID: "h1", Event: "PreToolUse", Status: "blocked", Reason: "</style><script>hook</script>"}},
		Rewinds:   []RewindUse{{TS: t0, Session: "s1", ToSeq: 4, Mode: "code", Restored: 1}},
		Incidents: Incidents{
			Errors:     []ErrorIncident{{TS: t0, Session: "s1", Code: "E1", Message: "boom"}},
			Turns:      []TurnIncident{{TS: t0, Session: "s1", Reason: "cancelled"}},
			Unreadable: []Unreadable{{File: "2026-10-01.jsonl", Line: 7, Error: "bad json"}},
		},
		Mapping: mapping(),
		GitHint: GitHint,
	}
	if details {
		r.Commands[0].Commands = []string{"ls -la"}
		r.Timeline = []SessionTimeline{{Session: "s1", Entries: []TimelineEntry{
			{TS: t0, Kind: "prompt", Text: "<script>alert(1)</script>"},
			{TS: t0, Kind: "response", Text: "PROMPT-RESPONSE-TEXT"},
			{TS: t0, Kind: "tool_call", Tool: "bash", Text: `{"cmd":"ls"}`},
			{TS: t0, Kind: "tool_result", Tool: "bash", Text: "out"},
			{TS: t0, Kind: "permission", Tool: "bash", Text: "allow"},
			{TS: t0, Kind: "hook", Text: "h1 blocked"},
		}}}
	}
	return r
}

func render(t *testing.T, r Report, lang i18n.Lang) string {
	t.Helper()
	b, err := RenderHTML(r, "sha256:reportid", lang)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderHTMLLanguages(t *testing.T) {
	fr := render(t, richReport(false), i18n.FR)
	en := render(t, richReport(false), i18n.EN)
	for _, c := range []struct{ out, want string }{
		{fr, `<html lang="fr">`}, {fr, "Rapport d&#39;activité tenon"},
		{en, `<html lang="en">`}, {en, "tenon activity report"},
		{fr, "sha256:reportid"}, {en, "sha256:reportid"},
		{fr, "chaîne intacte : 12 lignes, hash final sha256:final"},
		{en, "audit chain intact: 12 lines, final hash sha256:final"},
		{fr, "Ce rapport décrit l&#39;activité enregistrée par tenon."},
		{en, "This report describes the activity recorded by tenon."},
		{fr, "registre des traitements et transferts de données (RGPD)"},
		{en, "<code>git log --grep &#34;AI-Assisted-By&#34;</code>"},
		{fr, "/work/demo"}, {fr, "2026-10-20 12:00 UTC"},
	} {
		if !strings.Contains(c.out, c.want) {
			t.Errorf("sortie sans %q", c.want)
		}
	}
}

func TestRenderHTMLHeadAndNoExternal(t *testing.T) {
	for _, r := range []Report{richReport(false), richReport(true)} {
		out := render(t, r, i18n.FR)
		head := `<head>` + "\n" + `<meta charset="utf-8">` + "\n" + cspMeta
		if !strings.Contains(out, head) {
			t.Errorf("charset puis CSP doivent ouvrir <head> :\n%s", out[:min(len(out), 400)])
		}
		low := strings.ToLower(out)
		for _, bad := range []string{"<script", "<link", "src=", "href="} {
			if strings.Contains(low, bad) {
				t.Errorf("sortie contient %q", bad)
			}
		}
		if strings.Contains(out, "</style><") {
			t.Error("chemin </style> non échappé")
		}
	}
}

func TestRenderHTMLEscapesDetails(t *testing.T) {
	out := render(t, richReport(true), i18n.EN)
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("prompt non échappé")
	}
	if !strings.Contains(out, "&lt;/style&gt;&lt;script&gt;x&lt;/script&gt;") {
		t.Error("chemin non échappé")
	}
	if !strings.Contains(out, "&lt;/style&gt;&lt;script&gt;hook&lt;/script&gt;") {
		t.Error("raison de hook non échappée")
	}
}

func TestRenderHTMLDetailsBanner(t *testing.T) {
	with := render(t, richReport(true), i18n.FR)
	without := render(t, richReport(false), i18n.FR)
	const banner = "Ce rapport contient des données de conversation."
	if !strings.Contains(with, banner) {
		t.Error("bandeau details absent")
	}
	if !strings.Contains(with, "PROMPT-RESPONSE-TEXT") || !strings.Contains(with, "ls -la") {
		t.Error("chronologie ou commandes absentes avec details")
	}
	if strings.Contains(without, banner) {
		t.Error("bandeau details sans details")
	}
	for _, s := range []string{"alert(1)", "PROMPT-RESPONSE-TEXT", "ls -la"} {
		if strings.Contains(without, s) {
			t.Errorf("%q présent sans details", s)
		}
	}
}

func TestRenderHTMLBrokenChain(t *testing.T) {
	r := richReport(false)
	r.Integrity.Intact = false
	r.Integrity.Error = "line 3: hash mismatch"
	out := render(t, r, i18n.FR)
	if !strings.Contains(out, "journal altéré : line 3: hash mismatch") {
		t.Error("bandeau chaîne cassée absent")
	}
	if !strings.Contains(out, `class="banner broken"`) {
		t.Error("bandeau rouge absent")
	}
	if strings.Contains(render(t, richReport(false), i18n.FR), `class="banner broken"`) {
		t.Error("bandeau rouge avec une chaîne intacte")
	}
}

func TestRenderHTMLMetadata(t *testing.T) {
	r := richReport(false)
	r.Integrity.Level = LevelMetadata
	r.ContentAvailable = false
	r.ExcludedMetadataSessions = 3
	out := render(t, r, i18n.FR)
	for _, want := range []string{
		"journal au niveau metadata : contenu non disponible",
		"non disponible au niveau metadata",
		"3 sessions écartées : projet inconnu au niveau metadata",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sortie sans %q", want)
		}
	}
	if strings.Contains(out, "mcp1") {
		t.Error("contenu rendu au niveau metadata")
	}
	// Les lignes illisibles restent listées à tous les niveaux.
	if !strings.Contains(out, "2026-10-01.jsonl:7") || !strings.Contains(out, "bad json") {
		t.Error("lignes illisibles absentes au niveau metadata")
	}
	// Synthèse : sessions comptées, le reste non disponible.
	if !strings.Contains(out, `<tr><td>1</td><td class="muted">non disponible au niveau metadata</td>`) {
		t.Error("synthèse metadata sans marqueur non disponible")
	}
}

func TestRenderHTMLEmptyKeepsUnreadable(t *testing.T) {
	r := Report{
		Generator: "tenon test", Period: Period{Since: "2026-10-01", Until: "2026-10-20"},
		Scope: Scope{Kind: "all"}, Disclaimer: Disclaimer, ContentAvailable: true,
		Integrity: Integrity{Intact: false, Error: "line 2: bad hash", Level: LevelNone},
		Incidents: Incidents{Unreadable: []Unreadable{{File: "2026-10-02.jsonl", Line: 9, Error: "truncated"}}},
		Mapping:   mapping(), GitHint: GitHint,
	}
	out := render(t, r, i18n.FR)
	for _, want := range []string{
		"aucune activité sur la période", "2026-10-02.jsonl:9", "truncated",
		`class="banner broken"`, "journal altéré : line 2: bad hash",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sortie sans %q", want)
		}
	}
}

func TestRenderHTMLChainWithoutHash(t *testing.T) {
	r := richReport(false)
	r.Integrity.TotalLines, r.Integrity.FinalHash = 0, ""
	out := render(t, r, i18n.FR)
	if !strings.Contains(out, "<dd>chaîne intacte : 0 lignes</dd>") {
		t.Error("ligne d'intégrité sans hash incorrecte")
	}
}

func TestRenderHTMLEmptyAndNone(t *testing.T) {
	r := Report{
		Generator: "tenon test", Period: Period{Since: "2026-10-01", Until: "2026-10-20"},
		Scope: Scope{Kind: "all"}, Disclaimer: Disclaimer, ContentAvailable: true,
		Integrity: Integrity{Intact: true, Level: LevelNone}, Mapping: mapping(), GitHint: GitHint,
	}
	out := render(t, r, i18n.FR)
	if !strings.Contains(out, "aucune activité sur la période") {
		t.Error("rapport vide sans message")
	}
	if !strings.Contains(out, "tous les projets") {
		t.Error("périmètre all absent")
	}
	if strings.Contains(out, `id="summary"`) {
		t.Error("sections de contenu rendues sans activité")
	}
	// Sections vides : "aucun".
	r.Integrity.RangeLines = 1
	r.Scope = Scope{Kind: "session", Session: "sess-42"}
	out = render(t, r, i18n.FR)
	if !strings.Contains(out, `<p class="muted">aucun</p>`) || !strings.Contains(out, "sess-42") {
		t.Error("section vide sans aucun, ou périmètre session absent")
	}
}

// TestRenderHTMLKeysExist vérifie que chaque clé report. du gabarit existe
// dans les deux langues, y compris les familles de clés dynamiques.
func TestRenderHTMLKeysExist(t *testing.T) {
	src, err := os.ReadFile("report.html.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bt "(report\.[a-z0-9_.-]+)"`).FindAllStringSubmatch(string(src), -1) {
		keys[m[1]] = true
	}
	if len(keys) < 20 {
		t.Fatalf("trop peu de clés trouvées : %d", len(keys))
	}
	for _, l := range []string{LevelFull, LevelMetadata, LevelMixed, LevelNone} {
		keys["report.level."+l] = true
	}
	for _, m := range mapping() {
		keys["report.map."+m.Element] = true
	}
	for _, j := range []string{"eu", "non-eu", "local"} {
		keys["report.juris."+j] = true
	}
	for _, k := range []string{"prompt", "response", "tool_call", "tool_result", "permission", "hook"} {
		keys["report.kind."+k] = true
	}
	keys["report.author.main"] = true
	for k := range keys {
		for _, l := range []i18n.Lang{i18n.FR, i18n.EN} {
			if strings.HasPrefix(i18n.T(l, k), "!") {
				t.Errorf("clé %q absente en %s", k, l)
			}
		}
	}
	for _, l := range []i18n.Lang{i18n.FR, i18n.EN} {
		for _, d := range []bool{false, true} {
			if out := render(t, richReport(d), l); strings.Contains(out, "!report.") {
				t.Errorf("clé inconnue rendue (%s)", l)
			}
		}
	}
}

func TestRenderHTMLMixedBanner(t *testing.T) {
	r := richReport(false)
	r.Integrity.Level = LevelMixed
	r.Summary.UnknownProviderSessions = 3
	for _, c := range []struct {
		lang         i18n.Lang
		banner, summ string
	}{
		{i18n.EN, "part of the audit log is at metadata level: 3 sessions have an unknown provider; counts and providers are incomplete", "Sessions with an unknown provider: 3"},
		{i18n.FR, "une partie du journal est au niveau metadata : 3 sessions ont un fournisseur inconnu ; décomptes et fournisseurs incomplets", "Sessions au fournisseur inconnu : 3"},
	} {
		out := render(t, r, c.lang)
		if !strings.Contains(out, `<div class="banner warn">`+c.banner+`</div>`) || !strings.Contains(out, c.summ) {
			t.Errorf("%s: banner or summary count missing:\n%s", c.lang, out)
		}
	}
	full := render(t, richReport(false), i18n.EN)
	if strings.Contains(full, "part of the audit log") || strings.Contains(full, "unknown provider") {
		t.Errorf("mixed banner at full level")
	}
}

// La clé report.git n'a pas d'argument : la commande suit dans un <code>.
func TestRenderHTMLGitHint(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.FR, i18n.EN} {
		k := i18n.T(l, "report.git")
		if strings.Contains(k, "%") {
			t.Errorf("%s: report.git has a verb: %q", l, k)
		}
		out := render(t, richReport(false), l)
		if !strings.Contains(out, "<p>"+template.HTMLEscapeString(k)+"<code>git log") {
			t.Errorf("%s: git hint not rendered", l)
		}
	}
}
