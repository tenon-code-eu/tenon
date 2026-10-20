package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func fakeOllama(t *testing.T, tags string, ctxLen int) (*httptest.Server, *[]string) {
	t.Helper()
	var loaded []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(tags))
		case "/api/generate":
			b, _ := io.ReadAll(r.Body)
			loaded = append(loaded, string(b))
			_, _ = w.Write([]byte(`{"done":true}`))
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:8b","context_length":` + strconv.Itoa(ctxLen) + `}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &loaded
}

const tagsJSON = `{"models":[
 {"name":"glm:latest","modified_at":"2026-10-16T18:00:00+02:00","details":{"context_length":131072},"capabilities":["completion"]},
 {"name":"qwen3:8b","modified_at":"2026-10-16T16:16:18+00:00","details":{"context_length":40960},"capabilities":["completion","tools"]},
 {"name":"old:latest","modified_at":"2025-01-01T00:00:00Z","details":{"context_length":8192},"capabilities":["tools"]}
]}`

func TestDetectOllamaPicksRecentToolModel(t *testing.T) {
	srv, loaded := fakeOllama(t, tagsJSON, 4096)
	d, err := DetectOllama(context.Background(), nil, srv.URL+"/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	// glm, plus récent, n'accepte pas les outils.
	if d.Model != "qwen3:8b" || d.ContextWindow != 4096 || d.ModelWindow != 40960 {
		t.Fatalf("détecté : %+v", d)
	}
	if len(*loaded) != 1 || !strings.Contains((*loaded)[0], `"qwen3:8b"`) {
		t.Fatalf("modèle non chargé : %q", *loaded)
	}
}

func TestDetectOllamaNamedModel(t *testing.T) {
	srv, _ := fakeOllama(t, tagsJSON, 32768)
	d, err := DetectOllama(context.Background(), nil, srv.URL+"/v1/", "qwen3:8b")
	if err != nil || d.Model != "qwen3:8b" || d.ContextWindow != 32768 {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := DetectOllama(context.Background(), nil, srv.URL+"/v1", "devstral"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("modèle absent : %v", err)
	}
	// « old » désigne « old:latest ».
	if d, err := DetectOllama(context.Background(), nil, srv.URL+"/v1", "old"); err != nil || d.Model != "old:latest" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDetectOllamaNoToolModel(t *testing.T) {
	srv, _ := fakeOllama(t, `{"models":[{"name":"glm:latest","capabilities":["completion"]}]}`, 0)
	if _, err := DetectOllama(context.Background(), nil, srv.URL+"/v1", ""); !errors.Is(err, ErrNoModel) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetectOllamaUnreachable(t *testing.T) {
	srv, _ := fakeOllama(t, tagsJSON, 0)
	srv.Close()
	if _, err := DetectOllama(context.Background(), nil, srv.URL+"/v1", ""); err == nil {
		t.Fatal("serveur fermé : pas d'erreur")
	}
}

func TestDetectVLLM(t *testing.T) {
	body := `{"data":[{"id":"Qwen/Qwen3-8B","max_model_len":32768}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	d, err := DetectVLLM(context.Background(), nil, srv.URL+"/v1", "")
	if err != nil || d.Model != "Qwen/Qwen3-8B" || d.ContextWindow != 32768 {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := DetectVLLM(context.Background(), nil, srv.URL+"/v1", "autre"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("err = %v", err)
	}
	body = `{"data":[{"id":"a"},{"id":"b"}]}`
	if _, err := DetectVLLM(context.Background(), nil, srv.URL+"/v1", ""); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("plusieurs modèles : %v", err)
	}
	body = `{"data":[]}`
	if _, err := DetectVLLM(context.Background(), nil, srv.URL+"/v1", ""); !errors.Is(err, ErrNoModel) {
		t.Fatalf("aucun modèle : %v", err)
	}
}

// Sous strict-local, la détection ne passe par aucun proxy d'environnement.
func TestNoProxyDetectClient(t *testing.T) {
	c := NoProxyDetectClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatalf("transport : %#v, Proxy doit être nil", c.Transport)
	}
	if c.CheckRedirect == nil || c.Timeout != detectClient(nil).Timeout {
		t.Fatalf("redirections ou délai perdus : %+v", c)
	}
	if http.DefaultTransport.(*http.Transport).Proxy == nil {
		t.Fatal("http.DefaultTransport modifié")
	}
}
