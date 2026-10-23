package main

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/routing"
)

// newProvider construit un fournisseur compatible OpenAI ; noProxy : sans
// proxy d'environnement. Remplacée dans les tests.
var newProvider = func(info llm.ProviderInfo, key string, noProxy bool) provider.Provider {
	var opts []provider.Option
	if noProxy {
		opts = append(opts, provider.WithoutProxy())
	}
	return provider.NewOpenAICompat(info, key, opts...)
}

// routerDeps regroupe ce dont buildRouter a besoin pour construire un
// fournisseur candidat.
type routerDeps struct {
	ctx  context.Context
	lang i18n.Lang
	// keys : clé d'API de chaque fournisseur configuré (absente : aucune).
	keys map[string]string
}

// buildRouter construit le routeur de la session à partir du fournisseur
// courant current (déjà construit : curProv), de routing.local et des
// replis de cfg. Renvoie aussi la description de SessionStarted.routing
// (nil sans [routing]) et les avis du démarrage.
//
// Sans [routing], un fournisseur courant local est aussi le fournisseur
// local (lectures classées permises, sans repli) ; un fournisseur courant
// distant ne reçoit aucun routeur (comportement d'avant le routage).
// Un repli refusé par le profil ou une liste blanche n'empêche pas le
// démarrage : l'engine le saute au moment de l'envoi. Seul un repli
// inutilisable (clé absente, détection en échec) est ignoré avec un avis.
func buildRouter(cfg config.Config, current config.Provider, curProv provider.Provider, d routerDeps) (*routing.Router, *event.RoutingInfo, []string, error) {
	cur := routing.Candidate{ID: current.ID, Provider: curProv, Window: current.Window()}
	curLocal := current.Jurisdiction == string(llm.Local)
	rt := cfg.Routing
	if rt.Local == "" && len(rt.Fallback) == 0 {
		if curLocal {
			return routing.New(cur, &cur, nil), nil, nil, nil
		}
		return nil, nil, nil, nil
	}
	// Local : le fournisseur local effectif (vide s'il est indisponible).
	info := &event.RoutingInfo{}
	var notices []string
	built := map[string]routing.Candidate{current.ID: cur}

	var local *routing.Candidate
	switch {
	case rt.Local != "":
		pc, ok := cfg.Provider(rt.Local)
		if !ok {
			return nil, nil, nil, errors.New(i18n.T(d.lang, "err.unknown_provider", rt.Local))
		}
		// Ruling R5 : un fournisseur « local » joint par un hôte non local
		// est signalé, sans refus.
		if h := urlHost(pc.BaseURL); !profile.IsLocalHost(h) {
			notices = append(notices, i18n.T(d.lang, "routing.non_loopback", pc.ID, h))
		}
		c, ok := built[pc.ID]
		if !ok {
			var n []string
			var err error
			// Ruling R14 : jamais de proxy pour le fournisseur local.
			c, n, err = routeCandidate(cfg, pc, d, true)
			notices = append(notices, n...)
			if err != nil {
				// Sans fournisseur local, un contenu classé est refusé.
				notices = append(notices, i18n.T(d.lang, "routing.local_unavailable", pc.ID, err))
				break
			}
			built[pc.ID] = c
		}
		local = &c
	case curLocal:
		local = &cur
	}

	fallback := make([]routing.Candidate, 0, len(rt.Fallback))
	for _, id := range rt.Fallback {
		// Le fournisseur courant n'est pas répété (le routeur l'ignorerait).
		if id == current.ID || slices.Contains(info.Fallback, id) {
			continue
		}
		c, ok := built[id]
		if !ok {
			pc, found := cfg.Provider(id)
			if !found {
				return nil, nil, nil, errors.New(i18n.T(d.lang, "err.unknown_provider", id))
			}
			var n []string
			var err error
			c, n, err = routeCandidate(cfg, pc, d, providerNoProxy(cfg.Profile, pc))
			notices = append(notices, n...)
			if err != nil {
				notices = append(notices, i18n.T(d.lang, "routing.fallback_skipped", id, err))
				continue
			}
			built[id] = c
		}
		fallback = append(fallback, c)
		info.Fallback = append(info.Fallback, id)
	}
	if local != nil {
		info.Local = local.ID
	}
	return routing.New(cur, local, fallback), info, notices, nil
}

// routeCandidate construit le fournisseur candidat pc. Comme pour le
// fournisseur courant, la détection d'un modèle local ne contacte l'hôte
// qu'après les contrôles du profil et des listes blanches ; un candidat
// refusé est construit sans détection (l'engine le refusera à l'envoi).
// noProxy : fournisseur et détection sans proxy d'environnement.
func routeCandidate(cfg config.Config, pc config.Provider, d routerDeps, noProxy bool) (routing.Candidate, []string, error) {
	key := d.keys[pc.ID]
	if key == "" && pc.Jurisdiction != string(llm.Local) {
		if pc.APIKeyEnv == "" {
			return routing.Candidate{}, nil, errors.New(i18n.T(d.lang, "err.no_api_key_env", pc.ID))
		}
		return routing.Candidate{}, nil, errors.New(i18n.T(d.lang, "err.missing_env", pc.APIKeyEnv))
	}
	var notices []string
	if providerErr(d.lang, cfg, pc) == nil {
		var err error
		pc, notices, err = detectLocal(d.ctx, d.lang, pc, noProxy)
		if err != nil {
			return routing.Candidate{}, nil, err
		}
	}
	p := newProvider(providerInfo(pc), key, noProxy)
	return routing.Candidate{ID: pc.ID, Provider: p, Window: pc.Window()}, notices, nil
}

// routesLocal dit si le fournisseur courant pc est le fournisseur local du
// routeur : local, et routing.local vide ou égal à pc.
func routesLocal(cfg config.Config, pc config.Provider) bool {
	return pc.Jurisdiction == string(llm.Local) && (cfg.Routing.Local == "" || cfg.Routing.Local == pc.ID)
}

// routingLocal renvoie l'identifiant du fournisseur local du routeur r (""
// sans routeur ni fournisseur local).
func routingLocal(r *routing.Router) string {
	if r == nil {
		return ""
	}
	c, ok := r.Local()
	if !ok {
		return ""
	}
	return c.ID
}

// classificationLine renvoie la ligne « config check » de la classification.
func classificationLine(lang i18n.Lang, cfg config.Config) string {
	return i18n.T(lang, "config.classification", len(cfg.Classification.Paths), len(cfg.Classification.Markers))
}

// routingLine renvoie la ligne « config check » du routage.
func routingLine(lang i18n.Lang, cfg config.Config) string {
	rt := cfg.Routing
	if rt.Local == "" && len(rt.Fallback) == 0 {
		return i18n.T(lang, "config.routing_none")
	}
	local, fb := rt.Local, strings.Join(rt.Fallback, ", ")
	if local == "" {
		local = "-"
	}
	if fb == "" {
		fb = "-"
	}
	return i18n.T(lang, "config.routing", local, fb)
}
