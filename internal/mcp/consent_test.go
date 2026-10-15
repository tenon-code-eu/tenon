//go:build unix

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"codeberg.org/tenon/tenon/internal/config"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/mcp/mcptest"
	"codeberg.org/tenon/tenon/internal/tools"
)

// countingServer expose « echo » et compte les appels reçus.
func countingServer(n *atomic.Int32) func(*sdk.Server) {
	return func(s *sdk.Server) {
		s.AddTool(&sdk.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				n.Add(1)
				return textResult("ok"), nil
			})
	}
}

func nonEUDecl(id string) config.MCPServer {
	d := httpDecl(id)
	d.Jurisdiction = "non-eu"
	d.Country = "US"
	return d
}

// withoutPreConsent renvoie une copie de l'outil sans consentement d'avance.
// Start ne connecte un serveur hors UE qu'avec AllowNonEU : ForceAsk et
// Consent restent une défense en profondeur, testée sur cette copie.
func withoutPreConsent(t *testing.T, tl tools.Tool) tools.Tool {
	t.Helper()
	c := *tl.(*mcpTool)
	c.opts.AllowNonEU = false
	return &c
}

func TestNonEUServerNoConsentNoCall(t *testing.T) {
	var n atomic.Int32
	m, _ := start(t, []config.MCPServer{nonEUDecl("us")}, Options{
		dial: memServer(t, countingServer(&n)), Consent: func(string) bool { return false }, AllowNonEU: true,
	})
	tl := withoutPreConsent(t, toolByName(t, m, "mcp__us__echo"))
	tg, err := tl.Target(json.RawMessage(`{}`))
	if err != nil || !tg.ForceAsk {
		t.Fatalf("Target %+v (%v) : ForceAsk attendu", tg, err)
	}
	res := run(t, tl, `{"text":"secret"}`)
	if !res.IsError || res.Denied != "policy" || res.Content != "serveur MCP hors UE sans consentement" {
		t.Fatalf("résultat %+v", res)
	}
	if n.Load() != 0 {
		t.Fatalf("%d appel(s) reçu(s) par le serveur sans consentement", n.Load())
	}
}

func TestNonEUServerWithConsent(t *testing.T) {
	var n atomic.Int32
	m, _ := start(t, []config.MCPServer{nonEUDecl("us")}, Options{
		dial: memServer(t, countingServer(&n)), Consent: func(string) bool { return true }, AllowNonEU: true,
	})
	res := run(t, toolByName(t, m, "mcp__us__echo"), `{}`)
	if res.IsError || res.Content != "ok" || n.Load() != 1 {
		t.Fatalf("résultat %+v, %d appel(s)", res, n.Load())
	}
}

// Avec le consentement d'avance (--allow-non-eu), la validation n'est plus forcée.
func TestNonEUPreConsentNoForceAsk(t *testing.T) {
	var n atomic.Int32
	m, _ := start(t, []config.MCPServer{nonEUDecl("us")}, Options{dial: memServer(t, countingServer(&n)), AllowNonEU: true})
	tg, err := toolByName(t, m, "mcp__us__echo").Target(json.RawMessage(`{}`))
	if err != nil || tg.ForceAsk {
		t.Fatalf("Target %+v (%v) : ForceAsk inattendu", tg, err)
	}
}

// Un serveur UE ou local ne force pas la validation ; une juridiction
// inconnue est traitée comme hors UE.
func TestForceAskByJurisdiction(t *testing.T) {
	for jur, want := range map[string]bool{"eu": false, "local": false, "non-eu": true, "": true} {
		var n atomic.Int32
		d := httpDecl("srv")
		d.Jurisdiction = jur
		m, _ := start(t, []config.MCPServer{d}, Options{dial: memServer(t, countingServer(&n)), AllowNonEU: true})
		tg, _ := withoutPreConsent(t, toolByName(t, m, "mcp__srv__echo")).Target(json.RawMessage(`{}`))
		if tg.ForceAsk != want {
			t.Errorf("juridiction %q : ForceAsk=%v, attendu %v", jur, tg.ForceAsk, want)
		}
	}
}

func TestLookupCountry(t *testing.T) {
	var n atomic.Int32
	start(t, []config.MCPServer{nonEUDecl("usx")}, Options{dial: memServer(t, countingServer(&n)), AllowNonEU: true})
	info, ok := Lookup("mcp__usx__echo")
	if !ok || info.ID != "usx" || info.Country != "US" || info.Jurisdiction != llm.NonEU {
		t.Fatalf("Lookup : %+v %v", info, ok)
	}
	if _, ok := Lookup("mcp__usx__absent"); ok {
		t.Fatal("Lookup d'un outil inconnu")
	}
}

// Sans --allow-non-eu, un serveur hors UE (ou de juridiction inconnue) n'est
// ni lancé ni contacté : aucun octet ne lui parvient. Il est signalé Down
// (NonEUSkipped).
func TestNonEUServerNotStartedWithoutConsent(t *testing.T) {
	var reqs atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		http.Error(w, "non", http.StatusTeapot)
	}))
	defer ts.Close()
	marker := filepath.Join(t.TempDir(), "spawned")
	stdio, getenv := stdioServer(t, "loin", "", map[string]string{mcptest.EnvLog: marker})
	stdio.EnvFrom = append(stdio.EnvFrom, mcptest.EnvLog)
	stdio.Jurisdiction = "non-eu"
	web := config.MCPServer{ID: "web", Transport: "http", URL: ts.URL + "/mcp", Jurisdiction: "non-eu"}
	unknown := config.MCPServer{ID: "inconnu", Transport: "http", URL: ts.URL + "/mcp"}

	m, down := start(t, []config.MCPServer{stdio, web, unknown}, Options{Getenv: getenv})
	if len(m.Tools()) != 0 || len(down) != 3 {
		t.Fatalf("outils %d, down %+v", len(m.Tools()), down)
	}
	for _, d := range down {
		if !d.NonEUSkipped || d.Error == "" {
			t.Fatalf("down %+v", d)
		}
	}
	if n := reqs.Load(); n != 0 {
		t.Fatalf("%d requête(s) reçue(s) sans consentement", n)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("serveur stdio lancé sans consentement (%v)", err)
	}

	// Avec le consentement d'avance : lancé normalement.
	m, down = start(t, []config.MCPServer{stdio}, Options{Getenv: getenv, AllowNonEU: true})
	if len(down) != 0 || len(m.Tools()) == 0 {
		t.Fatalf("avec consentement : down %+v", down)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("serveur stdio non lancé : %v", err)
	}
}
