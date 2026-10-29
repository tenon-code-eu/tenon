// Package config charge la configuration globale et projet (TOML).
//
// La configuration projet est une frontière de sécurité : un dépôt
// malveillant ne doit pouvoir ni assouplir les permissions ni rediriger
// le trafic vers un autre fournisseur.
package config

import (
	"crypto/x509"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tenon-code-eu/tenon/internal/classify"
	"github.com/tenon-code-eu/tenon/internal/hostmatch"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/tsp"
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
	// Hooks : réglages des hooks. Clé globale uniquement (un hook est une
	// exécution de code arbitraire : un dépôt ne peut pas les activer).
	Hooks Hooks `toml:"hooks"`
	// Checkpoints : copies des fichiers avant écriture (/rewind). Clé globale
	// uniquement.
	Checkpoints Checkpoints `toml:"checkpoints"`
	// Sandbox : confinement des commandes shell. Mode effectif et listes
	// fusionnées (chemins développés) après Load ; write est global uniquement.
	Sandbox Sandbox `toml:"sandbox"`
	// Classification : chemins et marqueurs sensibles, union global + projet
	// + politique après Load.
	Classification Classification `toml:"classification"`
	// Routing : acheminement des sessions sensibles. Clé globale uniquement.
	Routing Routing `toml:"routing"`
	// Budget : plafond de tokens par session. Effectif après Load : le plus
	// bas du global, du projet et de la politique.
	Budget Budget `toml:"budget"`
	// Memory : mémoire gouvernée. Effectif après Load : le plus strict du
	// global, du projet et de la politique.
	Memory Memory `toml:"memory"`
	// MemoryDisabledBy : "policy" si la politique coupe la mémoire, sinon
	// "config" si le global ou le projet la coupe, sinon "".
	MemoryDisabledBy string `toml:"-"`
	// Usage : facteurs énergie et CO₂ déclarés. Clé globale uniquement.
	Usage Usage `toml:"usage"`
	// ACP : [acp], tenon comme agent d'éditeur. Clé globale uniquement.
	ACP ACP `toml:"acp"`
	// UI : [ui], thème et animation de la TUI. Clé globale uniquement.
	UI UI `toml:"ui"`
	// CRA : assistant Cyber Resilience Act. Clé globale uniquement.
	CRA CRA `toml:"cra"`
	// home : $HOME lu au chargement, pour développer « ~/ » de la politique.
	home string
	// Profile : profil de conformité effectif après Load (le plus strict du
	// global et du projet). Le projet peut seulement le durcir.
	Profile profile.Name `toml:"profile"`
	// WeakerProjectProfile : valeur projet ignorée car plus faible que le
	// profil global. Jamais lue depuis le TOML ; le CLI en tire un avis.
	WeakerProjectProfile profile.Name `toml:"-"`

	// Allowlist : liste blanche d'hôtes de fournisseurs de la config globale
	// (nil si absente). ProjectAllowlist : celle du projet, qui ne peut que
	// restreindre (les listes présentes s'appliquent toutes).
	Allowlist        *ProviderAllowlist `toml:"provider_allowlist"`
	ProjectAllowlist *ProviderAllowlist `toml:"-"`

	// Policy : politique d'organisation appliquée (nil si aucune). Posée par
	// ApplyPolicy, jamais lue depuis le TOML.
	Policy *orgpolicy.Policy `toml:"-"`
	// Avis produits par ApplyPolicy, jamais lus depuis le TOML.
	PolicyOverriddenProviders []string     `toml:"-"` // fournisseurs de la config remplacés par la politique
	PolicyAuditFrom           string       `toml:"-"` // niveau d'audit remplacé ("" si inchangé)
	PolicyTimestampImposed    bool         `toml:"-"` // horodatage de la config remplacé par la politique
	PolicyRoutingLocal        string       `toml:"-"` // fournisseur local imposé par la politique ("" si inchangé)
	PolicyRaisedProfile       profile.Name `toml:"-"` // profil de la config relevé par la politique
	// PolicyStrippedQualifications : identifiants (triés) dont les
	// qualifications de la config ont été retirées par la politique.
	PolicyStrippedQualifications []string `toml:"-"`
}

