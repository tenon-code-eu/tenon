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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"codeberg.org/tenon/tenon/internal/audit"
	"codeberg.org/tenon/tenon/internal/config"
	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/gitmark"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/instructions"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/mcp"
	"codeberg.org/tenon/tenon/internal/policy"
	"codeberg.org/tenon/tenon/internal/provider"
	"codeberg.org/tenon/tenon/internal/secrets"
	"codeberg.org/tenon/tenon/internal/session"
	"codeberg.org/tenon/tenon/internal/tools"
	"codeberg.org/tenon/tenon/internal/ui/headless"
	"codeberg.org/tenon/tenon/internal/ui/tui"
	"golang.org/x/term"
)

// startTUI lance l'interface interactive ; remplacée dans les tests.
var startTUI = tui.Run

// run exécute la CLI et retourne le code de sortie. stdin sert à
// « tenon key set » et à l'interface interactive ; isTerminal indique que
// l'entrée et la sortie standard sont des terminaux.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, wd string, isTerminal func() bool) int {
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
	// « tenon sessions --lang en » : --lang est aussi accepté après la sous-commande.
	if len(rest) > 0 && rest[0] == "sessions" {
		sub := flag.NewFlagSet("tenon sessions", flag.ContinueOnError)
		sub.SetOutput(stderr)
		sub.StringVar(langFlag, "lang", *langFlag, i18n.T(hl, "flag.lang"))
		if err := sub.Parse(rest[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 1
		}
		rest = append([]string{"sessions"}, sub.Args()...)
	}

	lang, ok := uiLang(*langFlag, getenv, wd)
	if !ok {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.bad_lang", *langFlag)))
		return 1
	}

	// --resume ne vaut que pour une conversation (interface interactive ou -p).
	if *resume != "" && len(rest) > 0 && rest[0] != "sessions" {
		fmt.Fprintln(stderr, i18n.T(lang, "err.no_prompt"))
		return 1
	}

	switch {
	case len(rest) == 2 && rest[0] == "audit" && rest[1] == "verify":
		return auditVerify(lang, stdout, stderr, getenv)
	case len(rest) == 2 && rest[0] == "config" && rest[1] == "check":
		if _, err := loadSetup(getenv, wd); err != nil {
			fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
			return 1
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
	case len(rest) >= 2 && rest[0] == "git" && rest[1] == "mark":
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

	st, err := startEngine(engineOptions{
		ctx: ctx, getenv: getenv, wd: wd, providerID: *providerID, allowNonEU: *allowNonEU,
		lang: lang, resume: *resume, stderr: stderr,
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
	return headless.Run(ctx, st.eng, st.sessionID, *prompt, headless.Options{
		Output:     out,
		Lang:       lang,
		Redact:     func(s string) string { out, _ := red.RedactJSON(s); return out },
		RedactText: redactText(red),
	}, stdout, stderr)
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
		Redact:        redactText(st.redactor),
		// Serveurs indisponibles signalés dès l'ouverture, en plus de
		// l'événement McpServerDown émis au premier tour.
		Notices: append(slices.Clone(st.notices), st.downNotices...),
	}, stdin, stdout)
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
// configuration (dont la valeur par défaut suit $LANG). Une configuration
// illisible n'empêche pas de choisir une langue : son erreur sera signalée
// plus loin. ok est faux si --lang n'est ni fr ni en.
func uiLang(flagValue string, getenv func(string) string, wd string) (i18n.Lang, bool) {
	cfgLang := config.Defaults(getenv).Lang
	if cfg, err := config.Load(config.GlobalPath(getenv), filepath.Join(wd, ".tenon", "config.toml"), getenv); err == nil {
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
	s, err := loadSetup(getenv, wd)
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

// loadSetup charge la configuration globale et projet, puis la convertit en
// règles de permissions et en Redactor.
func loadSetup(getenv func(string) string, wd string) (setup, error) {
	cfg, err := config.Load(config.GlobalPath(getenv), filepath.Join(wd, ".tenon", "config.toml"), getenv)
	if err != nil {
		return setup{}, err
	}
	rules := make([]policy.Rule, 0, len(cfg.Permissions.Rules))
	for _, r := range cfg.Permissions.Rules {
		d, err := policy.ParseDecision(r.Decision)
		if err != nil {
			return setup{}, fmt.Errorf("règle de permission (%s %q) : %w", r.Tool, r.Match, err)
		}
		rules = append(rules, policy.Rule{Tool: r.Tool, Match: r.Match, Decision: d})
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
}

// started est un Engine prêt pour la session sessionID.
type started struct {
	eng       *engine.Engine
	redactor  *policy.Redactor // celui de l'audit, de la session et d'Outbound
	sessionID string
	// close ferme les serveurs MCP, puis le journal d'audit et le fichier de
	// session ; à appeler après la fin du tour.
	close func()
	// notices sont les avertissements du démarrage (MCP), déjà traduits.
	notices []string
	// downNotices signalent les serveurs MCP indisponibles au démarrage, déjà
	// traduits ; le mode non interactif les affiche via l'événement McpServerDown.
	downNotices []string
	// provider et contextWindow décrivent le fournisseur choisi (barre d'état).
	provider      llm.ProviderInfo
	contextWindow int
}

// startEngine assemble configuration, fournisseur, politique, outils, audit et
// session ; une session reprise est restaurée dans l'Engine.
func startEngine(o engineOptions) (started, error) {
	getenv, wd, lang := o.getenv, o.wd, o.lang
	s, err := loadSetup(getenv, wd)
	if err != nil {
		return started{}, err
	}
	cfg := s.cfg

	providerID := o.providerID
	if providerID == "" {
		providerID = cfg.DefaultProvider
	}
	pc, ok := cfg.Provider(providerID)
	if !ok {
		return started{}, errors.New(i18n.T(lang, "err.unknown_provider", providerID))
	}
	info := llm.ProviderInfo{
		ID: pc.ID, Name: pc.Name, BaseURL: pc.BaseURL, Model: pc.Model,
		Country: pc.Country, Jurisdiction: llm.Jurisdiction(pc.Jurisdiction),
	}
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
	ts := append(tools.FS(wd, cfg.OutputLimit, tools.WithReadFilter(readable)),
		tools.Shell(wd, cfg.ShellTimeout.Duration, cfg.OutputLimit))

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

	eng := engine.New(engine.Deps{
		Provider:            provider.NewOpenAICompat(info, apiKey),
		Tools:               tools.NewRegistry(ts...),
		Permissions:         perm,
		Outbound:            out,
		Sinks:               []event.Sink{w, sw}, // audit d'abord, puis la session
		MaxIterations:       cfg.MaxIterations,
		SystemPrompt:        instructions.Render(i18n.SystemPrompt(lang), files),
		ProjectRoot:         wd,
		Instructions:        names,
		ContextWindow:       pc.Window(),
		CompactionThreshold: cfg.CompactionThreshold,
		UnavailableMCP:      unavailable,
		MCP:                 active,
	})
	if o.resume != "" {
		if err := eng.Restore(sid, events); err != nil {
			closeAll()
			return started{}, fmt.Errorf("session %s : %w", sid, err)
		}
	}
	return started{
		eng: eng, redactor: red, sessionID: sid, close: closeAll, notices: notices,
		downNotices: downNotices(o.lang, unavailable, red),
		provider:    info, contextWindow: pc.Window(),
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
	for _, s := range cfg.MCP.Servers {
		jurisdiction[s.ID] = llm.Jurisdiction(s.Jurisdiction)
	}
	allowNonEU, interactive := o.allowNonEU, o.interactive
	// Sans --allow-non-eu, mcp.Start ne lance ni ne contacte aucun serveur
	// hors UE (ou de juridiction inconnue) : ils sont signalés ignorés.
	// Consentement de juridiction, défense en profondeur vérifiée avant tout
	// envoi : un serveur UE ou local n'en a pas besoin ; hors UE, il faut
	// --allow-non-eu sans interface, la validation (ForceAsk) suffit dans la
	// TUI.
	consent := func(id string) bool {
		switch jurisdiction[id] {
		case llm.EU, llm.Local:
			return true
		}
		return allowNonEU || interactive
	}
	mgr, down := mcp.Start(ctx, cfg.MCP.Servers, mcp.Options{
		Getenv:      o.getenv,
		CallTimeout: cfg.ShellTimeout.Duration,
		OutputLimit: cfg.OutputLimit,
		Consent:     consent,
		Root:        o.wd,
		AllowNonEU:  allowNonEU,
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
	unavailable := make([]event.McpServerDownPayload, 0, len(down))
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
	var notices []string
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
		return "", fmt.Errorf("identifiant de session : %w", err)
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
		err = errors.New("usage : tenon git mark <fichier-message>")
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

// gitInstallHook installe le hook prepare-commit-msg du dépôt contenant wd.
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
