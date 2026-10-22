package config

import (
	"errors"
	"reflect"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
)

func TestRoutingLocalNotLocal(t *testing.T) {
	_, err := loadSB(t, "[routing]\nlocal = \"mistral\"\n", "")
	wantErr(t, err, "config: routing.local must name a local provider")
	_, err = loadSB(t, "[routing]\nlocal = \"nope\"\n", "")
	wantErr(t, err, "config: routing.local must name a local provider")
	cfg, err := loadSB(t, "[routing]\nlocal = \"ollama\"\nfallback = [\"mistral\"]\n", "")
	if err != nil || cfg.Routing.Local != "ollama" || !reflect.DeepEqual(cfg.Routing.Fallback, []string{"mistral"}) {
		t.Fatalf("%v %+v", err, cfg.Routing)
	}
}

func TestRoutingFallbackUnknown(t *testing.T) {
	_, err := loadSB(t, "[routing]\nfallback = [\"nope\"]\n", "")
	wantErr(t, err, `config: routing.fallback: unknown provider "nope"`)
}

func TestRoutingProject(t *testing.T) {
	_, err := loadSB(t, "", "[routing]\nlocal = \"ollama\"\n")
	wantErr(t, err, "config: routing not allowed in project configuration")
	_, err = loadSB(t, "", "[routing]\n")
	wantErr(t, err, "config: routing not allowed in project configuration")
}

func TestClassificationUnion(t *testing.T) {
	cfg, err := loadSB(t,
		"[classification]\npaths = [\"a/**\"]\nmarkers = [\"M1\"]\n",
		"[classification]\npaths = [\"b/**\"]\nmarkers = [\"M2\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.ClassifyRules()
	if !reflect.DeepEqual(r.Paths, []string{"a/**", "b/**"}) || !reflect.DeepEqual(r.Markers, []string{"M1", "M2"}) {
		t.Fatalf("%+v", r)
	}
}

func TestPolicyRouting(t *testing.T) {
	g := write(t, "[routing]\nlocal = \"vllm\"\n[classification]\npaths = [\"g/**\"]\n")
	pol := &orgpolicy.Policy{ID: "p",
		Classification: orgpolicy.Classification{Paths: []string{"o/**"}, Markers: []string{"SECRET X"}},
		Routing:        orgpolicy.Routing{Local: "ollama"}}
	cfg, err := LoadPolicy(g, "", env(nil), pol)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routing.Local != "ollama" || cfg.PolicyRoutingLocal != "ollama" {
		t.Fatalf("%+v %q", cfg.Routing, cfg.PolicyRoutingLocal)
	}
	r := cfg.ClassifyRules()
	if !reflect.DeepEqual(r.Paths, []string{"g/**", "o/**"}) || !reflect.DeepEqual(r.Markers, []string{"SECRET X"}) {
		t.Fatalf("%+v", r)
	}
	// Fournisseur local défini par la politique.
	pol = &orgpolicy.Policy{ID: "p",
		Providers: &orgpolicy.Providers{Define: []orgpolicy.ProviderDef{{ID: "interne", BaseURL: "http://gpu:8000/v1", Jurisdiction: "local"}}},
		Routing:   orgpolicy.Routing{Local: "interne"}}
	if cfg, err = LoadPolicy("", "", env(nil), pol); err != nil || cfg.Routing.Local != "interne" {
		t.Fatalf("%v %+v", err, cfg.Routing)
	}
	// Fournisseur non local : politique refusée.
	pol = &orgpolicy.Policy{ID: "p", Routing: orgpolicy.Routing{Local: "mistral"}}
	_, err = LoadPolicy("", "", env(nil), pol)
	if !errors.Is(err, orgpolicy.ErrPolicy) || err.Error() != "policy violation: routing.local must name a local provider" {
		t.Fatalf("%v", err)
	}
}