// Classification : [classification]. Les listes s'additionnent (global,
// projet, politique) ; rien ne peut en retirer.
type Classification struct {
	Paths   []string `toml:"paths"`
	Markers []string `toml:"markers"`
}

// CRA : [cra]. OSVMirror : miroir des archives OSV (https, ou http vers un
// hôte local) ; vide = base publique.
type CRA struct {
	OSVMirror string `toml:"osv_mirror"`
}

// Routing : [routing]. Local : fournisseur de juridiction « local » qui
// reçoit les sessions sensibles ; Fallback : fournisseurs de repli.
type Routing struct {
	Local    string   `toml:"local"`
	Fallback []string `toml:"fallback"`
	// Compaction et Subagents : fournisseur des appels de compaction et des
	// sous-agents ("" = fournisseur de la session).
	Compaction string `toml:"compaction"`
	Subagents  string `toml:"subagents"`
}

// Budget : [budget]. SessionTokens : plafond entrée + sortie par session
// (0 = aucun). WarnAt : fraction qui déclenche l'avis (0,8 par défaut dès
// qu'un plafond existe).
type Budget struct {
	SessionTokens int64   `toml:"session_tokens"`
	WarnAt        float64 `toml:"warn_at"`
}

// Memory : [memory]. Enabled et Project : nil = actif. RetentionDays : durée
// de conservation des entrées user et global (0 = 90 jours).
type Memory struct {
	Enabled       *bool `toml:"enabled"`
	Project       *bool `toml:"project"`
	RetentionDays int   `toml:"retention_days"`
}

// defaultRetentionDays : durée de conservation sans retention_days.
const defaultRetentionDays = 90

// On : la mémoire est active.
func (m Memory) On() bool { return m.Enabled == nil || *m.Enabled }

// ProjectOn : les entrées du dépôt sont lues et écrites.
func (m Memory) ProjectOn() bool { return m.Project == nil || *m.Project }

// Days : durée de conservation effective, en jours.
func (m Memory) Days() int {
	if m.RetentionDays == 0 {
		return defaultRetentionDays
	}
	return m.RetentionDays
}

// checkMemory contrôle retention_days s'il est présent (md dit s'il l'est).
func checkMemory(m Memory, md toml.MetaData) error {
	if md.IsDefined("memory", "retention_days") && (m.RetentionDays < 1 || m.RetentionDays > 3650) {
		return errMemoryRetention
	}
	return nil
}

var errMemoryRetention = errors.New("config: memory.retention_days must be between 1 and 3650")

// ACP : [acp]. AcceptEditorMCP : accepter les serveurs MCP transmis par
// l'éditeur (false par défaut ; la politique ne peut que le forcer à false).
type ACP struct {
	AcceptEditorMCP bool `toml:"accept_editor_mcp"`
}

// UIThemes liste les thèmes de la TUI (même ordre que tui.ThemeNames) ; le
// paquet tui reste la source des palettes, un test les garde alignés.
var UIThemes = []string{"tenon", "tokyonight", "catppuccin", "gruvbox", "light"}

// UI : [ui]. Theme : un des UIThemes ("tenon" par défaut). Animation : nil
// vaut vrai (animation d'ouverture de la TUI).
type UI struct {
	Theme     string `toml:"theme"`
	Animation *bool  `toml:"animation"`
}

// Usage : [usage]. Facteurs déclarés par l'utilisateur, 0 = non déclaré.
type Usage struct {
	WhPerMTok  float64 `toml:"wh_per_mtoken"`
	GCO2PerKWh float64 `toml:"gco2_per_kwh"`
}

// defaultWarnAt : seuil d'avis quand un budget existe sans warn_at.
const defaultWarnAt = 0.8

// finishBudget pose le seuil d'avis par défaut dès qu'un plafond existe.
func (c *Config) finishBudget() {
	if c.Budget.SessionTokens > 0 && c.Budget.WarnAt == 0 {
		c.Budget.WarnAt = defaultWarnAt
	}
}

