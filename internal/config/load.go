package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"github.com/BurntSushi/toml"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/profile"
)

// projectFile ne contient que les clés autorisées dans .tenon/config.toml.
// default_provider en est exclu : un dépôt ne choisit pas le fournisseur.
type projectFile struct {
	Lang        string      `toml:"lang"`
	Permissions Permissions `toml:"permissions"`
	Redaction   Redaction   `toml:"redaction"`
	Profile     string      `toml:"profile"`
	// ProviderAllowlist ne peut que restreindre : liste vide refusée à la validation.
	ProviderAllowlist *ProviderAllowlist `toml:"provider_allowlist"`
	// Sandbox : le projet ne peut que durcir (required, deny_read).
	Sandbox Sandbox `toml:"sandbox"`
	// Classification s'ajoute à celle du global ; Routing est refusé (pointeur :
	// une table vide l'est aussi).
	Classification Classification `toml:"classification"`
	Routing        *Routing       `toml:"routing"`
	// Budget : le projet ne peut que baisser le plafond. Usage est refusé
	// (pointeur : une table vide l'est aussi).
	Budget Budget `toml:"budget"`
	Usage  *Usage `toml:"usage"`
	// Memory : le projet ne peut que durcir (enabled = false, project = false,
	// retention_days plus court) ; le reste est ignoré sans bruit.
	Memory Memory `toml:"memory"`
	// CRA est refusé (pointeur : une table vide l'est aussi).
	CRA *CRA `toml:"cra"`
	// ACP est refusé (pointeur : une table vide l'est aussi).
	ACP *ACP `toml:"acp"`
	// UI est refusé (pointeur : une table vide l'est aussi).
	UI *UI `toml:"ui"`
}

