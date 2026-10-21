package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/gitmark"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/hostmatch"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/instructions"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/mcp"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/secrets"
	"github.com/tenon-code-eu/tenon/internal/session"
	"github.com/tenon-code-eu/tenon/internal/skills"
	"github.com/tenon-code-eu/tenon/internal/tools"
	"github.com/tenon-code-eu/tenon/internal/ui/headless"
	"github.com/tenon-code-eu/tenon/internal/ui/tui"
	"golang.org/x/term"
)

// startTUI lance l'interface interactive ; remplacée dans les tests.
var startTUI = tui.Run

// run exécute la CLI et retourne le code de sortie. stdin sert à
// « tenon key set » et à l'interface interactive ; isTerminal indique que
// l'entrée et la sortie standard sont des terminaux.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, wd string, isTerminal func() bool) int {
	// « tenon git mark » (hook commit-msg) passe avant toute lecture de
	// configuration : il n'a pas besoin de langue, et tout son travail reste
	// sous le délai de markTimeout.
	if len(args) >= 2 && args[0] == "git" && args[1] == "mark" {
		return gitMark(ctx, args[2:], stderr, getenv, wd)
	}
	// La langue de l'aide est connue avant l'analyse des options : --lang
	// est lu directement dans args.
	hl, _ := uiLang(argLang(args), getenv, wd)
	fs := flag.NewFlagSet("tenon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, i18n.T(hl, "flag.version"))
	prompt := fs.String("p", "", i18n.T(hl, "flag.p"))
	output := fs.String("output", string(headless.Text), i18n.T(hl, "flag.output"))
	allowNonEU := fs.Bool("allow-non-eu", false, i18n.T(hl, "flag.allow_non_eu"))
	providerID := fs.String("provider", "", i18n.T(hl, "flag.provider"))
	langFlag := fs.String("lang", "", i18n.T(hl, "flag.lang"))
	resume := fs.String("resume", "", i18n.T(hl, "flag.resume"))
	fs.Usage = func() {
		fmt.Fprintln(stderr, i18n.T(hl, "usage"))
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}

	if *showVersion {
		fmt.Fprintf(stdout, "tenon %s\n", version)
		return 0
	}

	rest := fs.Args()
	var rewindList bool
	// « tenon sessions --lang en » : --lang est aussi accepté après la sous-commande.
	if len(rest) > 0 && (rest[0] == "sessions" || rest[0] == "skills" || rest[0] == "agents" || rest[0] == "hooks" || rest[0] == "rewind") {
		sub := flag.NewFlagSet("tenon "+rest[0], flag.ContinueOnError)
		sub.SetOutput(stderr)
		sub.StringVar(langFlag, "lang", *langFlag, i18n.T(hl, "flag.lang"))
		if rest[0] == "rewind" {
			sub.BoolVar(&rewindList, "list", false, i18n.T(hl, "flag.rewind_list"))
			sub.StringVar(resume, "resume", *resume, i18n.T(hl, "flag.resume"))
		}
		if err := sub.Parse(rest[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 1
		}
		rest = append([]string{rest[0]}, sub.Args()...)
	}

	lang, ok := uiLang(*langFlag, getenv, wd)
	if !ok {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.bad_lang", *langFlag)))
		return 1
	}

	// --resume ne vaut que pour une conversation (interface interactive ou -p).
	if *resume != "" && len(rest) > 0 && rest[0] != "sessions" && rest[0] != "skills" && rest[0] != "agents" && rest[0] != "hooks" && rest[0] != "rewind" {
		fmt.Fprintln(stderr, i18n.T(lang, "err.no_prompt"))
		return 1
	}

	switch {
	case len(rest) == 2 && rest[0] == "audit" && rest[1] == "verify":
		return auditVerify(lang, stdout, stderr, getenv)
	case len(rest) >= 2 && rest[0] == "audit" && rest[1] == "export":
		return auditExport(rest[2:], *langFlag, stdout, stderr, getenv, wd)
	case len(rest) >= 2 && rest[0] == "policy" && rest[1] == "check":
		return policyCheck(lang, rest[2:], stdout, stderr)
	case len(rest) == 2 && rest[0] == "config" && rest[1] == "check":
		cs, err := loadSetup(lang, getenv, wd)
		if err == nil {
			_, _, err = loadHooks(lang, getenv, wd, cs.cfg.Hooks)
		}
		if err != nil {
			fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
			return 1
		}
		// Politique, profil et listes blanches effectifs, puis conformité du
		// fournisseur sélectionné (profil, puis listes blanches d'hôtes).
		if l := policyLine(lang, "config.policy", cs.cfg.Policy); l != "" {
			fmt.Fprintln(stdout, l)
		}
		if n := cs.cfg.Profile; n != profile.None {
			fmt.Fprintln(stdout, i18n.T(lang, "config.profile", n))
		}
		id := *providerID
		if id == "" {
			id = cs.cfg.DefaultProvider
		}
		if l := allowlistLine(lang, cs.cfg); l != "" {
			fmt.Fprintln(stdout, l)
		}
		if pc, ok := cs.cfg.Provider(id); ok {
			if err := providerErr(lang, cs.cfg, pc); err != nil {
				// Comme au démarrage : les avis expliquent le refus.
				for _, n := range profileNotices(lang, cs.cfg) {
					fmt.Fprintln(stderr, n)
				}
				fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
				return 1
			}
		}
		fmt.Fprintln(stdout, i18n.T(lang, "config.ok"))
		return 0
	case len(rest) >= 1 && rest[0] == "sessions":
		if len(rest) > 1 {
			fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", rest[1]))
			return 1
		}
		// Seul --lang accompagne « tenon sessions ».
		var extra string
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "lang" && extra == "" {
				extra = "-" + f.Name
			}
		})
		if extra != "" {
			fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", extra))
			return 1
		}
		return listSessions(lang, stdout, stderr, getenv, wd)
	case len(rest) >= 1 && rest[0] == "skills":
		if len(rest) > 1 {
			fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", rest[1]))
			return 1
		}
		return listSkills(lang, stdout, getenv, wd)
	case len(rest) >= 1 && rest[0] == "agents":
		if len(rest) > 1 {
			fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", rest[1]))
			return 1
		}
		return listAgents(lang, stdout, getenv, wd)
	case len(rest) >= 1 && rest[0] == "hooks":
		if len(rest) > 1 {
			fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", rest[1]))
			return 1
		}
		return listHooks(lang, stdout, stderr, getenv, wd)
	case len(rest) >= 1 && rest[0] == "rewind":
		// La restauration elle-même se fait dans l'interface interactive.
		if len(rest) > 1 || !rewindList || *resume == "" {
			arg := rest[0]
			if len(rest) > 1 {
				arg = rest[1]
			}
			fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", arg))
			return 1
		}
		return listRewind(lang, *resume, stdout, stderr, getenv, wd)
	case len(rest) >= 2 && rest[0] == "git" && rest[1] == "mark":
		// Précédé d'options globales (le hook n'en passe aucune).
		return gitMark(ctx, rest[2:], stderr, getenv, wd)
	case len(rest) >= 2 && rest[0] == "git" && rest[1] == "install-hook":
		return gitInstallHook(lang, rest[2:], stdout, stderr, wd)
	case len(rest) == 3 && rest[0] == "key" && (rest[1] == "set" || rest[1] == "delete"):
		return keyCommand(lang, rest[1], rest[2], stdin, stdout, stderr, getenv, wd)
	case len(rest) > 0:
		fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", rest[0]))
		return 1
	}

	if *prompt == "" {
		return runInteractive(ctx, fs, lang, engineOptions{
			ctx: ctx, getenv: getenv, wd: wd, providerID: *providerID, allowNonEU: *allowNonEU,
			lang: lang, resume: *resume, stderr: stderr, interactive: true,
		}, stdin, stdout, stderr, isTerminal)
	}
	out := headless.Output(*output)
	if out != headless.Text && out != headless.JSON {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.bad_output", *output)))
		return 1
	}

	// Skills et commande slash avant startEngine : un /<inconnu> ne crée ni
	// session, ni audit, ni verrou, et ne dépend d'aucune clé.
	disc := discoverSkills(lang, getenv, wd)
	in, err := headlessInput(lang, disc.cat, *prompt)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	agentsDisc := discoverAgents(lang, getenv, wd)
	st, err := startEngine(engineOptions{
		ctx: ctx, getenv: getenv, wd: wd, providerID: *providerID, allowNonEU: *allowNonEU,
		lang: lang, resume: *resume, stderr: stderr, skills: &disc, agents: &agentsDisc,
	})
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	// headless.Run ne rend la main qu'à la fin du tour : serveurs MCP puis
	// sinks sont fermés ensuite.
	defer st.close()
	red := st.redactor
	for _, n := range st.notices {
		fmt.Fprintln(stderr, redactText(red)(n))
	}
	code := headless.Run(ctx, st.eng, st.sessionID, in, headless.Options{
		Output:     out,
		Lang:       lang,
		Redact:     func(s string) string { out, _ := red.RedactJSON(s); return out },
		RedactText: redactText(red),
	}, stdout, stderr)
	// Les sinks sont encore ouverts : les événements du hook SessionEnd y
	// sont écrits.
	endSession(st, lang, "other", out == headless.Text, stderr)
	return code
}

