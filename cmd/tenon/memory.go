package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/memory"
	"github.com/tenon-code-eu/tenon/internal/policy"
)

// sensitiveTitle remplace le titre d'une entrée sensible dans les listes.
const sensitiveTitle = "(sensitive)"

// memEnv regroupe ce dont les sous-commandes de « tenon memory » ont besoin.
type memEnv struct {
	lang   i18n.Lang
	in     *bufio.Reader
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
	wd     string
	s      setup
	st     *memory.Store
}

// openMemoryStore construit le store selon la configuration effective :
// portée projet et rétention. Appelé seulement si la mémoire doit être lue.
func openMemoryStore(cfg config.Config, getenv func(string) string, wd string) *memory.Store {
	return memory.Open(memory.Options{
		Root: wd, DataDir: config.DataDir(getenv),
		Project: cfg.Memory.ProjectOn(), RetentionDays: cfg.Memory.Days(),
	})
}

// memoryStartup prépare la mémoire d'une session : purge des entrées
// échues, index du prompt (figé pour la session) et avis du démarrage.
// Mémoire coupée : aucun store, rien d'autre.
func memoryStartup(lang i18n.Lang, cfg config.Config, getenv func(string) string, wd string) (st *memory.Store, index string, expired []memory.Entry, notices []string) {
	if !cfg.Memory.On() {
		return nil, "", nil, nil
	}
	st = openMemoryStore(cfg, getenv, wd)
	expired, err := st.Purge()
	if err != nil {
		notices = append(notices, i18n.T(lang, "memory.purge_failed", err))
	}
	entries, listNotices := st.List()
	notices = append(notices, listNotices...)
	untrusted := 0
	for _, e := range entries {
		if !st.Trusted(e) {
			untrusted++
		}
	}
	if untrusted > 0 {
		notices = append(notices, fmt.Sprintf("memory: %d project entries not trusted — run tenon memory trust", untrusted))
	}
	index = strings.TrimRight(memory.Index(entries, st.Trusted), "\n")
	return st, index, expired, notices
}

// memoryRedact adapte le Redactor à engine.Deps.Redact : texte masqué et
// indication d'un changement.
func memoryRedact(red *policy.Redactor) func(string) (string, bool) {
	return func(s string) (string, bool) {
		out, _ := red.Redact(s)
		return out, out != s
	}
}

// memoryCommand exécute « tenon memory <sous-commande> ». list, show et
// forget fonctionnent mémoire coupée (l'effacement doit rester possible) ;
// trust est refusé.
func memoryCommand(lang i18n.Lang, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, wd, globalPath string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, i18n.T(lang, "memory.usage"))
		return 1
	}
	s, err := loadSetup(lang, getenv, wd, globalPath)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	m := memEnv{
		lang: lang, in: bufio.NewReader(stdin), stdout: stdout, stderr: stderr,
		getenv: getenv, wd: wd, s: s, st: openMemoryStore(s.cfg, getenv, wd),
	}
	switch args[0] {
	case "list":
		return m.list(args[1:])
	case "show":
		return m.show(args[1:])
	case "forget":
		return m.forget(args[1:])
	case "trust":
		return m.trust(args[1:])
	}
	fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", args[0]))
	fmt.Fprintln(stderr, i18n.T(lang, "memory.usage"))
	return 1
}

func (m memEnv) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("tenon memory "+name, flag.ContinueOnError)
	fs.SetOutput(m.stderr)
	return fs
}

// parse analyse args (options et positionnels mêlés) ; ok=false avec le code
// de sortie (0 pour -h).
func (m memEnv) parse(fs *flag.FlagSet, args []string, maxPos int) ([]string, int, bool) {
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, 0, false
		}
		return nil, 1, false
	}
	if len(pos) > maxPos {
		fmt.Fprintln(m.stderr, i18n.T(m.lang, "err.unexpected_args", pos[maxPos]))
		return nil, 1, false
	}
	return pos, 0, true
}

func (m memEnv) fail(err error) int {
	fmt.Fprintln(m.stderr, i18n.T(m.lang, "err.prefix", err))
	return 1
}

// scopeFlag lit --scope ("" : toutes les portées).
func (m memEnv) scopeFlag(v string) (memory.Scope, error) {
	if v == "" {
		return "", nil
	}
	return memory.ParseScope(v)
}

// entries renvoie les entrées lisibles (d'une portée si sc n'est pas vide) ;
// les avis des fichiers ignorés vont sur stderr.
func (m memEnv) entries(sc memory.Scope) []memory.Entry {
	all, notices := m.st.List()
	for _, n := range notices {
		fmt.Fprintln(m.stderr, stripControl(n))
	}
	var out []memory.Entry
	for _, e := range all {
		if sc == "" || e.Scope == sc {
			out = append(out, e)
		}
	}
	return out
}