// lowestBudget renvoie le plus bas des deux plafonds non nuls.
func lowestBudget(a, b int64) int64 {
	if a <= 0 {
		return max(b, 0)
	}
	if b > 0 && b < a {
		return b
	}
	return a
}

// checkBudget contrôle les clés présentes de [budget] ; md dit lesquelles le
// sont (0 explicite est refusé, absent est accepté).
func checkBudget(b Budget, md toml.MetaData) error {
	if md.IsDefined("budget", "session_tokens") && b.SessionTokens <= 0 {
		return errors.New("config: budget.session_tokens must be positive")
	}
	if md.IsDefined("budget", "warn_at") && !(b.WarnAt > 0 && b.WarnAt < 1) {
		return errors.New("config: budget.warn_at must be between 0 and 1")
	}
	return nil
}

// badFactor : vrai pour un facteur non positif, NaN ou infini.
func badFactor(x float64) bool { return !(x > 0) || math.IsInf(x, 0) }

// checkUsage contrôle les facteurs déclarés (positifs s'ils sont présents).
func checkUsage(u Usage, md toml.MetaData) error {
	if md.IsDefined("usage", "wh_per_mtoken") && badFactor(u.WhPerMTok) {
		return errors.New("config: usage.wh_per_mtoken must be positive")
	}
	if md.IsDefined("usage", "gco2_per_kwh") && badFactor(u.GCO2PerKWh) {
		return errors.New("config: usage.gco2_per_kwh must be positive")
	}
	return nil
}

// ClassifyRules renvoie les règles de classification de la configuration.
func (c Config) ClassifyRules() classify.Rules {
	return classify.Rules{Paths: c.Classification.Paths, Markers: c.Classification.Markers}
}

// validateRouting contrôle routing.local et routing.fallback ; à appeler
// quand les fournisseurs sont définitifs (politique comprise).
func (c Config) validateRouting() error {
	if l := c.Routing.Local; l != "" {
		if p, ok := c.Provider(l); !ok || p.Jurisdiction != "local" {
			return errors.New("config: routing.local must name a local provider")
		}
	}
	for _, id := range c.Routing.Fallback {
		if _, ok := c.Provider(id); !ok {
			return fmt.Errorf("config: routing.fallback: unknown provider %q", id)
		}
	}
	for _, k := range []struct{ key, id string }{
		{"compaction", c.Routing.Compaction}, {"subagents", c.Routing.Subagents},
	} {
		if k.id == "" {
			continue
		}
		if _, ok := c.Provider(k.id); !ok {
			return fmt.Errorf("config: routing.%s: unknown provider %q", k.key, k.id)
		}
	}
	return nil
}

// ProviderAllowlist restreint les hôtes des fournisseurs. Local admet les
// hôtes locaux (profile.IsLocalHost).
type ProviderAllowlist struct {
	Hosts []string `toml:"hosts"`
	Local bool     `toml:"local"`
}

// validate refuse une liste vide ou une entrée invalide. prefix vaut
// « config » ou « project config ».
func (a *ProviderAllowlist) validate(prefix string) error {
	if a == nil {
		return nil
	}
	if len(a.Hosts) == 0 && !a.Local {
		return fmt.Errorf("%s: provider_allowlist is empty", prefix)
	}
	for _, h := range a.Hosts {
		if err := hostmatch.CheckEntry(h); err != nil {
			return fmt.Errorf("%s: provider_allowlist: %q: %w", prefix, h, err)
		}
	}
	return nil
}

func checkQualifications(quals []string) error {
	for _, q := range quals {
		if q != profile.QualSecNumCloud {
			return fmt.Errorf("config: unknown qualification %q (%s)", q, profile.QualSecNumCloud)
		}
	}
	return nil
}

// Checkpoints règle la rétention des copies. 0 désactive les copies.
type Checkpoints struct {
	RetentionDays int `toml:"retention_days"`
}

