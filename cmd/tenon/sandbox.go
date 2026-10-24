package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/sandbox"
)

// probeSandbox sonde le mécanisme d'isolation (remplacée dans les tests).
var probeSandbox = sandbox.Probe

// sandboxNotices active les avis du bac à sable au démarrage (désactivés dans
// les tests qui ne portent pas sur lui). Les refus du mode required ne sont
// jamais concernés.
var sandboxNotices = true

// environ renvoie l'environnement de tenon, base de celui des commandes
// shell (remplacée dans les tests).
var environ = os.Environ

// sandboxMode renvoie le mode effectif de cfg ("" vaut auto).
func sandboxMode(cfg config.Config) string {
	if cfg.Sandbox.Mode == "" {
		return "auto"
	}
	return cfg.Sandbox.Mode
}

// sandboxStatus renvoie, pour cfg, l'état de l'isolation : mode off, aucune
// sonde ; sinon la sonde (une fois par processus), normalisée.
func sandboxStatus(cfg config.Config) (string, sandbox.Status) {
	mode := sandboxMode(cfg)
	if mode == "off" {
		return mode, sandbox.Status{Mechanism: "none", Network: "none"}
	}
	st := probeSandbox()
	if !st.Available {
		st.Mechanism, st.Network = "none", "none"
	}
	if st.Mechanism == "" {
		st.Mechanism = "none"
	}
	if st.Network == "" {
		st.Network = "none"
	}
	return mode, st
}

// sandboxRefusal renvoie le refus traduit du mode required quand
// l'isolation (fichiers ou réseau) manque ; nil sinon.
func sandboxRefusal(lang i18n.Lang, mode string, st sandbox.Status) error {
	if mode != "required" {
		return nil
	}
	switch {
	case !st.Available:
		return errors.New(i18n.T(lang, "sandbox.required_unavailable", st.Reason))
	case st.Network == "none":
		return errors.New(i18n.T(lang, "sandbox.required_network", st.Reason))
	}
	return nil
}

// runtimeSockets sont les sockets d'exécution de conteneurs connus et le bus
// de session D-Bus : les atteindre revient à sortir du bac à sable
// (systemd-run --user, Secret Service).
func runtimeSockets(getenv func(string) string) []string {
	out := []string{"/var/run/docker.sock", "/run/docker.sock", "/run/containerd/containerd.sock"}
	if d := getenv("XDG_RUNTIME_DIR"); d != "" {
		out = append(out, filepath.Join(d, "docker.sock"), filepath.Join(d, "podman", "podman.sock"), filepath.Join(d, "bus"))
	}
	if bus := filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "bus"); !slices.Contains(out, bus) {
		out = append(out, bus)
	}
	return out
}

// sandboxSetup sonde l'isolation une fois et renvoie la Spec de base de
// l'outil shell (nil : commandes non confinées), la description journalisée,
// les avis du démarrage, et le refus du mode required. configDir, dataDir et
// policyPath sont interdits aux commandes, comme les secrets du dossier
// personnel home et sandbox.deny_read.
func sandboxSetup(lang i18n.Lang, cfg config.Config, wd, home, configDir, dataDir, policyPath string, getenv func(string) string) (spec *sandbox.Spec, info event.SandboxInfo, notices []string, err error) {
	mode, st := sandboxStatus(cfg)
	info = event.SandboxInfo{Mode: mode, Mechanism: st.Mechanism, Network: st.Network, Reason: st.Reason}
	if mode == "off" {
		return nil, info, nil, nil
	}
	if err := sandboxRefusal(lang, mode, st); err != nil {
		return nil, info, nil, err
	}
	if !st.Available {
		notices = append(notices, i18n.T(lang, "sandbox.unavailable", st.Reason))
	} else {
		switch st.Network {
		case "tcp":
			notices = append(notices, i18n.T(lang, "sandbox.tcp_only"))
		case "none":
			notices = append(notices, i18n.T(lang, "sandbox.network_open", st.Reason))
		}
		// Sockets Unix non confinés (Linux, ABI Landlock < 9) : chaque socket
		// d'exécution présent reste joignable depuis une commande confinée.
		if !st.UnixSockets {
			for _, p := range runtimeSockets(getenv) {
				if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
					notices = append(notices, i18n.T(lang, "sandbox.unix_socket", p))
				}
			}
		}
		spec = &sandbox.Spec{
			Dir:      wd,
			Write:    append(sandbox.DefaultWrite(wd, home, getenv, runtime.GOOS), cfg.Sandbox.Write...),
			DenyRead: append(sandbox.DefaultDenyRead(home, configDir, dataDir, policyPath), cfg.Sandbox.DenyRead...),
			// macOS seulement (l'assistant Linux l'ignore, voir Spec.DenyWrite).
			DenyWrite: sandbox.GitDenyWrite(wd),
		}
	}
	if !sandboxNotices {
		notices = nil
	}
	return spec, info, notices, nil
}

// sandboxPaths renvoie le dossier personnel, le dossier de configuration et
// de données de tenon, et le fichier de politique d'organisation ("" sans
// dossier de politique), tous interdits aux commandes confinées.
func sandboxPaths(getenv func(string) string) (home, configDir, dataDir, policyPath string) {
	if dir, _ := orgpolicy.Resolve(getenv); dir != "" {
		policyPath = filepath.Join(dir, "policy.toml")
	}
	return getenv("HOME"), filepath.Dir(config.GlobalPath(getenv)), config.DataDir(getenv), policyPath
}