// endSessionTimeout borne l'attente des hooks SessionEnd.
const endSessionTimeout = 10 * time.Second

// endSession déclenche les hooks SessionEnd une fois le tour rendu, avant la
// fermeture des sinks. Une erreur n'est affichée que si showErr (sortie texte).
func endSession(st started, lang i18n.Lang, reason string, showErr bool, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), endSessionTimeout)
	defer cancel()
	if err := st.eng.EndSession(ctx, st.sessionID, reason); err != nil && showErr {
		fmt.Fprint(stderr, redactText(st.redactor)(i18n.T(lang, "err.prefix", err)+"\n"))
	}
}

// redactText masque un texte libre avec red : même fonction pour le mode non
// interactif et la TUI.
func redactText(red *policy.Redactor) func(string) string {
	return func(s string) string { out, _ := red.Redact(s); return out }
}

// runInteractive ouvre l'interface interactive (tenon sans -p). --output et
// l'absence de terminal sont refusés avant toute autre opération.
func runInteractive(ctx context.Context, fs *flag.FlagSet, lang i18n.Lang, o engineOptions, stdin io.Reader, stdout, stderr io.Writer, isTerminal func() bool) int {
	output := false
	fs.Visit(func(f *flag.Flag) { output = output || f.Name == "output" })
	if output {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.output_requires_p")))
		return 1
	}
	if isTerminal == nil || !isTerminal() {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.no_tty")))
		return 1
	}

	st, err := startEngine(o)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	// Serveurs MCP puis sinks sont fermés après le retour de la TUI, qui
	// attend la fin du tour en cours.
	defer st.close()
	err = startTUI(ctx, st.eng, tui.Options{
		Lang:          lang,
		SessionID:     st.sessionID,
		Provider:      st.provider,
		ContextWindow: st.contextWindow,
		ProjectRoot:   o.wd,
		Version:       version,
		Profile:       string(st.profile),
		Policy:        st.policy,
		Redact:        redactText(st.redactor),
		Skills:        st.skills,
		Agents:        st.agents,
		Hooks:         st.hooks,
		// Serveurs indisponibles signalés dès l'ouverture, en plus de
		// l'événement McpServerDown émis au premier tour.
		Notices: append(slices.Clone(st.notices), st.downNotices...),
	}, stdin, stdout)
	// Après le retour de la TUI (même en erreur), avant la fermeture des sinks.
	// Un tour inachevé tient le verrou de session : EndSession expire alors.
	endSession(st, lang, "prompt_input_exit", true, stderr)
	switch {
	case errors.Is(err, tui.ErrTurnNotFinished):
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.turn_not_finished")))
		return 1
	case err != nil:
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	return 0
}

// argLang renvoie la valeur de -lang/--lang dans args (forme séparée ou
// avec « = »), ou "" ; l'analyse s'arrête à « -- ».
func argLang(args []string) string {
	for i, a := range args {
		if a == "--" {
			break
		}
		switch {
		case a == "-lang" || a == "--lang":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-lang="):
			return strings.TrimPrefix(a, "-lang=")
		case strings.HasPrefix(a, "--lang="):
			return strings.TrimPrefix(a, "--lang=")
		}
	}
	return ""
}

// uiLang renvoie la langue de l'interface : --lang, sinon lang de la
// configuration (anglais par défaut). Une configuration
// illisible n'empêche pas de choisir une langue : son erreur sera signalée
// plus loin. ok est faux si --lang n'est ni fr ni en.
func uiLang(flagValue string, getenv func(string) string, wd string) (i18n.Lang, bool) {
	cfgLang := config.Defaults(getenv).Lang
	// La politique (au mieux) peut définir le fournisseur par défaut : sans
	// elle, une telle configuration serait refusée et sa langue perdue.
	pol, err := orgpolicy.Load(orgpolicy.Resolve(getenv))
	if err != nil {
		pol = nil
	}
	if cfg, err := config.LoadPolicy(config.GlobalPath(getenv), filepath.Join(wd, ".tenon", "config.toml"), getenv, pol); err == nil {
		cfgLang = cfg.Lang
	}
	fallback := i18n.Parse(cfgLang)
	switch flagValue {
	case "":
		return fallback, true
	case string(i18n.FR), string(i18n.EN):
		return i18n.Lang(flagValue), true
	}
	return fallback, false
}

// sessionsDir est le dossier des sessions persistées.
func sessionsDir(getenv func(string) string) string {
	return filepath.Join(config.DataDir(getenv), "sessions")
}

// listSessions affiche les sessions du projet wd, la plus récente d'abord.
func listSessions(lang i18n.Lang, stdout, stderr io.Writer, getenv func(string) string, wd string) int {
	l, err := session.List(sessionsDir(getenv), wd)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	if len(l) == 0 {
		fmt.Fprintln(stdout, i18n.T(lang, "sessions.none"))
		return 0
	}
	fmt.Fprintln(stdout, i18n.T(lang, "sessions.header", len(l)))
	for _, s := range l {
		fmt.Fprintf(stdout, "%s  %s  %d  %s\n", s.ID, s.Updated.Local().Format("2006-01-02 15:04"), s.Turns, s.FirstPrompt)
	}
	return 0
}

// keyCommand enregistre (set) ou supprime (delete) la clé d'un fournisseur
// configuré dans le trousseau système.
func keyCommand(lang i18n.Lang, action, providerID string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, wd string) int {
	s, err := loadSetup(lang, getenv, wd)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	if _, ok := s.cfg.Provider(providerID); !ok {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.unknown_provider", providerID)))
		return 1
	}
	if action == "delete" {
		if err := secrets.Delete(providerID); err != nil {
			fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
			return 1
		}
		fmt.Fprintln(stdout, i18n.T(lang, "key.deleted", providerID))
		return 0
	}
	key, err := readKey(lang, providerID, stdin, stderr)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	if key == "" {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.empty_key")))
		return 1
	}
	if err := secrets.Set(providerID, key); err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	fmt.Fprintln(stdout, i18n.T(lang, "key.saved", providerID))
	return 0
}

// readKey lit une ligne sur stdin : sans écho après une invite sur stderr si
// stdin est un terminal, telle quelle sinon (tube, redirection).
func readKey(lang i18n.Lang, providerID string, stdin io.Reader, stderr io.Writer) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(stderr, i18n.T(lang, "key.prompt", providerID))
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", errors.New(i18n.T(lang, "err.read_key", err))
		}
		return strings.TrimSpace(string(b)), nil
	}
	if stdin == nil {
		return "", errors.New(i18n.T(lang, "err.read_key", i18n.T(lang, "err.no_stdin")))
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New(i18n.T(lang, "err.read_key", err))
	}
	return strings.TrimSpace(line), nil
}

