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
	"strings"

	"codeberg.org/tenon/tenon/internal/audit"
	"codeberg.org/tenon/tenon/internal/config"
	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/instructions"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/policy"
	"codeberg.org/tenon/tenon/internal/provider"
	"codeberg.org/tenon/tenon/internal/secrets"
	"codeberg.org/tenon/tenon/internal/session"
	"codeberg.org/tenon/tenon/internal/tools"
	"codeberg.org/tenon/tenon/internal/ui/headless"
	"golang.org/x/term"
)

// run exécute la CLI et retourne le code de sortie. stdin ne sert qu'à
// « tenon key set ».
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, wd string) int {
	fs := flag.NewFlagSet("tenon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "afficher la version et quitter")
	prompt := fs.String("p", "", "exécuter ce prompt sans interface interactive")
	output := fs.String("output", string(headless.Text), "format de sortie : text ou json")
	allowNonEU := fs.Bool("allow-non-eu", false, "autoriser l'envoi à un fournisseur hors UE")
	providerID := fs.String("provider", "", "identifiant du fournisseur (défaut : default_provider)")
	langFlag := fs.String("lang", "", "langue de l'interface : fr ou en (défaut : lang de la configuration)")
	resume := fs.String("resume", "", "reprendre une session : son identifiant ou last (avec -p)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage : tenon -p "<prompt>" [--resume <id|last>] [--lang fr|en] [--output text|json] [--allow-non-eu] [--provider <id>]`)
		fmt.Fprintln(stderr, "        tenon sessions [--lang fr|en]")
		fmt.Fprintln(stderr, "        tenon key set|delete <fournisseur>")
		fmt.Fprintln(stderr, "        tenon audit verify")
		fmt.Fprintln(stderr, "        tenon config check")
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
		sub.StringVar(langFlag, "lang", *langFlag, "langue de l'interface : fr ou en")
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
	case len(rest) == 3 && rest[0] == "key" && (rest[1] == "set" || rest[1] == "delete"):
		return keyCommand(lang, rest[1], rest[2], stdin, stdout, stderr, getenv, wd)
	case len(rest) > 0:
		fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", rest[0]))
		return 1
	}

	if *prompt == "" {
		fmt.Fprintln(stderr, i18n.T(lang, "err.no_prompt"))
		return 1
	}
	out := headless.Output(*output)
	if out != headless.Text && out != headless.JSON {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "err.bad_output", *output)))
		return 1
	}

	st, err := startEngine(engineOptions{
		getenv: getenv, wd: wd, providerID: *providerID, allowNonEU: *allowNonEU,
		lang: lang, resume: *resume, stderr: stderr,
	})
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	defer st.close()
	red := st.redactor
	return headless.Run(ctx, st.eng, st.sessionID, *prompt, headless.Options{
		Output:     out,
		Lang:       lang,
		Redact:     func(s string) string { out, _ := red.RedactJSON(s); return out },
		RedactText: func(s string) string { out, _ := red.Redact(s); return out },
	}, stdout, stderr)
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
			return "", fmt.Errorf("lecture de la clé : %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	if stdin == nil {
		return "", errors.New("lecture de la clé : entrée standard absente")
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("lecture de la clé : %w", err)
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
	getenv     func(string) string
	wd         string
	providerID string
	allowNonEU bool
	lang       i18n.Lang
	// resume : "" (nouvelle session), "last" ou un identifiant de session.
	resume string
	// stderr reçoit les avertissements (trousseau, session endommagée).
	stderr io.Writer
}

// started est un Engine prêt pour la session sessionID.
type started struct {
	eng       *engine.Engine
	redactor  *policy.Redactor // celui de l'audit, de la session et d'Outbound
	sessionID string
	close     func() // ferme le journal d'audit et le fichier de session
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
		return started{}, fmt.Errorf("instructions du projet : %w", err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}

	// La session à reprendre est lue avant l'ouverture du writer, qui tronque
	// un fichier endommagé à sa première ligne illisible.
	sdir := sessionsDir(getenv)
	var sid string
	var events []event.Event
	switch o.resume {
	case "":
		if sid, err = newSessionID(); err != nil {
			return started{}, err
		}
	case "last":
		if sid, err = session.Latest(sdir, wd); err != nil {
			return started{}, err
		}
	default:
		sid = o.resume
	}
	if o.resume != "" {
		var corrupt bool
		events, corrupt, err = session.Load(sdir, sid)
		if err != nil {
			return started{}, fmt.Errorf("session %s : %w", sid, err)
		}
		// Comme List et Latest, la reprise est limitée aux sessions du projet
		// courant : une session d'un autre projet est traitée comme absente,
		// avant toute ouverture de son fichier ou requête au fournisseur.
		if !sameProject(events, wd) {
			return started{}, fmt.Errorf("session %s : %w", sid, session.ErrNoSession)
		}
		if corrupt {
			fmt.Fprintln(o.stderr, i18n.T(lang, "resume.warning_corrupt", skippedLines(sdir, sid, len(events)), sid))
		}
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
	sw, err := session.Open(sdir, sid, redactJSON)
	if err != nil {
		_ = w.Close()
		return started{}, err
	}
	closeAll := func() {
		_ = sw.Close()
		_ = w.Close()
	}

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
	})
	if o.resume != "" {
		if err := eng.Restore(sid, events); err != nil {
			closeAll()
			return started{}, fmt.Errorf("session %s : %w", sid, err)
		}
	}
	return started{eng: eng, redactor: red, sessionID: sid, close: closeAll}, nil
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
// (chemins nettoyés, liens symboliques résolus quand c'est possible).
func sameProject(events []event.Event, wd string) bool {
	for _, ev := range events {
		if ev.Type != event.SessionStarted {
			continue
		}
		var p event.SessionStartedPayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.ProjectRoot == "" {
			return false
		}
		return canonicalPath(p.ProjectRoot) == canonicalPath(wd)
	}
	return false
}

func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
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
