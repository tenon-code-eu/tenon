package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"codeberg.org/tenon/tenon/internal/audit"
	"codeberg.org/tenon/tenon/internal/config"
	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/policy"
	"codeberg.org/tenon/tenon/internal/provider"
	"codeberg.org/tenon/tenon/internal/tools"
	"codeberg.org/tenon/tenon/internal/ui/headless"
)

// run exécute la CLI et retourne le code de sortie.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string, wd string) int {
	fs := flag.NewFlagSet("tenon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "afficher la version et quitter")
	prompt := fs.String("p", "", "exécuter ce prompt sans interface interactive")
	output := fs.String("output", string(headless.Text), "format de sortie : text ou json")
	allowNonEU := fs.Bool("allow-non-eu", false, "autoriser l'envoi à un fournisseur hors UE")
	providerID := fs.String("provider", "", "identifiant du fournisseur (défaut : default_provider)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage : tenon -p "<prompt>" [--output text|json] [--allow-non-eu] [--provider <id>]`)
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

	switch rest := fs.Args(); {
	case len(rest) == 2 && rest[0] == "audit" && rest[1] == "verify":
		return auditVerify(stdout, stderr, getenv)
	case len(rest) == 2 && rest[0] == "config" && rest[1] == "check":
		if _, err := loadSetup(getenv, wd); err != nil {
			fmt.Fprintf(stderr, "tenon : %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "OK")
		return 0
	case len(rest) > 0:
		fmt.Fprintf(stderr, "tenon: argument inattendu: %s\n", rest[0])
		return 1
	}

	if *prompt == "" {
		fmt.Fprintln(stderr, `l'interface interactive arrive bientôt : utilisez -p "<prompt>"`)
		return 1
	}
	out := headless.Output(*output)
	if out != headless.Text && out != headless.JSON {
		fmt.Fprintf(stderr, "tenon : format de sortie inconnu %q (text|json)\n", *output)
		return 1
	}

	eng, red, closeFn, err := startEngine(getenv, wd, *providerID, *allowNonEU)
	if err != nil {
		fmt.Fprintf(stderr, "tenon : %v\n", err)
		return 1
	}
	defer closeFn()
	sid, err := newSessionID()
	if err != nil {
		fmt.Fprintf(stderr, "tenon : %v\n", err)
		return 1
	}
	return headless.Run(ctx, eng, sid, *prompt, headless.Options{
		Output:     out,
		Redact:     func(s string) string { out, _ := red.RedactJSON(s); return out },
		RedactText: func(s string) string { out, _ := red.Redact(s); return out },
	}, stdout, stderr)
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

// startEngine assemble configuration, fournisseur, politique, outils et audit.
// Le Redactor renvoyé est celui de l'audit et d'Outbound ; closeFn ferme le
// journal d'audit.
func startEngine(getenv func(string) string, wd, providerID string, allowNonEU bool) (*engine.Engine, *policy.Redactor, func(), error) {
	s, err := loadSetup(getenv, wd)
	if err != nil {
		return nil, nil, nil, err
	}
	cfg := s.cfg

	if providerID == "" {
		providerID = cfg.DefaultProvider
	}
	pc, ok := cfg.Provider(providerID)
	if !ok {
		return nil, nil, nil, fmt.Errorf("fournisseur inconnu %q", providerID)
	}
	info := llm.ProviderInfo{
		ID: pc.ID, Name: pc.Name, BaseURL: pc.BaseURL, Model: pc.Model,
		Country: pc.Country, Jurisdiction: llm.Jurisdiction(pc.Jurisdiction),
	}
	var apiKey string
	if pc.APIKeyEnv != "" {
		apiKey = getenv(pc.APIKeyEnv)
	}
	if apiKey == "" && info.Jurisdiction != llm.Local {
		if pc.APIKeyEnv == "" {
			return nil, nil, nil, fmt.Errorf("fournisseur %q : api_key_env non défini", pc.ID)
		}
		return nil, nil, nil, fmt.Errorf("variable %s absente", pc.APIKeyEnv)
	}

	perm, err := policy.NewPermissions(wd, s.rules)
	if err != nil {
		return nil, nil, nil, err
	}
	out := policy.NewOutbound(s.redactor)
	if allowNonEU {
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
	w, err := audit.Open(filepath.Join(config.DataDir(getenv), "audit"), audit.Level(cfg.Audit.Level),
		func(s string) string { out, _ := red.RedactJSON(s); return out })
	if err != nil {
		return nil, nil, nil, err
	}

	eng := engine.New(engine.Deps{
		Provider:      provider.NewOpenAICompat(info, apiKey),
		Tools:         tools.NewRegistry(ts...),
		Permissions:   perm,
		Outbound:      out,
		Sinks:         []event.Sink{w},
		MaxIterations: cfg.MaxIterations,
		ProjectRoot:   wd,
	})
	return eng, red, func() { _ = w.Close() }, nil
}

func auditVerify(stdout, stderr io.Writer, getenv func(string) string) int {
	hash, n, err := audit.Verify(filepath.Join(config.DataDir(getenv), "audit"))
	if err != nil {
		fmt.Fprintf(stderr, "tenon : %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "OK : %d lignes, dernier hash %s\n", n, hash)
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