// setup est la configuration chargée et convertie pour la politique.
type setup struct {
	cfg      config.Config
	rules    []policy.Rule
	redactor *policy.Redactor
}

// loadSetup charge la configuration globale et projet, y applique la
// politique d'organisation (plancher), puis la convertit en règles de
// permissions et en Redactor.
func loadSetup(lang i18n.Lang, getenv func(string) string, wd string) (setup, error) {
	// La politique est chargée avant la configuration : une politique
	// illisible ou mal signée bloque le démarrage, quel que soit l'état de la
	// configuration. Seul le répertoire système est contrôlé (propriétaire
	// root, droits).
	dir, system := orgpolicy.Resolve(getenv)
	pol, err := orgpolicy.Load(dir, system)
	if err != nil {
		return setup{}, errors.New(i18n.T(lang, "err.policy_load", err))
	}
	// Le fournisseur par défaut est contrôlé après la politique : il peut être
	// l'un de ses providers.define.
	cfg, err := config.LoadPolicy(config.GlobalPath(getenv), filepath.Join(wd, ".tenon", "config.toml"), getenv, pol)
	if err != nil {
		return setup{}, err
	}
	rules := make([]policy.Rule, 0, len(cfg.Permissions.Rules))
	for _, r := range cfg.Permissions.Rules {
		d, err := policy.ParseDecision(r.Decision)
		if err != nil {
			return setup{}, fmt.Errorf("permission rule (%s %q): %w", r.Tool, r.Match, err)
		}
		// Une règle de la politique se comporte comme une règle projet : ses
		// ask priment les allow de la configuration.
		rules = append(rules, policy.Rule{Tool: r.Tool, Match: r.Match, Decision: d, Project: r.Project || r.Policy})
	}
	custom := make([]policy.RedactionRule, 0, len(cfg.Redaction.Rules))
	for _, r := range cfg.Redaction.Rules {
		custom = append(custom, policy.RedactionRule{Name: r.Name, Pattern: r.Pattern})
	}
	red, err := policy.NewRedactor(custom)
	if err != nil {
		return setup{}, err
	}
	// Les clés d'API sans préfixe reconnaissable (printenv, echo $KEY) sont
	// masquées à l'identique ; l'environnement du shell n'est pas modifié.
	for _, p := range cfg.Providers {
		if p.APIKeyEnv == "" {
			continue
		}
		if v := getenv(p.APIKeyEnv); len(v) >= 8 {
			red.AddLiteral(v)
		}
	}
	return setup{cfg: cfg, rules: rules, redactor: red}, nil
}

