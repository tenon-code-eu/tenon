package compliance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// tokensLog : s1 (mistral) deux appels dont un estimé, une vue façonnée ;
// s2 (openai) un appel réel avec cache.
func tokensLog(t *testing.T) Dataset {
	b := newLog(t)
	b.start("s1", provEU)
	b.add("s1", event.Usage, event.UsagePayload{Provider: "mistral", Model: "large", Purpose: "turn", Input: 1000, Output: 200, Cached: 400})
	b.add("s1", event.ContextShaped, event.ContextShapedPayload{Masked: []string{"c1"}, SavedTokens: 300})
	b.add("s1", event.Usage, event.UsagePayload{Provider: "mistral", Model: "large", Purpose: "compaction", Input: 600, Output: 200, Estimated: true})
	b.start("s2", provUS)
	b.add("s2", event.Usage, event.UsagePayload{Provider: "openai", Model: "gpt", Purpose: "subagent", Input: 500, Output: 100, Cached: 50, ParentCallID: "a1"})
	return b.dataset()
}

func TestExportTokens(t *testing.T) {
	r := Build(tokensLog(t), Options{})
	if r.Tokens == nil {
		t.Fatal("section tokens absente")
	}
	tk := r.Tokens
	if tk.Total != (TokenCounts{Input: 2100, Output: 500, Cached: 450, EstimatedShare: 0.3077, Saved: 300}) {
		t.Fatalf("total = %+v", tk.Total)
	}
	if len(tk.Sessions) != 2 || tk.Sessions[0].Session != "s1" ||
		tk.Sessions[0].TokenCounts != (TokenCounts{Input: 1600, Output: 400, Cached: 400, EstimatedShare: 0.4, Saved: 300}) ||
		tk.Sessions[1].TokenCounts != (TokenCounts{Input: 500, Output: 100, Cached: 50, Saved: 0}) {
		t.Fatalf("sessions = %+v", tk.Sessions)
	}
	if len(tk.Providers) != 2 || tk.Providers[0].ID != "mistral" || tk.Providers[0].Model != "large" ||
		tk.Providers[0].Input != 1600 || tk.Providers[0].Saved != 300 || tk.Providers[1].ID != "openai" || tk.Providers[1].Saved != 0 {
		t.Fatalf("providers = %+v", tk.Providers)
	}
}

func TestExportEnergyOnlyDeclared(t *testing.T) {
	ds := tokensLog(t)
	for _, o := range []Options{{}, {WhPerMTok: 120}, {GCO2PerKWh: 50}} {
		if r := Build(ds, o); r.Tokens.Energy != nil {
			t.Fatalf("énergie sans les deux facteurs (%+v) : %+v", o, r.Tokens.Energy)
		}
	}
	r := Build(ds, Options{WhPerMTok: 1000, GCO2PerKWh: 50})
	// (2100+500)/1e6 × 1000 = 2,6 Wh ; 2,6/1000 × 50 = 0,13 g.
	e := r.Tokens.Energy
	// Ruling R14 : les facteurs utilisés sont enregistrés.
	if e == nil || *e != (Energy{Wh: 2.6, GCO2e: 0.13, WhPerMTok: 1000, GCO2PerKWh: 50}) {
		t.Fatalf("energy = %+v", e)
	}
	js, err := json.Marshal(e)
	if err != nil || string(js) != `{"wh":2.6,"gco2e":0.13,"wh_per_mtoken":1000,"gco2_per_kwh":50}` {
		t.Fatalf("json %s %v", js, err)
	}
	// Trois chiffres significatifs.
	out := render(t, r, i18n.FR)
	if !strings.Contains(out, "≈ 2,60 Wh · ≈ 0,130 g CO₂e (facteurs déclarés : 1000 Wh/Mtoken, 50 g CO₂e/kWh)") {
		t.Fatalf("ligne FR absente : %s", out)
	}
	if out := render(t, r, i18n.EN); !strings.Contains(out, "≈ 2.60 Wh · ≈ 0.130 g CO₂e (declared factors: 1000 Wh/Mtoken, 50 g CO₂e/kWh)") {
		t.Fatalf("ligne EN absente")
	}
	// Petite session : jamais « 0,00 » pour une valeur non nulle.
	small := Build(ds, Options{WhPerMTok: 0.5, GCO2PerKWh: 52})
	if out := render(t, small, i18n.FR); !strings.Contains(out, "≈ 0,00130 Wh · ≈ 0,0000676 g CO₂e (facteurs déclarés : 0,5 Wh/Mtoken, 52 g CO₂e/kWh)") {
		t.Fatalf("petite session : %s", out)
	}
	if out := render(t, Build(ds, Options{}), i18n.EN); strings.Contains(out, "Wh") {
		t.Fatal("énergie rendue sans facteurs")
	}
}

func TestExportTokensOmittedWithoutUsage(t *testing.T) {
	b := newLog(t)
	b.start("s1", provEU)
	r := Build(b.dataset(), Options{WhPerMTok: 1, GCO2PerKWh: 1})
	if r.Tokens != nil {
		t.Fatalf("section présente sans Usage : %+v", r.Tokens)
	}
}
