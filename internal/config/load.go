package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// projectFile ne contient que les clés autorisées dans .tenon/config.toml.
// default_provider en est exclu : un dépôt ne choisit pas le fournisseur.
type projectFile struct {
	Lang        string      `toml:"lang"`
	Permissions Permissions `toml:"permissions"`
	Redaction   Redaction   `toml:"redaction"`
}

// Load fusionne défauts, configuration globale puis configuration projet.
// Un fichier absent est ignoré.
func Load(globalPath, projectPath string, getenv func(string) string) (Config, error) {
	cfg := Defaults(getenv)

	if data, ok, err := readOptional(globalPath); err != nil {
		return Config{}, err
	} else if ok {
		g := cfg
		g.Providers = nil
		md, err := toml.Decode(string(data), &g)
		if err != nil {
			return Config{}, fmt.Errorf("config globale : %w", err)
		}
		// Une clé mal orthographiée (ex. [[permission.rules]]) ferait
		// disparaître silencieusement des règles : on la refuse.
		if und := md.Undecoded(); len(und) > 0 {
			return Config{}, fmt.Errorf("config globale : clé non autorisée %q", und[0].String())
		}
		cfg.Lang, cfg.DefaultProvider = g.Lang, g.DefaultProvider
		cfg.MaxIterations, cfg.ShellTimeout, cfg.OutputLimit = g.MaxIterations, g.ShellTimeout, g.OutputLimit
		cfg.Audit = g.Audit
		cfg.CompactionThreshold = g.CompactionThreshold
		cfg.Permissions, cfg.Redaction = g.Permissions, g.Redaction
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
			return Config{}, fmt.Errorf("config projet : %w", err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			return Config{}, fmt.Errorf("config projet : clé non autorisée %q", und[0].String())
		}
		for _, r := range p.Permissions.Rules {
			if r.Decision == "allow" {
				return Config{}, errors.New(`config projet : les règles "allow" sont interdites`)
			}
		}
		if p.Lang != "" {
			cfg.Lang = p.Lang
		}
		cfg.Permissions.Rules = append(cfg.Permissions.Rules, p.Permissions.Rules...)
		cfg.Redaction.Rules = append(cfg.Redaction.Rules, p.Redaction.Rules...)
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) mergeProvider(p Provider) {
	for i := range c.Providers {
		if c.Providers[i].ID == p.ID {
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
