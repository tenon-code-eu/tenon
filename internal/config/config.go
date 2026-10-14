// Package config charge la configuration globale et projet (TOML).
//
// La configuration projet est une frontière de sécurité : un dépôt
// malveillant ne doit pouvoir ni assouplir les permissions ni rediriger
// le trafic vers un autre fournisseur.
package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

type Config struct {
	Lang            string   `toml:"lang"`
	DefaultProvider string   `toml:"default_provider"`
	MaxIterations   int      `toml:"max_iterations"`
	ShellTimeout    Duration `toml:"shell_timeout"`
	OutputLimit     int      `toml:"output_limit"`
	// CompactionThreshold : part de la fenêtre de contexte au-delà de
	// laquelle l'historique est compacté. Clé globale uniquement.
	CompactionThreshold float64     `toml:"compaction_threshold"`
	Audit               Audit       `toml:"audit"`
	Providers           []Provider  `toml:"providers"`
	Permissions         Permissions `toml:"permissions"`
	Redaction           Redaction   `toml:"redaction"`
}

type Audit struct {
	Level string `toml:"level"`
}

type Permissions struct {
	Rules []Rule `toml:"rules"`
}

type Redaction struct {
	Rules []RedactionRule `toml:"rules"`
}

// Duration encapsule time.Duration pour le décodage TOML ("2m").
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

type Provider struct {
	ID            string `toml:"id"`
	Name          string `toml:"name"`
	BaseURL       string `toml:"base_url"`
	APIKeyEnv     string `toml:"api_key_env"`
	Model         string `toml:"model"`
	Country       string `toml:"country"`
	Jurisdiction  string `toml:"jurisdiction"`
	ContextWindow int    `toml:"context_window"`
}

// defaultWindow : fenêtre de contexte (jetons) si le fournisseur n'en déclare pas.
const defaultWindow = 32000

// Window renvoie la fenêtre de contexte du fournisseur, ou 32000 si non renseignée.
func (p Provider) Window() int {
	if p.ContextWindow == 0 {
		return defaultWindow
	}
	return p.ContextWindow
}

// Rule.Decision : "allow" | "ask" | "deny".
type Rule struct {
	Tool     string `toml:"tool"`
	Match    string `toml:"match"`
	Decision string `toml:"decision"`
}

type RedactionRule struct {
	Name    string `toml:"name"`
	Pattern string `toml:"pattern"`
}

// Provider renvoie le fournisseur d'identifiant id.
func (c Config) Provider(id string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Defaults renvoie la configuration par défaut, préréglages inclus.
func Defaults(getenv func(string) string) Config {
	lang := "fr"
	if l := getenv("LANG"); len(l) >= 2 && l[:2] == "en" {
		lang = "en"
	}
	return Config{
		Lang:                lang,
		DefaultProvider:     "mistral",
		MaxIterations:       50,
		ShellTimeout:        Duration{2 * time.Minute},
		OutputLimit:         30000,
		CompactionThreshold: 0.8,
		Audit:               Audit{Level: "full"},
		Providers:           presets(),
	}
}

// GlobalPath : $XDG_CONFIG_HOME/tenon/config.toml, sinon $HOME/.config/tenon/config.toml.
func GlobalPath(getenv func(string) string) string {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "tenon", "config.toml")
	}
	return filepath.Join(getenv("HOME"), ".config", "tenon", "config.toml")
}

// DataDir : $XDG_DATA_HOME/tenon, sinon $HOME/.local/share/tenon.
func DataDir(getenv func(string) string) string {
	if x := getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "tenon")
	}
	return filepath.Join(getenv("HOME"), ".local", "share", "tenon")
}

func (c Config) validate() error {
	switch c.Lang {
	case "fr", "en":
	default:
		return fmt.Errorf("config : lang invalide %q (fr|en)", c.Lang)
	}
	switch c.Audit.Level {
	case "full", "metadata":
	default:
		return fmt.Errorf("config : audit.level invalide %q (full|metadata)", c.Audit.Level)
	}
	if c.CompactionThreshold <= 0 || c.CompactionThreshold >= 1 {
		return fmt.Errorf("config : compaction_threshold invalide %v (]0,1[)", c.CompactionThreshold)
	}
	for _, p := range c.Providers {
		if p.ContextWindow < 0 {
			return fmt.Errorf("config : context_window négatif pour le fournisseur %q", p.ID)
		}
		switch p.Jurisdiction {
		case "eu", "non-eu", "local":
		default:
			return fmt.Errorf("config : jurisdiction invalide %q pour le fournisseur %q (eu|non-eu|local)", p.Jurisdiction, p.ID)
		}
	}
	for _, r := range c.Permissions.Rules {
		switch r.Decision {
		case "allow", "ask", "deny":
		default:
			return fmt.Errorf("config : decision invalide %q (allow|ask|deny)", r.Decision)
		}
	}
	for _, r := range c.Redaction.Rules {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("config : pattern de masquage %q invalide : %w", r.Name, err)
		}
	}
	if _, ok := c.Provider(c.DefaultProvider); !ok {
		return fmt.Errorf("config : default_provider inconnu %q", c.DefaultProvider)
	}
	return nil
}