// configDeny renvoie le fichier de configuration explicite (--config)
// globalPath à interdire aux commandes : seulement s'il diffère du chemin par
// défaut (dont le dossier est déjà interdit) et se trouve hors du projet wd
// (chemins canoniques). Un fichier du projet est versionné ; l'interdire
// bloquerait sous Linux la création de fichiers dans ses dossiers parents.
// Le fichier seul est interdit, jamais son dossier.
func configDeny(globalPath, wd string, getenv func(string) string) []string {
	if globalPath == "" || globalPath == config.GlobalPath(getenv) {
		return nil
	}
	abs, err := filepath.Abs(globalPath)
	if err != nil {
		return nil
	}
	target := canonicalPath(abs)
	if rel, err := filepath.Rel(canonicalPath(wd), target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	return []string{target}
}

// canonicalPath résout les liens symboliques du plus long préfixe existant de
// p (absolu) : un fichier encore absent garde le chemin réel de ses parents.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	var rest []string
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(append([]string{p}, rest...)...)
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}

// classifiedDenyRead renvoie, sous macOS (goos "darwin"), les chemins
// existants désignés par les préfixes littéraux des motifs de
// classification : segments jusqu'au premier joker (`*`), résolus contre la
// racine du projet root (motif absolu : tel quel). Ajoutés au DenyRead de
// l'outil shell (ruling R22), ils empêchent une commande récursive ou un
// motif shell (`grep -r`, `cat */x`) de lire un dossier classé sans le
// nommer, fournisseur local ou non. Un motif sans préfixe littéral
// (`**/*.md`, `*.d`) ou réduit à la racine n'ajoute rien. Sous Linux, rien :
// interdire un chemin du projet y bloquerait la création de fichiers dans ses
// dossiers parents (plan 16, R9) ; limite documentée.
func classifiedDenyRead(patterns []string, root, goos string) []string {
	if goos != "darwin" {
		return nil
	}
	var out []string
	for _, pat := range patterns {
		var lit []string
		for _, seg := range strings.Split(filepath.ToSlash(pat), "/") {
			if strings.Contains(seg, "*") {
				break
			}
			if seg != "" && seg != "." {
				lit = append(lit, seg)
			}
		}
		if len(lit) == 0 {
			continue
		}
		var p string
		if filepath.IsAbs(pat) {
			p = filepath.Join(append([]string{string(filepath.Separator)}, lit...)...)
		} else {
			p = filepath.Join(append([]string{root}, lit...)...)
			// Un motif relatif qui sort du projet (`..`) ne s'y applique pas.
			if rel, err := filepath.Rel(root, p); err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
		}
		if _, err := os.Lstat(p); err != nil || slices.Contains(out, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// shellSpec renvoie la Spec de l'outil shell : spec complétée par les
// chemins de extra en DenyRead (copie : celle des serveurs MCP n'en dépend
// pas). nil si spec est nil.
func shellSpec(spec *sandbox.Spec, extra []string) *sandbox.Spec {
	if spec == nil || len(extra) == 0 {
		return spec
	}
	s := *spec
	s.DenyRead = append(slices.Clone(spec.DenyRead), extra...)
	return &s
}

// mcpSandboxSpec dérive de la Spec du shell celle de base des serveurs MCP
// stdio : sans le projet, rendu inscriptible selon le risk de chaque serveur.
func mcpSandboxSpec(spec *sandbox.Spec, wd string) *sandbox.Spec {
	if spec == nil {
		return nil
	}
	s := *spec
	s.Write = slices.DeleteFunc(slices.Clone(spec.Write), func(p string) bool { return p == wd })
	s.DenyRead = slices.Clone(spec.DenyRead)
	s.DenyWrite = slices.Clone(spec.DenyWrite)
	return &s
}

// shellSandboxed indique que les commandes shell sont confinées réseau
// compris : sans coupure réseau (sonde "none"), la demande ne doit ni dire
// « sans réseau sauf autorisation » ni proposer « Autoriser avec réseau ».
func shellSandboxed(spec *sandbox.Spec, info event.SandboxInfo) bool {
	return spec != nil && info.Network != "none"
}

// sandboxLine renvoie la ligne « config check » de l'isolation.
func sandboxLine(lang i18n.Lang, mode string, st sandbox.Status) string {
	if mode != "off" && !st.Available {
		return i18n.T(lang, "config.sandbox_unavailable", mode, st.Reason)
	}
	return i18n.T(lang, "config.sandbox", mode, st.Mechanism, st.Network)
}

// secretEnvKeep sont les variables de base transmises à tout serveur stdio
// (minimalEnv) : jamais retirées, même nommées par env_from.
var secretEnvKeep = map[string]bool{"PATH": true, "HOME": true, "LANG": true}

// secretEnv renvoie environ sans les variables nommées par un api_key_env
// (tous les fournisseurs configurés, et toujours celles de tous les
// préréglages, même redéfinis), un bearer_env ou un env_from
// (tous les serveurs MCP). Le résultat n'est jamais nil : l'outil shell
// n'hérite plus de l'environnement de tenon.
func secretEnv(cfg config.Config, environ []string) []string {
	drop := map[string]bool{}
	add := func(k string) {
		if k != "" && !secretEnvKeep[k] {
			drop[k] = true
		}
	}
	for _, p := range cfg.Providers {
		add(p.APIKeyEnv)
	}
	for _, k := range config.PresetKeyEnvs() {
		add(k)
	}
	for _, s := range cfg.MCP.Servers {
		add(s.BearerEnv)
		for _, k := range s.EnvFrom {
			add(k)
		}
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			out = append(out, kv)
		}
	}
	return out
}