// engineOptions paramètre startEngine.
type engineOptions struct {
	// ctx borne la connexion aux serveurs MCP (nil : context.Background).
	ctx        context.Context
	getenv     func(string) string
	wd         string
	providerID string
	allowNonEU bool
	lang       i18n.Lang
	// resume : "" (nouvelle session), "last" ou un identifiant de session.
	resume string
	// stderr reçoit les avertissements (trousseau, session endommagée).
	stderr io.Writer
	// interactive : interface interactive (TUI). Un appel à un serveur MCP
	// hors UE y est validé par l'utilisateur (ForceAsk) ; sans interface, il
	// exige --allow-non-eu.
	interactive bool
	// skills : skills déjà découverts (nil : startEngine les découvre).
	skills *discovered
	// agents : agents déjà découverts (nil : startEngine les découvre).
	agents *discoveredAgents
}

// started est un Engine prêt pour la session sessionID.
type started struct {
	eng       *engine.Engine
	redactor  *policy.Redactor // celui de l'audit, de la session et d'Outbound
	sessionID string
	// close ferme les serveurs MCP, puis le journal d'audit et le fichier de
	// session ; à appeler après la fin du tour.
	close func()
	// notices sont les avertissements du démarrage (fournisseur local
	// détecté, MCP), déjà traduits.
	notices []string
	// downNotices signalent les serveurs MCP indisponibles au démarrage, déjà
	// traduits ; le mode non interactif les affiche via l'événement McpServerDown.
	downNotices []string
	// skills est le catalogue des skills découverts (jamais nil).
	skills *skills.Catalog
	// agents est le catalogue des agents (jamais nil).
	agents *agents.Catalog
	// hooks est l'ensemble des hooks chargés (jamais nil).
	hooks *hooks.Set
	// provider et contextWindow décrivent le fournisseur choisi (barre d'état).
	provider      llm.ProviderInfo
	contextWindow int
	// profile est le profil de conformité effectif (écran de bienvenue).
	profile profile.Name
	// policy est la ligne de la politique d'organisation, déjà traduite
	// (écran de bienvenue ; "" sans politique).
	policy string
}

// providerInfo décrit le fournisseur configuré pc.
func providerInfo(pc config.Provider) llm.ProviderInfo {
	return llm.ProviderInfo{
		ID: pc.ID, Name: pc.Name, BaseURL: pc.BaseURL, Model: pc.Model,
		Country: pc.Country, Jurisdiction: llm.Jurisdiction(pc.Jurisdiction),
		Qualifications: slices.Clone(pc.Qualifications),
	}
}

// profileJurisdiction rend une juridiction lisible (« inconnue » si vide).
func profileJurisdiction(lang i18n.Lang, j string) string {
	if j == "" {
		return i18n.T(lang, "profile.jurisdiction_unknown")
	}
	return j
}

// urlHost renvoie l'hôte d'une URL (vide si illisible).
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// profileProviderErr contrôle le fournisseur info contre le profil n et
// renvoie l'erreur traduite (nil s'il est conforme). profile.CheckProvider
// décide ; le message est reconstruit ici depuis les valeurs connues : sous
// strict-local ou secnumcloud, un fournisseur local ne peut échouer que par
// son hôte ; sous secnumcloud, un fournisseur eu que faute de qualification.
func profileProviderErr(lang i18n.Lang, n profile.Name, info llm.ProviderInfo) error {
	if profile.CheckProvider(n, info) == nil {
		return nil
	}
	var msg string
	switch {
	case (n == profile.StrictLocal || n == profile.SecNumCloud) && info.Jurisdiction == llm.Local:
		msg = i18n.T(lang, "err.profile_host", n, info.ID, urlHost(info.BaseURL))
	case n == profile.SecNumCloud && info.Jurisdiction == llm.EU:
		msg = i18n.T(lang, "err.profile_unqualified", n, info.ID)
	default:
		msg = i18n.T(lang, "err.profile_provider", n, info.ID, profileJurisdiction(lang, string(info.Jurisdiction)))
	}
	if n == profile.StrictLocal {
		hint := "profile.hint_local"
		if info.Jurisdiction == llm.Local {
			hint = "profile.hint_local_url"
		}
		msg += " (" + i18n.T(lang, hint) + ")"
	}
	return errors.New(msg)
}

// policyProviderErr contrôle l'hôte du fournisseur info contre la liste
// blanche de la politique pol et renvoie l'erreur traduite (nil s'il est admis
// ou sans politique).
func policyProviderErr(lang i18n.Lang, pol *orgpolicy.Policy, info llm.ProviderInfo) error {
	h := orgpolicy.HostOf(info.BaseURL)
	if pol.AllowsHost(h) {
		return nil
	}
	return errors.New(i18n.T(lang, "err.policy_provider", info.ID, h, pol.ID))
}

// allowlists renvoie les listes blanches de fournisseurs de cfg : globale
// puis projet, les listes absentes omises.
func allowlists(cfg config.Config) []hostmatch.List {
	var out []hostmatch.List
	for _, a := range []struct {
		name string
		l    *config.ProviderAllowlist
	}{{"global", cfg.Allowlist}, {"project", cfg.ProjectAllowlist}} {
		if a.l != nil {
			out = append(out, hostmatch.List{Name: a.name, Hosts: slices.Clone(a.l.Hosts), Local: a.l.Local})
		}
	}
	return out
}

// allowlistProviderErr contrôle l'hôte du fournisseur info contre chaque
// liste blanche de cfg et renvoie l'erreur traduite de la première qui le
// refuse (nil s'il est admis partout ou sans liste).
func allowlistProviderErr(lang i18n.Lang, cfg config.Config, info llm.ProviderInfo) error {
	h := hostmatch.HostOf(info.BaseURL)
	for _, l := range allowlists(cfg) {
		if !l.Allows(h) {
			return errors.New(i18n.T(lang, "err.allowlist_provider", info.ID, h, i18n.T(lang, "allowlist.name_"+l.Name)))
		}
	}
	return nil
}

// allowlistLine renvoie la ligne « config check » des listes blanches de
// cfg ("" sans liste).
func allowlistLine(lang i18n.Lang, cfg config.Config) string {
	if cfg.Allowlist == nil && cfg.ProjectAllowlist == nil {
		return ""
	}
	side := func(a *config.ProviderAllowlist) (n, local string) {
		if a == nil {
			return "-", ""
		}
		if a.Local {
			local = " + local"
		}
		return strconv.Itoa(len(a.Hosts)), local
	}
	gn, gl := side(cfg.Allowlist)
	pn, pl := side(cfg.ProjectAllowlist)
	return i18n.T(lang, "config.allowlist", gn, gl, pn, pl)
}

