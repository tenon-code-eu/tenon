package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/provider"
)

// minAgentWindow : sous cette fenêtre (jetons), consignes et schémas des
// outils occupent l'essentiel du contexte ; l'agent devient inutilisable.
const minAgentWindow = 16000

// detectFuncs : détection par valeur de config.Provider.Detect.
var detectFuncs = map[string]func(context.Context, *http.Client, string, string) (provider.Detected, error){
	"ollama": provider.DetectOllama,
	"vllm":   provider.DetectVLLM,
}

// detectLocal complète model et context_window d'un fournisseur local
// (Ollama, vLLM) en interrogeant son serveur, à l'adresse configurée
// seulement. Une valeur fixée par la configuration n'est jamais remplacée.
// Renvoie les avis à afficher au démarrage. noProxy (profil strict-local) :
// aucun proxy d'environnement, comme pour les appels au modèle.
func detectLocal(ctx context.Context, lang i18n.Lang, pc config.Provider, noProxy bool) (config.Provider, []string, error) {
	f, ok := detectFuncs[pc.Detect]
	if !ok || (pc.Model != "" && pc.ContextWindow != 0) {
		return pc, nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute) // chargement du modèle compris
	defer cancel()
	var client *http.Client
	if noProxy {
		client = provider.NoProxyDetectClient()
	}
	d, err := f(ctx, client, pc.BaseURL, pc.Model)
	switch {
	case errors.Is(err, provider.ErrNoModel):
		return pc, nil, errors.New(i18n.T(lang, "err.detect_no_model", pc.Name, pc.BaseURL))
	case errors.Is(err, provider.ErrModelNotFound):
		return pc, nil, errors.New(i18n.T(lang, "err.detect_model", pc.Name, err))
	case err != nil:
		return pc, nil, errors.New(i18n.T(lang, "err.detect", pc.Name, pc.BaseURL, err))
	}
	if pc.Model == "" {
		pc.Model = d.Model
	}
	if pc.ContextWindow == 0 {
		pc.ContextWindow = d.ContextWindow
	}
	notices := []string{i18n.T(lang, "detect.ok", pc.Name, pc.Model, pc.Window())}
	if pc.Window() < minAgentWindow {
		notices = append(notices, i18n.T(lang, "detect.small_window", pc.Window()))
	}
	return pc, notices, nil
}
