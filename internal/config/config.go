// Package config charge la configuration globale et projet (TOML).
//
// La configuration projet est une frontière de sécurité : un dépôt
// malveillant ne doit pouvoir ni assouplir les permissions ni rediriger
// le trafic vers un autre fournisseur.
package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
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
	// MCP : serveurs MCP. Clé globale uniquement (un serveur stdio est une
	// exécution de code arbitraire : un dépôt ne peut pas en déclarer).
	MCP MCP `toml:"mcp"`
}

type MCP struct {
	Servers []MCPServer `toml:"servers"`
}

// MCPServer déclare un serveur MCP. Aucun secret : variables référencées par nom.
type MCPServer struct {
	ID           string   `toml:"id"`           // [a-z0-9_-]{1,32}
	Transport    string   `toml:"transport"`    // "stdio" | "http"
	Command      []string `toml:"command"`      // stdio : argv, non vide
	EnvFrom      []string `toml:"env_from"`     // stdio : variables transmises au serveur
	URL          string   `toml:"url"`          // http : https://… (http:// seulement vers localhost/127.0.0.1/::1)
	BearerEnv    string   `toml:"bearer_env"`   // http : variable contenant le jeton
	Jurisdiction string   `toml:"jurisdiction"` // "eu" | "non-eu" | "local"
	Country      string   `toml:"country"`
	Risk         string   `toml:"risk"`  // "" | "read" | "write" | "exec" | "network"
	Tools        []string `toml:"tools"` // liste blanche optionnelle des outils exposés
	Disabled     bool     `toml:"disabled"`
}

var mcpIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

func validateMCPURL(id, raw string) error {
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("config : url invalide pour le serveur MCP %q", id)
	}
	if u.User != nil {
		return fmt.Errorf("config : url du serveur MCP %q : identifiants interdits dans l'URL (utiliser bearer_env)", id)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return fmt.Errorf("config : url du serveur MCP %q : requête et fragment interdits dans l'URL (jeton via bearer_env)", id)
	}
	switch u.Scheme {
	case "https":
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
		default:
			return fmt.Errorf("config : url du serveur MCP %q : https requis (http seulement vers localhost, 127.0.0.1 ou ::1)", id)
		}
	default:
		return fmt.Errorf("config : url du serveur MCP %q : schéma %q non pris en charge (https)", id, u.Scheme)
	}
	return nil
}

func (m MCP) validate() error {
	seen := map[string]bool{}
	for _, s := range m.Servers {
		if !mcpIDPattern.MatchString(s.ID) {
			return fmt.Errorf("config : identifiant de serveur MCP invalide %q ([a-z0-9_-]{1,32})", s.ID)
		}
		if seen[s.ID] {
			return fmt.Errorf("config : identifiant de serveur MCP dupliqué %q", s.ID)
		}
		seen[s.ID] = true
		switch s.Transport {
		case "stdio":
			if len(s.Command) == 0 || s.Command[0] == "" {
				return fmt.Errorf("config : command requis pour le serveur MCP stdio %q", s.ID)
			}
			// Un chemin relatif serait résolu depuis le projet, non fiable.
			if c := s.Command[0]; !filepath.IsAbs(c) && strings.ContainsRune(c, '/') {
				return fmt.Errorf("config : command du serveur MCP stdio %q : chemin absolu ou nom nu (cherché dans PATH) requis, pas %q", s.ID, c)
			}
		case "http":
			if err := validateMCPURL(s.ID, s.URL); err != nil {
				return err
			}
			if s.Jurisdiction == "" {
				return fmt.Errorf("config : jurisdiction requise pour le serveur MCP http %q (eu|non-eu|local)", s.ID)
			}
		default:
			return fmt.Errorf("config : transport invalide %q pour le serveur MCP %q (stdio|http)", s.Transport, s.ID)
		}
		switch s.Jurisdiction {
		case "eu", "non-eu", "local":
		default:
			return fmt.Errorf("config : jurisdiction invalide %q pour le serveur MCP %q (eu|non-eu|local)", s.Jurisdiction, s.ID)
		}
		switch s.Risk {
		case "", "read", "write", "exec", "network":
		default:
			return fmt.Errorf("config : risk invalide %q pour le serveur MCP %q (read|write|exec|network)", s.Risk, s.ID)
		}
	}
	return nil
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
	// Detect : "ollama" ou "vllm" pour interroger le serveur au démarrage
	// (modèle servi, fenêtre appliquée) quand model ou context_window est
	// vide ; "off" désactive la détection héritée d'un préréglage.
	Detect string `toml:"detect"`
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

// Rule.Decision : "allow" | "ask" | "deny". Project est posé au chargement
// pour les règles de la config projet (jamais lu depuis le TOML).
type Rule struct {
	Tool     string `toml:"tool"`
	Match    string `toml:"match"`
	Decision string `toml:"decision"`
	Project  bool   `toml:"-"`
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
	// Interface en anglais par défaut ; lang = "fr" (ou --lang fr) pour le
	// français. $LANG n'est pas lu : la langue ne change pas d'une machine à
	// l'autre sans configuration.
	return Config{
		Lang:                "en",
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
		switch p.Detect {
		case "", "off", "ollama", "vllm":
		default:
			return fmt.Errorf("config : detect invalide %q pour le fournisseur %q (ollama|vllm|off)", p.Detect, p.ID)
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
	if err := c.MCP.validate(); err != nil {
		return err
	}
	if _, ok := c.Provider(c.DefaultProvider); !ok {
		return fmt.Errorf("config : default_provider inconnu %q", c.DefaultProvider)
	}
	return nil
}