// providerNoProxy dit si le fournisseur pc doit ignorer les variables de
// proxy (HTTP_PROXY, HTTPS_PROXY) sous le profil n : strict-local, ou
// secnumcloud avec un fournisseur local. Sinon une adresse privée acceptée
// par le profil sortirait par un proxy.
func providerNoProxy(n profile.Name, pc config.Provider) bool {
	return n == profile.StrictLocal || (n == profile.SecNumCloud && pc.Jurisdiction == string(llm.Local))
}

// mcpNoProxy dit si le gestionnaire MCP ignore les variables de proxy sous
// le profil n. L'option vaut pour tous ses serveurs : secnumcloud l'active
// car il admet des serveurs http locaux.
func mcpNoProxy(n profile.Name) bool {
	return n == profile.StrictLocal || n == profile.SecNumCloud
}

// providerErr applique au fournisseur pc les contrôles d'avant connexion :
// profil, liste blanche de la politique, puis listes blanches de la config.
func providerErr(lang i18n.Lang, cfg config.Config, pc config.Provider) error {
	info := providerInfo(pc)
	if err := profileProviderErr(lang, cfg.Profile, info); err != nil {
		return err
	}
	if err := policyProviderErr(lang, cfg.Policy, info); err != nil {
		return err
	}
	return allowlistProviderErr(lang, cfg, info)
}

// policyLine renvoie la ligne d'identification de la politique pol sous la
// clé de traduction key ("" sans politique).
func policyLine(lang i18n.Lang, key string, pol *orgpolicy.Policy) string {
	if pol == nil {
		return ""
	}
	return i18n.T(lang, key, pol.ID, pol.Version, pol.KeyID)
}

// profileNotices renvoie les avis du profil et de la politique de cfg,
// affichés au démarrage et avant un refus du fournisseur.
func profileNotices(lang i18n.Lang, cfg config.Config) []string {
	var out []string
	if cfg.WeakerProjectProfile != profile.None {
		// La valeur projet, plus faible, a été ignorée : cfg.Profile est le profil global.
		out = append(out, i18n.T(lang, "profile.weaker_project", cfg.WeakerProjectProfile, cfg.Profile))
	}
	return append(out, policyNotices(lang, cfg)...)
}

// policyNotices renvoie les avis du durcissement de cfg par sa politique.
func policyNotices(lang i18n.Lang, cfg config.Config) []string {
	pol := cfg.Policy
	if pol == nil {
		return nil
	}
	var out []string
	if cfg.PolicyRaisedProfile != profile.None {
		out = append(out, i18n.T(lang, "policy.profile_raised", cfg.PolicyRaisedProfile, cfg.Profile, pol.ID))
	}
	for _, id := range cfg.PolicyOverriddenProviders {
		out = append(out, i18n.T(lang, "policy.provider_overridden", pol.ID, id))
	}
	if cfg.PolicyAuditFrom != "" {
		out = append(out, i18n.T(lang, "policy.audit_imposed", pol.ID, cfg.Audit.Level, cfg.PolicyAuditFrom))
	}
	if ids := cfg.PolicyStrippedQualifications; len(ids) > 0 {
		out = append(out, i18n.T(lang, "policy.qualifications_stripped", pol.ID, strings.Join(ids, ", ")))
	}
	return out
}

// profileMCPSkip contrôle le serveur MCP s contre le profil n et renvoie
// l'avis traduit de son exclusion ("" s'il est conforme). Comme pour le
// fournisseur, seul un serveur local non stdio peut échouer par son hôte.
func profileMCPSkip(lang i18n.Lang, n profile.Name, s config.MCPServer) string {
	t := profile.MCPTarget{ID: s.ID, Transport: s.Transport, URL: s.URL, Jurisdiction: s.Jurisdiction, Qualifications: s.Qualifications}
	if profile.CheckMCP(n, t) == nil {
		return ""
	}
	reason := profileJurisdiction(lang, s.Jurisdiction)
	switch {
	case (n == profile.StrictLocal || n == profile.SecNumCloud) && s.Jurisdiction == string(llm.Local):
		reason = i18n.T(lang, "profile.reason_host", urlHost(s.URL))
	case n == profile.SecNumCloud && s.Jurisdiction == string(llm.EU):
		reason = i18n.T(lang, "profile.reason_unqualified")
	}
	return i18n.T(lang, "profile.mcp_skipped", n, s.ID, reason)
}