// Hooks règle le chargement des hooks utilisateur.
type Hooks struct {
	ClaudeSettings bool `toml:"claude_settings"` // lit aussi ~/.claude/settings.json
	Disabled       bool `toml:"disabled"`        // ne charge aucun hook
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
	// Qualifications : qualifications déclarées (« secnumcloud »). Déclaratif,
	// jamais vérifié ; ignoré sous politique d'organisation.
	Qualifications []string `toml:"qualifications"`
}

var mcpIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// ValidMCPID indique si id est un identifiant de serveur MCP valide.
func ValidMCPID(id string) bool { return mcpIDPattern.MatchString(id) }

// ValidateMCPURL contrôle l'URL d'un serveur MCP http : https (ou http vers
// l'hôte local), sans identifiants, requête ni fragment.
func ValidateMCPURL(id, raw string) error {
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("config: invalid url for MCP server %q", id)
	}
	if u.User != nil {
		return fmt.Errorf("config: url of MCP server %q: credentials not allowed in the URL (use bearer_env)", id)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return fmt.Errorf("config: url of MCP server %q: query and fragment not allowed in the URL (token via bearer_env)", id)
	}
	switch u.Scheme {
	case "https":
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
		default:
			return fmt.Errorf("config: url of MCP server %q: https required (http only to localhost, 127.0.0.1 or ::1)", id)
		}
	default:
		return fmt.Errorf("config: url of MCP server %q: unsupported scheme %q (https)", id, u.Scheme)
	}
	return nil
}

// ValidateMCPCommand contrôle la commande d'un serveur MCP stdio : présente,
// chemin absolu ou nom nu (cherché dans PATH).
func ValidateMCPCommand(id string, command []string) error {
	if len(command) == 0 || command[0] == "" {
		return fmt.Errorf("config: command required for stdio MCP server %q", id)
	}
	// Un chemin relatif serait résolu depuis le projet, non fiable.
	if c := command[0]; !filepath.IsAbs(c) && strings.ContainsRune(c, '/') {
		return fmt.Errorf("config: command of stdio MCP server %q: absolute path or bare name (looked up in PATH) required, not %q", id, c)
	}
	return nil
}

func (m MCP) validate() error {
	seen := map[string]bool{}
	for _, s := range m.Servers {
		if !ValidMCPID(s.ID) {
			return fmt.Errorf("config: invalid MCP server id %q ([a-z0-9_-]{1,32})", s.ID)
		}
		if seen[s.ID] {
			return fmt.Errorf("config: duplicate MCP server id %q", s.ID)
		}
		seen[s.ID] = true
		switch s.Transport {
		case "stdio":
			if err := ValidateMCPCommand(s.ID, s.Command); err != nil {
				return err
			}
		case "http":
			if err := ValidateMCPURL(s.ID, s.URL); err != nil {
				return err
			}
			if s.Jurisdiction == "" {
				return fmt.Errorf("config: jurisdiction required for http MCP server %q (eu|non-eu|local)", s.ID)
			}
		default:
			return fmt.Errorf("config: invalid transport %q for MCP server %q (stdio|http)", s.Transport, s.ID)
		}
		switch s.Jurisdiction {
		case "eu", "non-eu", "local":
		default:
			return fmt.Errorf("config: invalid jurisdiction %q for MCP server %q (eu|non-eu|local)", s.Jurisdiction, s.ID)
		}
		switch s.Risk {
		case "", "read", "write", "exec", "network":
		default:
			return fmt.Errorf("config: invalid risk %q for MCP server %q (read|write|exec|network)", s.Risk, s.ID)
		}
	}
	return nil
}

type Audit struct {
	Level     string          `toml:"level"`
	Timestamp *AuditTimestamp `toml:"timestamp"` // globale uniquement
}

// AuditTimestamp configure l'horodatage RFC 3161 du journal d'audit. CAPEM
// vient de la politique d'organisation, jamais du TOML.
type AuditTimestamp struct {
	URL    string `toml:"url"`
	CAFile string `toml:"ca_file"`
	CAPEM  []byte `toml:"-"`
}