// Load fusionne défauts, configuration globale puis configuration projet.
// Un fichier absent est ignoré.
func Load(globalPath, projectPath string, getenv func(string) string) (Config, error) {
	cfg, err := load(globalPath, projectPath, getenv)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadPolicy charge la configuration comme Load puis applique la politique
// pol (nil : aucune). Le fournisseur par défaut n'est contrôlé qu'après la
// politique : il peut être l'un de ses providers.define.
func LoadPolicy(globalPath, projectPath string, getenv func(string) string, pol *orgpolicy.Policy) (Config, error) {
	cfg, err := load(globalPath, projectPath, getenv)
	if err != nil {
		return Config{}, err
	}
	if cfg, err = ApplyPolicy(cfg, pol); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// load fusionne les fichiers et valide tout sauf le fournisseur par défaut.
func load(globalPath, projectPath string, getenv func(string) string) (Config, error) {
	cfg := Defaults(getenv)
	cfg.home = getenv("HOME")

	if data, ok, err := readOptional(globalPath); err != nil {
		return Config{}, err
	} else if ok {
		g := cfg
		g.Providers = nil
		md, err := toml.Decode(string(data), &g)
		if err != nil {
			return Config{}, fmt.Errorf("global config: %w", err)
		}
		// Une clé mal orthographiée (ex. [[permission.rules]]) ferait
		// disparaître silencieusement des règles : on la refuse.
		if und := md.Undecoded(); len(und) > 0 {
			return Config{}, fmt.Errorf("global config: key not allowed %q", und[0].String())
		}
		cfg.Lang, cfg.DefaultProvider = g.Lang, g.DefaultProvider
		cfg.MaxIterations, cfg.ShellTimeout, cfg.OutputLimit = g.MaxIterations, g.ShellTimeout, g.OutputLimit
		cfg.Audit = g.Audit
		cfg.CompactionThreshold = g.CompactionThreshold
		cfg.Permissions, cfg.Redaction = g.Permissions, g.Redaction
		cfg.MCP = g.MCP
		cfg.Hooks = g.Hooks
		cfg.Checkpoints = g.Checkpoints
		cfg.Classification, cfg.Routing, cfg.CRA = g.Classification, g.Routing, g.CRA
		if err := checkBudget(g.Budget, md); err != nil {
			return Config{}, err
		}
		if err := checkUsage(g.Usage, md); err != nil {
			return Config{}, err
		}
		cfg.Budget, cfg.Usage = g.Budget, g.Usage
		cfg.ACP = g.ACP
		cfg.UI = g.UI
		if err := checkMemory(g.Memory, md); err != nil {
			return Config{}, err
		}
		cfg.Memory = g.Memory
		if err := checkSandboxMode(g.Sandbox.Mode); err != nil {
			return Config{}, err
		}
		cfg.Sandbox.Mode = g.Sandbox.Mode
		if cfg.Sandbox.Write, err = expandSandboxPaths("write", g.Sandbox.Write, cfg.home); err != nil {
			return Config{}, err
		}
		if cfg.Sandbox.DenyRead, err = expandSandboxPaths("deny_read", g.Sandbox.DenyRead, cfg.home); err != nil {
			return Config{}, err
		}
		cfg.Allowlist = g.Allowlist
		gp, err := profile.Parse(string(g.Profile))
		if err != nil {
			return Config{}, fmt.Errorf("config: %w", err)
		}
		cfg.Profile = gp
		for i := range cfg.MCP.Servers {
			if s := &cfg.MCP.Servers[i]; s.Transport == "stdio" && s.Jurisdiction == "" {
				s.Jurisdiction = "local"
			}
		}
		for _, p := range g.Providers {
			if p.Jurisdiction == "" {
				p.Jurisdiction = "non-eu"
			}
			cfg.mergeProvider(p)
		}
	}

	if data, ok, err := readOptional(projectPath); err != nil {
		return Config{}, err
	} else if ok {
		var p projectFile
		md, err := toml.Decode(string(data), &p)
		if err != nil {
			return Config{}, fmt.Errorf("project config: %w", err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			return Config{}, fmt.Errorf("project config: key not allowed %q", und[0].String())
		}
		for _, r := range p.Permissions.Rules {
			if r.Network {
				return Config{}, errors.New("config: network not allowed in project configuration")
			}
			if r.Decision == "allow" {
				return Config{}, errors.New(`project config: "allow" rules are not allowed`)
			}
		}
		if p.Routing != nil {
			return Config{}, errors.New("config: routing not allowed in project configuration")
		}
		if p.Usage != nil {
			return Config{}, errors.New("config: usage not allowed in project configuration")
		}
		if p.UI != nil {
			return Config{}, errors.New("config: ui not allowed in project configuration")
		}
		if p.ACP != nil {
			return Config{}, errors.New("config: acp not allowed in project configuration")
		}
		if err := checkBudget(p.Budget, md); err != nil {
			return Config{}, err
		}
		if err := checkMemory(p.Memory, md); err != nil {
			return Config{}, err
		}
		if p.CRA != nil {
			return Config{}, errors.New("config: cra not allowed in project configuration")
		}
		for i := range p.Permissions.Rules {
			p.Permissions.Rules[i].Project = true
		}
		if p.Lang != "" {
			cfg.Lang = p.Lang
		}
		pp, err := profile.Parse(p.Profile)
		if err != nil {
			return Config{}, fmt.Errorf("project config: %w", err)
		}
		// Le projet ne peut que durcir le profil global.
		if pp != profile.None && profile.Stricter(cfg.Profile, pp) != pp {
			cfg.WeakerProjectProfile = pp
		}
		cfg.Profile = profile.Stricter(cfg.Profile, pp)
		cfg.ProjectAllowlist = p.ProviderAllowlist
		if err := checkSandboxMode(p.Sandbox.Mode); err != nil {
			return Config{}, err
		}
		if p.Sandbox.Mode == "off" {
			return Config{}, errors.New("config: sandbox.mode not allowed in project configuration")
		}
		if p.Sandbox.Write != nil {
			return Config{}, errors.New("config: sandbox.write not allowed in project configuration")
		}
		pd, err := expandSandboxPaths("deny_read", p.Sandbox.DenyRead, cfg.home)
		if err != nil {
			return Config{}, err
		}
		cfg.Sandbox.Mode = stricterSandbox(cfg.Sandbox.Mode, p.Sandbox.Mode)
		cfg.Sandbox.DenyRead = append(cfg.Sandbox.DenyRead, pd...)
		// Budget : le plus bas des plafonds non nuls ; le seuil d'avis du projet
		// s'applique seulement s'il baisse le plafond ou si aucun n'existe.
		if low := lowestBudget(cfg.Budget.SessionTokens, p.Budget.SessionTokens); low != cfg.Budget.SessionTokens {
			cfg.Budget.SessionTokens = low
			if p.Budget.WarnAt != 0 {
				cfg.Budget.WarnAt = p.Budget.WarnAt
			}
		}
		// Mémoire : le projet ne peut que couper ou raccourcir.
		if p.Memory.Enabled != nil && !*p.Memory.Enabled {
			cfg.Memory.Enabled = p.Memory.Enabled
		}
		if p.Memory.Project != nil && !*p.Memory.Project {
			cfg.Memory.Project = p.Memory.Project
		}
		if p.Memory.RetentionDays > 0 && p.Memory.RetentionDays < cfg.Memory.Days() {
			cfg.Memory.RetentionDays = p.Memory.RetentionDays
		}
		cfg.Classification.Paths = append(slices.Clone(cfg.Classification.Paths), p.Classification.Paths...)
		cfg.Classification.Markers = append(slices.Clone(cfg.Classification.Markers), p.Classification.Markers...)
		cfg.Permissions.Rules = append(cfg.Permissions.Rules, p.Permissions.Rules...)
		cfg.Redaction.Rules = append(cfg.Redaction.Rules, p.Redaction.Rules...)
	}

	cfg.finishBudget()
	if !cfg.Memory.On() {
		cfg.MemoryDisabledBy = "config"
	}
	// Plancher du profil, puis mode explicite (« auto » par défaut).
	cfg.Sandbox.Mode = stricterSandbox(cfg.Sandbox.Mode, profile.SandboxMode(cfg.Profile))
	if err := cfg.validateFields(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// mergeProvider remplace le fournisseur de même identifiant. Une entrée qui
// remplace un préréglage détecté (ollama, vllm) hérite de sa détection, sauf
// detect = "off" : model ou context_window laissés vides restent détectés.
func (c *Config) mergeProvider(p Provider) {
	for i := range c.Providers {
		if c.Providers[i].ID == p.ID {
			if p.Detect == "" {
				p.Detect = c.Providers[i].Detect
			}
			c.Providers[i] = p
			return
		}
	}
	c.Providers = append(c.Providers, p)
}

func readOptional(path string) ([]byte, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