// expires formate l'échéance d'une entrée ("" pour project).
func (m memEnv) expires(e memory.Entry) string {
	t := m.st.Expires(e)
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// memRow est une ligne de « tenon memory list --json ».
type memRow struct {
	Scope     string `json:"scope"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Sensitive bool   `json:"sensitive"`
	Created   string `json:"created"`
	Expires   string `json:"expires"`
}

// list affiche les entrées : portée, id, titre, auteur, création, échéance.
func (m memEnv) list(args []string) int {
	fs := m.flags("list")
	scope := fs.String("scope", "", i18n.T(m.lang, "flag.memory_scope"))
	asJSON := fs.Bool("json", false, i18n.T(m.lang, "flag.memory_json"))
	showSensitive := fs.Bool("show-sensitive", false, i18n.T(m.lang, "flag.memory_show_sensitive"))
	if _, code, ok := m.parse(fs, args, 0); !ok {
		return code
	}
	sc, err := m.scopeFlag(*scope)
	if err != nil {
		return m.fail(err)
	}
	rows := []memRow{}
	for _, e := range m.entries(sc) {
		title := e.Title
		if e.Sensitive && !*showSensitive {
			title = sensitiveTitle
		}
		rows = append(rows, memRow{
			Scope: string(e.Scope), ID: e.ID, Title: title, Author: e.Author, Sensitive: e.Sensitive,
			Created: e.Created.UTC().Format(time.RFC3339), Expires: m.expires(e),
		})
	}
	if *asJSON {
		b, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			return m.fail(err)
		}
		fmt.Fprintln(m.stdout, string(b))
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(m.stdout, i18n.T(m.lang, "memory.none"))
		return 0
	}
	tw := tabwriter.NewWriter(m.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, i18n.T(m.lang, "memory.list_header"))
	for _, r := range rows {
		exp := "-"
		if r.Expires != "" {
			exp = r.Expires[:10]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Scope, r.ID, stripControl(r.Title), stripControl(r.Author), r.Created[:10], exp)
	}
	_ = tw.Flush()
	return 0
}

// printEntry écrit les champs de l'en-tête puis le corps, caractères de
// contrôle neutralisés (le texte peut venir d'un dépôt cloné).
func (m memEnv) printEntry(e memory.Entry) {
	fmt.Fprintf(m.stdout, "scope: %s\nid: %s\ntitle: %s\nsensitive: %t\nauthor: %s\ncreated: %s\n",
		e.Scope, e.ID, stripControl(e.Title), e.Sensitive, stripControl(e.Author), e.Created.UTC().Format(time.RFC3339))
	if exp := m.expires(e); exp != "" {
		fmt.Fprintf(m.stdout, "expires: %s\n", exp)
	}
	fmt.Fprintln(m.stdout, "---")
	for _, l := range strings.Split(strings.TrimRight(e.Body, "\n"), "\n") {
		fmt.Fprintln(m.stdout, stripControl(strings.TrimSuffix(l, "\r")))
	}
}

// show affiche une entrée, même sensible : c'est une demande explicite de
// l'utilisateur, sans session.
func (m memEnv) show(args []string) int {
	fs := m.flags("show")
	scope := fs.String("scope", "", i18n.T(m.lang, "flag.memory_scope"))
	pos, code, ok := m.parse(fs, args, 1)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		fmt.Fprintln(m.stderr, i18n.T(m.lang, "memory.usage"))
		return 1
	}
	sc, err := m.scopeFlag(*scope)
	if err != nil {
		return m.fail(err)
	}
	e, err := m.st.Get(sc, pos[0])
	if errors.Is(err, memory.ErrNotFound) {
		return m.fail(errors.New(i18n.T(m.lang, "memory.not_found", pos[0])))
	}
	if err != nil {
		return m.fail(err)
	}
	m.printEntry(e)
	return 0
}

// confirm pose la question sur stderr et lit une ligne : y ou yes (casse
// indifférente) confirme.
func (m memEnv) confirm(question string) bool {
	fmt.Fprint(m.stderr, question)
	line, _ := m.in.ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// forgetTarget est une entrée à effacer, désignée par portée et id (un
// fichier illisible s'efface aussi par son id).
type forgetTarget struct {
	scope memory.Scope
	id    string
}

// forget efface une entrée (dans chaque portée où elle existe, ou dans
// --scope) ou toutes les entrées (--all), après confirmation sans --yes.
// Chaque effacement est journalisé dans une session courte « memory ».
func (m memEnv) forget(args []string) int {
	fs := m.flags("forget")
	scope := fs.String("scope", "", i18n.T(m.lang, "flag.memory_scope"))
	all := fs.Bool("all", false, i18n.T(m.lang, "flag.memory_all"))
	yes := fs.Bool("yes", false, i18n.T(m.lang, "flag.memory_yes"))
	pos, code, ok := m.parse(fs, args, 1)
	if !ok {
		return code
	}
	if *all == (len(pos) == 1) {
		fmt.Fprintln(m.stderr, i18n.T(m.lang, "memory.usage"))
		return 1
	}
	sc, err := m.scopeFlag(*scope)
	if err != nil {
		return m.fail(err)
	}
	var targets []forgetTarget
	if *all {
		for _, e := range m.entries(sc) {
			targets = append(targets, forgetTarget{e.Scope, e.ID})
		}
		if len(targets) == 0 {
			fmt.Fprintln(m.stdout, i18n.T(m.lang, "memory.none"))
			return 0
		}
	} else {
		id := pos[0]
		if err := memory.CheckID(id); err != nil {
			return m.fail(err)
		}
		scopes := []memory.Scope{memory.Project, memory.User, memory.Global}
		if sc != "" {
			scopes = []memory.Scope{sc}
		}
		for _, s := range scopes {
			// Un fichier présent mais illisible reste effaçable ; avec
			// --scope, le fichier suffit, même portée projet coupée
			// (l'effacement doit rester possible).
			_, err := m.st.Get(s, id)
			_, statErr := os.Lstat(m.st.Path(s, id))
			if !errors.Is(err, memory.ErrNotFound) || (sc != "" && statErr == nil) {
				targets = append(targets, forgetTarget{s, id})
			}
		}
		if len(targets) == 0 {
			return m.fail(errors.New(i18n.T(m.lang, "memory.not_found", id)))
		}
	}
	if !*yes {
		for _, t := range targets {
			fmt.Fprintf(m.stdout, "%s/%s\n", t.scope, t.id)
		}
		if !m.confirm(i18n.T(m.lang, "memory.forget_confirm", len(targets))) {
			fmt.Fprintln(m.stderr, i18n.T(m.lang, "memory.aborted"))
			return 1
		}
	}
	var events []journalEvent
	failed := false
	for _, t := range targets {
		if err := m.st.Forget(t.scope, t.id); err != nil {
			fmt.Fprintln(m.stderr, i18n.T(m.lang, "err.prefix", fmt.Errorf("%s/%s: %w", t.scope, t.id, err)))
			failed = true
			continue
		}
		events = append(events, journalEvent{event.MemoryForgotten, event.MemoryForgottenPayload{
			Scope: string(t.scope), ID: t.id, By: "user",
		}})
		fmt.Fprintln(m.stdout, i18n.T(m.lang, "memory.forgotten", t.scope, t.id))
		if t.scope == memory.Project {
			fmt.Fprintf(m.stderr, "memory: %s removed from the working tree; git history still holds it\n", t.id)
		}
	}
	if len(events) > 0 {
		if err := shortJournal(m.s, m.getenv, m.wd, "memory", events); err != nil {
			return m.fail(err)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// trust affiche chaque entrée project non approuvée (ou celle désignée),
// demande confirmation, puis enregistre leur empreinte. Refusé mémoire
// coupée.
func (m memEnv) trust(args []string) int {
	if !m.s.cfg.Memory.On() {
		by := m.s.cfg.MemoryDisabledBy
		if by == "" {
			by = "config"
		}
		fmt.Fprintf(m.stderr, "memory: disabled by %s\n", by)
		return 1
	}
	fs := m.flags("trust")
	all := fs.Bool("all", false, i18n.T(m.lang, "flag.memory_all"))
	pos, code, ok := m.parse(fs, args, 1)
	if !ok {
		return code
	}
	if *all && len(pos) == 1 {
		fmt.Fprintln(m.stderr, i18n.T(m.lang, "memory.usage"))
		return 1
	}
	var pending []memory.Entry
	found := false
	for _, e := range m.entries(memory.Project) {
		if len(pos) == 1 && e.ID != pos[0] {
			continue
		}
		found = true
		if !m.st.Trusted(e) {
			pending = append(pending, e)
		}
	}
	if len(pos) == 1 && !found {
		return m.fail(errors.New(i18n.T(m.lang, "memory.not_found", pos[0])))
	}
	if len(pending) == 0 {
		fmt.Fprintln(m.stdout, i18n.T(m.lang, "memory.trust_none"))
		return 0
	}
	for i, e := range pending {
		if i > 0 {
			fmt.Fprintln(m.stdout)
		}
		m.printEntry(e)
	}
	if !m.confirm(i18n.T(m.lang, "memory.trust_confirm", len(pending))) {
		fmt.Fprintln(m.stderr, i18n.T(m.lang, "memory.aborted"))
		return 1
	}
	// L'empreinte enregistrée est celle du fichier affiché : un fichier
	// modifié depuis reste non approuvé.
	var events []journalEvent
	failed := false
	for _, e := range pending {
		if err := m.st.Trust(e); err != nil {
			fmt.Fprintln(m.stderr, i18n.T(m.lang, "err.prefix", err))
			failed = true
			continue
		}
		events = append(events, journalEvent{event.MemoryTrusted, event.MemoryTrustedPayload{ID: e.ID, SHA256: e.SHA256}})
		fmt.Fprintln(m.stdout, i18n.T(m.lang, "memory.trusted", e.ID))
	}
	if len(events) > 0 {
		if err := shortJournal(m.s, m.getenv, m.wd, "memory", events); err != nil {
			return m.fail(err)
		}
	}
	if failed {
		return 1
	}
	return 0
}