// TimestampRoots renvoie les racines de confiance de l'autorité d'horodatage
// et leur origine : "policy" (CAPEM) > "ca_file" > "system" (pool nil).
func (c Config) TimestampRoots() (*x509.CertPool, string, error) {
	ts := c.Audit.Timestamp
	if ts == nil {
		return nil, "system", nil
	}
	if len(ts.CAPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ts.CAPEM) {
			return nil, "", errors.New("audit.timestamp: policy ca: no certificate")
		}
		return pool, "policy", nil
	}
	if ts.CAFile != "" {
		pool, err := loadCAFile(ts.CAFile)
		if err != nil {
			return nil, "", fmt.Errorf("config: audit.timestamp.ca_file: %w", err)
		}
		return pool, "ca_file", nil
	}
	return nil, "system", nil
}

func loadCAFile(path string) (*x509.CertPool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, errors.New("no certificate")
	}
	return pool, nil
}

// validate contrôle l'horodatage : URL https (http pour les hôtes locaux),
// ca_file lisible et contenant un certificat.
func (t *AuditTimestamp) validate() error {
	if t == nil {
		return nil
	}
	if t.URL == "" {
		if t.CAFile != "" {
			return errors.New("config: audit.timestamp.ca_file without url")
		}
		return nil
	}
	if err := tsp.CheckURL(t.URL); err != nil {
		return err
	}
	if t.CAFile != "" {
		if _, err := loadCAFile(t.CAFile); err != nil {
			return fmt.Errorf("config: audit.timestamp.ca_file: %w", err)
		}
	}
	return nil
}

// Sandbox : [sandbox]. Mode : "auto" (défaut) | "required" | "off".
type Sandbox struct {
	Mode     string   `toml:"mode"`
	Write    []string `toml:"write"`
	DenyRead []string `toml:"deny_read"`
}

// sandboxRank ordonne les modes : off < auto < required ; "" vaut auto.
func sandboxRank(m string) int {
	switch m {
	case "off":
		return 0
	case "required":
		return 2
	}
	return 1
}

// stricterSandbox renvoie le plus strict des deux modes ("" vaut auto).
func stricterSandbox(a, b string) string {
	if a == "" {
		a = "auto"
	}
	if b != "" && sandboxRank(b) > sandboxRank(a) {
		return b
	}
	return a
}

func checkSandboxMode(m string) error {
	switch m {
	case "", "auto", "required", "off":
		return nil
	}
	return errors.New("config: sandbox.mode must be auto, required or off")
}