// startEngine assemble configuration, fournisseur, politique, outils, audit et
// session ; une session reprise est restaurée dans l'Engine.
func startEngine(o engineOptions) (started, error) {
	getenv, wd, lang := o.getenv, o.wd, o.lang
	s, err := loadSetup(lang, getenv, wd)
	if err != nil {
		return started{}, err
	}
	cfg := s.cfg
	// Sous un profil, --allow-non-eu est refusé avant toute autre étape.
	if o.allowNonEU && cfg.Profile != profile.None {
		return started{}, errors.New(i18n.T(lang, "err.profile_allow_non_eu", cfg.Profile))
	}
	startNotices := profileNotices(lang, cfg)
	hookSet, hookNotices, err := loadHooks(lang, getenv, wd, cfg.Hooks)
	if err != nil {
		return started{}, err
	}

	providerID := o.providerID
	if providerID == "" {
		providerID = cfg.DefaultProvider
	}
	pc, ok := cfg.Provider(providerID)
	if !ok {
		return started{}, errors.New(i18n.T(lang, "err.unknown_provider", providerID))
	}
	// Profil, puis listes blanches (politique, globale, projet), avant toute
	// connexion : la détection d'un modèle local contacterait l'hôte
	// configuré. Les avis de la politique sont affichés avant le refus : ils
	// l'expliquent (qualification ignorée, profil relevé).
	if err := providerErr(lang, cfg, pc); err != nil {
		if o.stderr != nil {
			for _, n := range startNotices {
				fmt.Fprintln(o.stderr, n)
			}
		}
		return started{}, err
	}
	pc, detectNotices, err := detectLocal(o.ctx, lang, pc, providerNoProxy(cfg.Profile, pc))
	if err != nil {
		return started{}, err
	}
	info := providerInfo(pc)
	// Environnement d'abord, puis trousseau. Un trousseau illisible ne bloque
	// pas : la clé est considérée absente. Les clés de TOUS les fournisseurs
	// configurés sont masquées (un outil peut afficher celle d'un autre) :
	// celles de l'environnement par loadSetup, celles du trousseau ici, sous
	// leur forme décodée et sous les formes encodées de go-keyring.
	var apiKey string
	var keyErr error
	for _, p := range cfg.Providers {
		v, source, err := providerKey(p, getenv)
		if p.ID == pc.ID {
			apiKey, keyErr = v, err
		}
		if err == nil && source == "keyring" {
			s.redactor.AddKeyringSecret(v)
		}
	}
	if keyErr != nil {
		fmt.Fprintln(o.stderr, i18n.T(lang, "warn.keyring", keyErr))
		apiKey = ""
	}
	if apiKey == "" && info.Jurisdiction != llm.Local {
		if pc.APIKeyEnv == "" {
			return started{}, errors.New(i18n.T(lang, "err.no_api_key_env", pc.ID))
		}
		return started{}, errors.New(i18n.T(lang, "err.missing_env", pc.APIKeyEnv))
	}

	files, err := instructions.Load(wd)
	if err != nil {
		return started{}, errors.New(i18n.T(lang, "err.instructions", err))
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}

	// La session à reprendre est verrouillée avant d'être lue (aucun autre
	// processus ne l'écrit pendant la lecture), puis lue avant l'ouverture du
	// writer, qui tronque un fichier endommagé à sa première ligne illisible.
	sdir := sessionsDir(getenv)
	var sid string
	var events []event.Event
	var lock *session.Lock
	defer func() {
		// Sans effet une fois le verrou confié au writer.
		if lock != nil {
			lock.Release()
		}
	}()
	lockErr := func(err error) error {
		if errors.Is(err, session.ErrLocked) {
			return errors.New(i18n.T(lang, "err.session_locked", sid))
		}
		return err
	}
	switch o.resume {
	case "":
		if sid, err = newSessionID(); err != nil {
			return started{}, err
		}
	case "last":
		if sid, err = session.Latest(sdir, wd); err != nil {
			if errors.Is(err, session.ErrNoSession) {
				return started{}, errors.New(i18n.T(lang, "err.no_session_project"))
			}
			return started{}, err
		}
	default:
		sid = o.resume
	}
	if o.resume != "" {
		resumeErr := func(err error) error {
			switch {
			case errors.Is(err, session.ErrInvalidID):
				return errors.New(i18n.T(lang, "err.invalid_session_id", sid))
			case errors.Is(err, session.ErrNoSession):
				return errors.New(i18n.T(lang, "err.no_session", sid))
			}
			return fmt.Errorf("session %s : %w", sid, err)
		}
		// Une session absente ne crée ni dossier ni verrou.
		if err := session.Exists(sdir, sid); err != nil {
			return started{}, resumeErr(err)
		}
		if lock, err = session.Acquire(sdir, sid); err != nil {
			return started{}, lockErr(err)
		}
		var corrupt bool
		events, corrupt, err = session.Load(sdir, sid)
		if err != nil {
			return started{}, resumeErr(err)
		}
		// Comme List et Latest, la reprise est limitée aux sessions du projet
		// courant : une session d'un autre projet est traitée comme absente,
		// avant toute ouverture de son fichier ou requête au fournisseur.
		if !sameProject(events, wd) {
			return started{}, errors.New(i18n.T(lang, "err.no_session", sid))
		}
		if corrupt {
			fmt.Fprintln(o.stderr, i18n.T(lang, "resume.warning_corrupt", skippedLines(sdir, sid, len(events)), sid))
		}
		// Les compteurs du Redactor sont propres au processus : ceux des
		// marqueurs déjà présents dans la session sont réservés, sinon un
		// nouveau secret recevrait le marqueur d'un autre.
		reserveMarkers(s.redactor, events)
	}

	perm, err := policy.NewPermissions(wd, s.rules)
	if err != nil {
		return started{}, err
	}
	out := policy.NewOutbound(s.redactor)
	if o.allowNonEU {
		out.Consent(info.ID)
	}

	// grep et glob ne révèlent que les fichiers que read_file pourrait lire
	// sans demande : jamais .env*, *.pem, id_rsa* ni un fichier refusé.
	readable := func(tool, abs string) bool {
		target := tools.Target{Paths: []string{abs}}
		return perm.Check(policy.Call{Tool: tool, Risk: tools.Read, Target: target}) == policy.Allow &&
			perm.Check(policy.Call{Tool: "read_file", Risk: tools.Read, Target: target}) == policy.Allow
	}
	// Copies d'avant écriture (rewind) : ouvertes et nettoyées au démarrage.
	store, ckNotices := openCheckpoints(lang, getenv, cfg.Checkpoints.RetentionDays)
	fsOpts := []tools.Option{tools.WithReadFilter(readable)}
	if store != nil {
		fsOpts = append(fsOpts, tools.WithSnapshot(func(_ string, old []byte) error {
			_, err := store.Save(old)
			return err
		}))
	} else {
		// Copies désactivées ou magasin indisponible : chaque écriture d'un
		// fichier existant porte no_checkpoint.
		fsOpts = append(fsOpts, tools.WithoutCheckpoints())
	}
	ts := append(tools.FS(wd, cfg.OutputLimit, fsOpts...),
		tools.Shell(wd, cfg.ShellTimeout.Duration, cfg.OutputLimit))
	// Skills : l'outil skill rejoint les outils internes, avant ceux de MCP.
	disc := o.skills
	if disc == nil {
		d := discoverSkills(lang, getenv, wd)
		disc = &d
	}
	cat, skillsSection, skillNotices := disc.cat, disc.section, disc.notices
	ts = append(ts, skills.NewTool(cat, cfg.OutputLimit))
	ad := o.agents
	if ad == nil {
		d := discoverAgents(lang, getenv, wd)
		ad = &d
	}
	base := instructions.Render(i18n.SystemPrompt(lang), files)

	red := s.redactor
	redactJSON := func(s string) string { out, _ := red.RedactJSON(s); return out }
	w, err := audit.Open(filepath.Join(config.DataDir(getenv), "audit"), audit.Level(cfg.Audit.Level), redactJSON)
	if err != nil {
		return started{}, err
	}
	if lock == nil {
		if lock, err = session.Acquire(sdir, sid); err != nil {
			_ = w.Close()
			return started{}, lockErr(err)
		}
	}
	sw, err := lock.Open(redactJSON)
	if err != nil {
		_ = w.Close()
		return started{}, err
	}
	// Serveurs MCP (configuration globale seulement), démarrés après les
	// étapes qui peuvent échouer sans eux.
	mgr, unavailable, active, notices := startMCP(o, cfg, red)
	closeAll := func() {
		// Serveurs MCP d'abord : un appel en cours ne peut plus écrire dans
		// des sinks fermés.
		// liveMCP n'est retiré qu'après Close : un second Ctrl+C pendant le
		// délai SIGTERM → SIGKILL atteint encore Kill.
		_ = mgr.Close()
		liveMCP.CompareAndSwap(mgr, nil)
		_ = sw.Close()
		_ = w.Close()
	}
	// Outils MCP après les outils internes : leur préfixe mcp__ ne peut
	// masquer aucun outil interne.
	ts = append(ts, mgr.Tools()...)

	// strict-local, ou secnumcloud avec un fournisseur local : le
	// fournisseur ignore les variables de proxy (voir providerNoProxy).
	var provOpts []provider.Option
	if providerNoProxy(cfg.Profile, pc) {
		provOpts = append(provOpts, provider.WithoutProxy())
	}

	eng := engine.New(engine.Deps{
		Provider:            provider.NewOpenAICompat(info, apiKey, provOpts...),
		Tools:               tools.NewRegistry(ts...),
		Permissions:         perm,
		Outbound:            out,
		Sinks:               []event.Sink{w, sw}, // audit d'abord, puis la session
		MaxIterations:       cfg.MaxIterations,
		SystemPrompt:        systemPrompt(base, skillsSection, ad.section),
		BasePrompt:          base,
		SkillsSection:       skillsSection,
		Agents:              ad.cat,
		Lang:                lang,
		OutputLimit:         cfg.OutputLimit,
		ProjectRoot:         wd,
		Instructions:        names,
		ContextWindow:       pc.Window(),
		CompactionThreshold: cfg.CompactionThreshold,
		UnavailableMCP:      unavailable,
		MCP:                 active,
		Hooks:               hookSet,
		Checkpoints:         store,
		TranscriptPath:      filepath.Join(sdir, sid+".jsonl"),
		Notify:              o.interactive,
		Profile:             cfg.Profile,
		Policy:              cfg.Policy,
		Allowlists:          allowlists(cfg),
	})
	if o.resume != "" {
		if err := eng.Restore(sid, events); err != nil {
			closeAll()
			return started{}, fmt.Errorf("session %s : %w", sid, err)
		}
	}
	return started{
		eng: eng, redactor: red, sessionID: sid, close: closeAll, notices: slices.Concat(startNotices, detectNotices, skillNotices, ad.notices, hookNotices, ckNotices, notices), skills: cat, agents: ad.cat, hooks: hookSet,
		downNotices: downNotices(o.lang, unavailable, red),
		provider:    info, contextWindow: pc.Window(), profile: cfg.Profile,
		policy: policyLine(lang, "tui.welcome.policy", cfg.Policy),
	}, nil
}

