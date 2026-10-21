package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Detected décrit ce qu'un serveur local (Ollama, vLLM) sert réellement :
// le modèle et la fenêtre de contexte appliquée par le serveur, qui peut être
// bien plus petite que celle du modèle (Ollama : 4096 jetons par défaut sur
// une machine modeste).
type Detected struct {
	Model         string
	ContextWindow int // 0 : inconnue
	// ModelWindow est la fenêtre maximale du modèle (Ollama), 0 si inconnue.
	ModelWindow int
}

// ErrNoModel : le serveur ne sert aucun modèle utilisable.
var ErrNoModel = errors.New("no usable model")

// ErrModelNotFound : le modèle demandé n'est pas servi.
var ErrModelNotFound = errors.New("model not found on the server")

// maxDetectBody plafonne chaque réponse lue pendant la détection.
const maxDetectBody = 4 << 20

// detectClient ne suit aucune redirection : la détection ne contacte que
// l'adresse configurée, comme les appels au modèle.
func detectClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{
		Timeout:       2 * time.Minute, // chargement du modèle compris
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NoProxyDetectClient est detectClient(nil) sans proxy d'environnement
// (HTTP_PROXY, HTTPS_PROXY) : profil strict-local, ou secnumcloud avec un
// fournisseur local. http.DefaultTransport n'est pas modifié.
func NoProxyDetectClient() *http.Client {
	c := detectClient(nil)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	c.Transport = tr
	return c
}

func getJSON(ctx context.Context, c *http.Client, method, url string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDetectBody))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s : HTTP %d", method, url, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// ollamaRoot renvoie l'adresse de l'API native d'Ollama à partir de l'adresse
// compatible OpenAI (http://hôte:11434/v1 → http://hôte:11434).
func ollamaRoot(baseURL string) string {
	return strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
}

type ollamaModel struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Details    struct {
		ContextLength int `json:"context_length"`
	} `json:"details"`
	// Capabilities est absent des versions anciennes d'Ollama.
	Capabilities []string `json:"capabilities"`
}

func modTime(m *ollamaModel) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, m.ModifiedAt)
	return t
}

// sameOllamaName : « qwen3 » désigne « qwen3:latest ».
func sameOllamaName(a, b string) bool {
	norm := func(s string) string {
		if !strings.Contains(s, ":") {
			return s + ":latest"
		}
		return s
	}
	return norm(a) == norm(b)
}

// DetectOllama interroge le serveur Ollama de baseURL. Sans model, il choisit
// le modèle modifié le plus récemment parmi ceux qui acceptent les appels
// d'outils. La fenêtre est celle du modèle chargé (/api/ps) : le modèle est
// donc chargé en mémoire, ce que le premier appel ferait de toute façon.
func DetectOllama(ctx context.Context, c *http.Client, baseURL, model string) (Detected, error) {
	c = detectClient(c)
	root := ollamaRoot(baseURL)
	var tags struct {
		Models []ollamaModel `json:"models"`
	}
	if err := getJSON(ctx, c, http.MethodGet, root+"/api/tags", nil, &tags); err != nil {
		return Detected{}, err
	}
	var chosen *ollamaModel
	if model != "" {
		for i := range tags.Models {
			if sameOllamaName(tags.Models[i].Name, model) {
				chosen = &tags.Models[i]
				break
			}
		}
		if chosen == nil {
			return Detected{}, fmt.Errorf("%w : %s", ErrModelNotFound, model)
		}
	} else {
		for i := range tags.Models {
			m := &tags.Models[i]
			if m.Capabilities != nil && !slices.Contains(m.Capabilities, "tools") {
				continue
			}
			if chosen == nil || modTime(m).After(modTime(chosen)) {
				chosen = m
			}
		}
		if chosen == nil {
			return Detected{}, ErrNoModel
		}
	}
	d := Detected{Model: chosen.Name, ModelWindow: chosen.Details.ContextLength}

	// Une requête sans prompt charge le modèle sans rien générer.
	if err := getJSON(ctx, c, http.MethodPost, root+"/api/generate",
		map[string]any{"model": d.Model, "keep_alive": "10m"}, nil); err != nil {
		return d, err
	}
	var ps struct {
		Models []struct {
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	if err := getJSON(ctx, c, http.MethodGet, root+"/api/ps", nil, &ps); err != nil {
		return d, err
	}
	for _, m := range ps.Models {
		if sameOllamaName(m.Name, d.Model) {
			d.ContextWindow = m.ContextLength
		}
	}
	return d, nil
}

// DetectVLLM interroge GET /models du serveur vLLM. Sans model, le serveur
// doit servir un seul modèle. La fenêtre est max_model_len.
func DetectVLLM(ctx context.Context, c *http.Client, baseURL, model string) (Detected, error) {
	c = detectClient(c)
	var list struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if err := getJSON(ctx, c, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil, &list); err != nil {
		return Detected{}, err
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		ids = append(ids, m.ID)
		if model != "" && m.ID == model {
			return Detected{Model: m.ID, ContextWindow: m.MaxModelLen}, nil
		}
	}
	switch {
	case model != "":
		return Detected{}, fmt.Errorf("%w : %s (servis : %s)", ErrModelNotFound, model, strings.Join(ids, ", "))
	case len(list.Data) == 0:
		return Detected{}, ErrNoModel
	case len(list.Data) > 1:
		return Detected{}, fmt.Errorf("several models served (%s): set model in the configuration", strings.Join(ids, ", "))
	}
	return Detected{Model: list.Data[0].ID, ContextWindow: list.Data[0].MaxModelLen}, nil
}
