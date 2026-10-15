//go:build unix

package mcp

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"codeberg.org/tenon/tenon/internal/config"
	"codeberg.org/tenon/tenon/internal/llm"
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

func TestNonEUServerNoConsentNoCall(t *testing.T) {
	var n atomic.Int32
	m, _ := start(t, []config.MCPServer{nonEUDecl("us")}, Options{
		dial: memServer(t, countingServer(&n)), Consent: func(string) bool { return false },
	})
	tl := toolByName(t, m, "mcp__us__echo")
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
		dial: memServer(t, countingServer(&n)), Consent: func(string) bool { return true },
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
		m, _ := start(t, []config.MCPServer{d}, Options{dial: memServer(t, countingServer(&n))})
		tg, _ := toolByName(t, m, "mcp__srv__echo").Target(json.RawMessage(`{}`))
		if tg.ForceAsk != want {
			t.Errorf("juridiction %q : ForceAsk=%v, attendu %v", jur, tg.ForceAsk, want)
		}
	}
}

func TestLookupCountry(t *testing.T) {
	var n atomic.Int32
	start(t, []config.MCPServer{nonEUDecl("usx")}, Options{dial: memServer(t, countingServer(&n))})
	info, ok := Lookup("mcp__usx__echo")
	if !ok || info.ID != "usx" || info.Country != "US" || info.Jurisdiction != llm.NonEU {
		t.Fatalf("Lookup : %+v %v", info, ok)
	}
	if _, ok := Lookup("mcp__usx__absent"); ok {
		t.Fatal("Lookup d'un outil inconnu")
	}
}
