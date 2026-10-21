package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

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
			if r.Decision == "allow" {
				return Config{}, errors.New(`project config: "allow" rules are not allowed`)
			}
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
		cfg.Permissions.Rules = append(cfg.Permissions.Rules, p.Permissions.Rules...)
		cfg.Redaction.Rules = append(cfg.Redaction.Rules, p.Redaction.Rules...)
	}

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