// downNotices renvoie l'avis de chaque serveur MCP indisponible au démarrage,
// au format de l'événement McpServerDown.
func downNotices(l i18n.Lang, unavailable []event.McpServerDownPayload, red *policy.Redactor) []string {
	out := make([]string, 0, len(unavailable))
	for _, u := range unavailable {
		out = append(out, headless.ServerDownNotice(l, u, redactText(red)))
	}
	return out
}

// liveMCP est le Manager des serveurs MCP ouverts, tués par killLiveMCP lors
// d'une sortie immédiate (second Ctrl+C).
var liveMCP atomic.Pointer[mcp.Manager]

// killLiveMCP tue sur-le-champ les serveurs MCP stdio ouverts : le processus
// va quitter sans exécuter ses fonctions différées.
func killLiveMCP() {
	if m := liveMCP.Load(); m != nil {
		m.Kill()
	}
}

// startMCP se connecte aux serveurs MCP de la configuration globale. Leurs
// secrets (env_from, bearer_env) sont ajoutés au Redactor avant toute
// écriture ; les serveurs injoignables et les avertissements sont renvoyés
// pour être signalés.
func startMCP(o engineOptions, cfg config.Config, red *policy.Redactor) (*mcp.Manager, []event.McpServerDownPayload, []event.MCPServerInfo, []string) {
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	jurisdiction := map[string]llm.Jurisdiction{}
	// Serveurs exclus par le profil ou la politique : ni lancés ni contactés, signalés
	// indisponibles. Un serveur désactivé n'est pas lancé : pas d'avis.
	servers := make([]config.MCPServer, 0, len(cfg.MCP.Servers))
	skipped := map[string]string{}
	var skippedOrder []string
	for _, s := range cfg.MCP.Servers {
		jurisdiction[s.ID] = llm.Jurisdiction(s.Jurisdiction)
		if !s.Disabled {
			if msg := profileMCPSkip(o.lang, cfg.Profile, s); msg != "" {
				skipped[s.ID] = msg
				skippedOrder = append(skippedOrder, s.ID)
				continue
			}
			// Politique d'organisation : même chemin que le profil.
			var command0 string
			if len(s.Command) > 0 {
				command0 = s.Command[0]
			}
			if !cfg.Policy.AllowsMCP(s.Transport, s.URL, command0) {
				skipped[s.ID] = i18n.T(o.lang, "policy.mcp_skipped", cfg.Policy.ID, s.ID)
				skippedOrder = append(skippedOrder, s.ID)
				continue
			}
		}
		servers = append(servers, s)
	}
	allowNonEU, interactive := o.allowNonEU, o.interactive
	// Sans --allow-non-eu, mcp.Start ne lance ni ne contacte aucun serveur
	// hors UE (ou de juridiction inconnue) : ils sont signalés ignorés.
	// Consentement de juridiction, défense en profondeur vérifiée avant tout
	// envoi : un serveur UE ou local n'en a pas besoin ; hors UE, il faut
	// --allow-non-eu sans interface, la validation (ForceAsk) suffit dans la
	// TUI.
	consent := func(id string) bool {
		// Défense en profondeur : un serveur exclus (profil, politique) n'est jamais admis.
		if _, ok := skipped[id]; ok {
			return false
		}
		switch jurisdiction[id] {
		case llm.EU, llm.Local:
			return true
		}
		return allowNonEU || interactive
	}
	mgr, down := mcp.Start(ctx, servers, mcp.Options{
		Getenv:      o.getenv,
		CallTimeout: cfg.ShellTimeout.Duration,
		OutputLimit: cfg.OutputLimit,
		Consent:     consent,
		Root:        o.wd,
		AllowNonEU:  allowNonEU,
		NoProxy:     mcpNoProxy(cfg.Profile),
	})
	liveMCP.Store(mgr)
	// Comme les clés d'API : une valeur trop courte (ex. DEBUG=1) masquerait
	// du texte ordinaire.
	for _, v := range mgr.Secrets() {
		if len(v) >= 8 {
			red.AddLiteral(v)
		}
	}
	isDown := map[string]bool{}
	unavailable := make([]event.McpServerDownPayload, 0, len(down)+len(skippedOrder))
	var notices []string
	for _, id := range skippedOrder {
		isDown[id] = true
		unavailable = append(unavailable, event.McpServerDownPayload{Server: id, Error: skipped[id]})
		notices = append(notices, skipped[id])
	}
	for _, d := range down {
		isDown[d.Server] = true
		msg := d.Error
		if d.NonEUSkipped {
			msg = i18n.T(o.lang, "mcp.non_eu_skipped", d.Server)
		}
		unavailable = append(unavailable, event.McpServerDownPayload{Server: d.Server, Error: msg})
	}
	var active []event.MCPServerInfo
	for _, s := range cfg.MCP.Servers {
		if s.Disabled || isDown[s.ID] {
			continue
		}
		active = append(active, event.MCPServerInfo{ID: s.ID, Jurisdiction: llm.Jurisdiction(s.Jurisdiction), Country: s.Country})
	}
	for _, w := range mgr.Warnings() {
		notices = append(notices, i18n.T(o.lang, "mcp.warning", headless.OneLine(w)))
	}
	return mgr, unavailable, active, notices
}