// expandSandboxPaths valide et développe « ~/ » (avec home) pour la clé key.
func expandSandboxPaths(key string, paths []string, home string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, "~/"):
			if home == "" {
				return nil, fmt.Errorf("config: sandbox.%s: HOME not set", key)
			}
			p = filepath.Join(home, p[2:])
		case filepath.IsAbs(p):
		default:
			return nil, fmt.Errorf("config: sandbox.%s: path must be absolute or start with ~/", key)
		}
		out = append(out, p)
	}
	return out, nil
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
	// Qualifications : qualifications déclarées (« secnumcloud »). Déclaratif,
	// jamais vérifié ; ignoré sous politique d'organisation (sauf define).
	Qualifications []string `toml:"qualifications"`
	// StreamUsage : false n'envoie pas stream_options.include_usage ; absent
	// : vrai (l'usage réel est demandé au fournisseur).
	StreamUsage *bool `toml:"stream_usage"`
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
	Policy   bool   `toml:"-"` // règle imposée par la politique d'organisation
	// Network : la commande shell correspondante s'exécute avec réseau
	// (allow ou ask ; jamais en config projet ni en politique).
	Network bool `toml:"network"`
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
	animationOn := true // pointeur frais : le décodage TOML écrit à travers
	return Config{
		Lang:                "en",
		DefaultProvider:     "mistral",
		MaxIterations:       50,
		ShellTimeout:        Duration{2 * time.Minute},
		OutputLimit:         30000,
		CompactionThreshold: 0.8,
		Audit:               Audit{Level: "full"},
		Sandbox:             Sandbox{Mode: "auto"},
		Checkpoints:         Checkpoints{RetentionDays: 30},
		UI:                  UI{Theme: "tenon", Animation: &animationOn},
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

// validate contrôle la configuration complète, fournisseur par défaut compris.
func (c Config) validate() error {
	if err := c.validateFields(); err != nil {
		return err
	}
	if _, ok := c.Provider(c.DefaultProvider); !ok {
		return fmt.Errorf("config: unknown default_provider %q", c.DefaultProvider)
	}
	return c.validateRouting()
}

// validateFields contrôle tout sauf l'existence du fournisseur par défaut,
// qu'une politique d'organisation peut encore définir.
func (c Config) validateFields() error {
	switch c.Lang {
	case "fr", "en":
	default:
		return fmt.Errorf("config: invalid lang %q (fr|en)", c.Lang)
	}
	switch c.Audit.Level {
	case "full", "metadata":
	default:
		return fmt.Errorf("config: invalid audit.level %q (full|metadata)", c.Audit.Level)
	}
	if err := c.Audit.Timestamp.validate(); err != nil {
		return err
	}
	if err := checkSandboxMode(c.Sandbox.Mode); err != nil {
		return err
	}
	if c.CompactionThreshold <= 0 || c.CompactionThreshold >= 1 {
		return fmt.Errorf("config: invalid compaction_threshold %v (]0,1[)", c.CompactionThreshold)
	}
	for _, p := range c.Providers {
		if err := checkQualifications(p.Qualifications); err != nil {
			return err
		}
		if p.ContextWindow < 0 {
			return fmt.Errorf("config: negative context_window for provider %q", p.ID)
		}
		switch p.Detect {
		case "", "off", "ollama", "vllm":
		default:
			return fmt.Errorf("config: invalid detect %q for provider %q (ollama|vllm|off)", p.Detect, p.ID)
		}
		switch p.Jurisdiction {
		case "eu", "non-eu", "local":
		default:
			return fmt.Errorf("config: invalid jurisdiction %q for provider %q (eu|non-eu|local)", p.Jurisdiction, p.ID)
		}
	}
	for _, r := range c.Permissions.Rules {
		switch r.Decision {
		case "allow", "ask", "deny":
		default:
			return fmt.Errorf("config: invalid decision %q (allow|ask|deny)", r.Decision)
		}
		if r.Network {
			if r.Tool != "shell" {
				return errors.New("config: network only for tool shell")
			}
			if r.Decision == "deny" {
				return errors.New("config: network only with allow or ask")
			}
		}
	}
	for _, r := range c.Redaction.Rules {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("config: invalid redaction pattern %q: %w", r.Name, err)
		}
	}
	if c.Budget.SessionTokens < 0 {
		return errors.New("config: budget.session_tokens must be positive")
	}
	if c.Budget.WarnAt != 0 && !(c.Budget.WarnAt > 0 && c.Budget.WarnAt < 1) {
		return errors.New("config: budget.warn_at must be between 0 and 1")
	}
	if c.Memory.RetentionDays != 0 && (c.Memory.RetentionDays < 1 || c.Memory.RetentionDays > 3650) {
		return errMemoryRetention
	}
	if c.Usage.WhPerMTok != 0 && badFactor(c.Usage.WhPerMTok) {
		return errors.New("config: usage.wh_per_mtoken must be positive")
	}
	if c.Usage.GCO2PerKWh != 0 && badFactor(c.Usage.GCO2PerKWh) {
		return errors.New("config: usage.gco2_per_kwh must be positive")
	}
	if !slices.Contains(UIThemes, c.UI.Theme) {
		return fmt.Errorf("config: unknown ui.theme %q", c.UI.Theme)
	}
	if c.Checkpoints.RetentionDays < 0 {
		return errors.New("config: retention_days must be >= 0")
	}
	if err := c.MCP.validate(); err != nil {
		return err
	}
	for _, s := range c.MCP.Servers {
		if err := checkQualifications(s.Qualifications); err != nil {
			return err
		}
	}
	if err := c.Allowlist.validate("config"); err != nil {
		return err
	}
	if err := c.ProjectAllowlist.validate("project config"); err != nil {
		return err
	}
	return nil
}