// reMarkerKind reconnaît un marqueur de masquage et en capture le type et le
// numéro.
var reMarkerKind = regexp.MustCompile(`\[([A-Z_]+)_(\d+)\]`)

// reserveMarkers réserve, pour chaque type, le plus grand numéro de marqueur
// présent dans les charges utiles des événements.
func reserveMarkers(red *policy.Redactor, events []event.Event) {
	top := map[string]int{}
	for _, ev := range events {
		for _, m := range reMarkerKind.FindAllStringSubmatch(string(ev.Payload), -1) {
			if n, err := strconv.Atoi(m[2]); err == nil && n > top[m[1]] {
				top[m[1]] = n
			}
		}
	}
	for kind, n := range top {
		red.Reserve(kind, n)
	}
}

// providerKey cherche la clé du fournisseur p (environnement, puis
// trousseau). Un fournisseur local sans api_key_env n'a pas de clé : le
// trousseau n'est pas consulté.
func providerKey(p config.Provider, getenv func(string) string) (value, source string, err error) {
	if llm.Jurisdiction(p.Jurisdiction) == llm.Local && p.APIKeyEnv == "" {
		return "", "", nil
	}
	return secrets.Lookup(p.ID, p.APIKeyEnv, getenv)
}

// sameProject indique si le premier SessionStarted de events désigne wd
// (même comparaison que session.List).
func sameProject(events []event.Event, wd string) bool {
	for _, ev := range events {
		if ev.Type != event.SessionStarted {
			continue
		}
		var p event.SessionStartedPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return false
		}
		return session.SameRoot(p.ProjectRoot, wd)
	}
	return false
}

// skippedLines compte les lignes non vides de la session au-delà des valid
// premiers événements lisibles.
func skippedLines(dir, sid string, valid int) int {
	b, err := os.ReadFile(filepath.Join(dir, sid+".jsonl"))
	if err != nil {
		return 1
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	if n-valid < 1 {
		return 1
	}
	return n - valid
}

func auditVerify(lang i18n.Lang, stdout, stderr io.Writer, getenv func(string) string) int {
	hash, n, err := audit.Verify(filepath.Join(config.DataDir(getenv), "audit"))
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	fmt.Fprintln(stdout, i18n.T(lang, "audit.ok", n, hash))
	return 0
}

// newSessionID renvoie "s_" suivi de 12 caractères hexadécimaux aléatoires.
func newSessionID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session id: %w", err)
	}
	return "s_" + hex.EncodeToString(b), nil
}

// markFunc et markTimeout : marquage et délai de « tenon git mark »,
// remplacés dans les tests.
var (
	markFunc    = gitmark.MarkContext
	markTimeout = gitmark.Timeout
)

// gitMark ajoute les trailers de l'agent au message de commit args[0]. Appelé
// par le hook commit-msg, il ne doit jamais bloquer un commit : code 0 dans
// tous les cas, erreur écrite seulement si TENON_DEBUG=1. L'exécution entière
// est bornée par markTimeout : passé ce délai, la main est rendue sans
// attendre le marquage, qui ne modifie plus le message (son contexte a
// expiré).
func gitMark(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string, wd string) int {
	var err error
	if len(args) != 1 || args[0] == "" {
		err = errors.New("usage: tenon git mark <message-file>")
	} else {
		ctx, cancel := context.WithTimeout(ctx, markTimeout)
		defer cancel()
		mark, sdir := markFunc, sessionsDir(getenv)
		done := make(chan error, 1)
		go func() { done <- mark(ctx, args[0], wd, sdir) }()
		select {
		case err = <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err != nil && getenv("TENON_DEBUG") == "1" {
		fmt.Fprintln(stderr, "tenon git mark :", err)
	}
	return 0
}

// gitInstallHook installe le hook commit-msg du dépôt contenant wd.
func gitInstallHook(lang i18n.Lang, args []string, stdout, stderr io.Writer, wd string) int {
	fs := flag.NewFlagSet("tenon git install-hook", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, i18n.T(lang, "flag.force"))
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", fs.Arg(0)))
		return 1
	}
	path, err := gitmark.InstallHook(wd, *force)
	switch {
	case errors.Is(err, gitmark.ErrHookExists):
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "git.hook_exists", path)))
		return 1
	case err != nil:
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	fmt.Fprintln(stdout, i18n.T(lang, "git.hook_installed", path))
	return 0
}
