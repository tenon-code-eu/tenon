package config

import (
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
)

func TestBudgetInvalid(t *testing.T) {
	for _, s := range []string{"0", "-5"} {
		_, err := loadSB(t, "[budget]\nsession_tokens = "+s+"\n", "")
		wantErr(t, err, "config: budget.session_tokens must be positive")
	}
	for _, s := range []string{"0", "1", "1.5", "-0.1", "nan", "inf", "-inf"} {
		_, err := loadSB(t, "[budget]\nsession_tokens = 10\nwarn_at = "+s+"\n", "")
		wantErr(t, err, "config: budget.warn_at must be between 0 and 1")
	}
	_, err := loadSB(t, "", "[budget]\nsession_tokens = 0\n")
	wantErr(t, err, "config: budget.session_tokens must be positive")
}

func TestBudgetDefaults(t *testing.T) {
	cfg, err := loadSB(t, "", "")
	if err != nil || cfg.Budget.SessionTokens != 0 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
	cfg, err = loadSB(t, "[budget]\nsession_tokens = 1000\n", "")
	if err != nil || cfg.Budget.SessionTokens != 1000 || cfg.Budget.WarnAt != 0.8 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
	cfg, err = loadSB(t, "[budget]\nsession_tokens = 1000\nwarn_at = 0.5\n", "")
	if err != nil || cfg.Budget.WarnAt != 0.5 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
}

func TestBudgetProjectLowestWins(t *testing.T) {
	g := "[budget]\nsession_tokens = 1000\n"
	cfg, err := loadSB(t, g, "[budget]\nsession_tokens = 5000\n")
	if err != nil || cfg.Budget.SessionTokens != 1000 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
	cfg, err = loadSB(t, g, "[budget]\nsession_tokens = 400\n")
	if err != nil || cfg.Budget.SessionTokens != 400 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
	// Sans budget global, le projet en pose un.
	cfg, err = loadSB(t, "", "[budget]\nsession_tokens = 400\n")
	if err != nil || cfg.Budget.SessionTokens != 400 || cfg.Budget.WarnAt != 0.8 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
}

func TestUsage(t *testing.T) {
	cfg, err := loadSB(t, "[usage]\nwh_per_mtoken = 120.5\ngco2_per_kwh = 55\n", "")
	if err != nil || cfg.Usage.WhPerMTok != 120.5 || cfg.Usage.GCO2PerKWh != 55 {
		t.Fatalf("%v %+v", err, cfg.Usage)
	}
	_, err = loadSB(t, "[usage]\nwh_per_mtoken = 0\n", "")
	wantErr(t, err, "config: usage.wh_per_mtoken must be positive")
	_, err = loadSB(t, "[usage]\ngco2_per_kwh = -1\n", "")
	wantErr(t, err, "config: usage.gco2_per_kwh must be positive")
	for _, v := range []string{"nan", "inf", "-inf"} {
		_, err = loadSB(t, "[usage]\nwh_per_mtoken = "+v+"\n", "")
		wantErr(t, err, "config: usage.wh_per_mtoken must be positive")
		_, err = loadSB(t, "[usage]\ngco2_per_kwh = "+v+"\n", "")
		wantErr(t, err, "config: usage.gco2_per_kwh must be positive")
	}
	_, err = loadSB(t, "", "[usage]\nwh_per_mtoken = 1\n")
	wantErr(t, err, "config: usage not allowed in project configuration")
	_, err = loadSB(t, "", "[usage]\n")
	wantErr(t, err, "config: usage not allowed in project configuration")
}

func TestRoutingSmallModels(t *testing.T) {
	cfg, err := loadSB(t, "[routing]\ncompaction = \"ollama\"\nsubagents = \"mistral\"\n", "")
	if err != nil || cfg.Routing.Compaction != "ollama" || cfg.Routing.Subagents != "mistral" {
		t.Fatalf("%v %+v", err, cfg.Routing)
	}
	_, err = loadSB(t, "[routing]\ncompaction = \"nope\"\n", "")
	wantErr(t, err, `config: routing.compaction: unknown provider "nope"`)
	_, err = loadSB(t, "[routing]\nsubagents = \"nope\"\n", "")
	wantErr(t, err, `config: routing.subagents: unknown provider "nope"`)
	_, err = loadSB(t, "", "[routing]\ncompaction = \"ollama\"\n")
	wantErr(t, err, "config: routing not allowed in project configuration")
}

func TestPolicyBudget(t *testing.T) {
	g := write(t, "[budget]\nsession_tokens = 5000\n")
	pol := &orgpolicy.Policy{ID: "p", Budget: orgpolicy.Budget{SessionTokens: 2000}}
	cfg, err := LoadPolicy(g, "", env(nil), pol)
	if err != nil || cfg.Budget.SessionTokens != 2000 || cfg.Budget.WarnAt != 0.8 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
	// Budget global plus bas que la politique : il reste.
	pol.Budget.SessionTokens = 9000
	if cfg, err = LoadPolicy(g, "", env(nil), pol); err != nil || cfg.Budget.SessionTokens != 5000 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
	// Sans budget de politique : inchangé.
	if cfg, err = LoadPolicy(g, "", env(nil), &orgpolicy.Policy{ID: "p"}); err != nil || cfg.Budget.SessionTokens != 5000 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
	// Sans budget global.
	pol.Budget.SessionTokens = 300
	if cfg, err = LoadPolicy("", "", env(nil), pol); err != nil || cfg.Budget.SessionTokens != 300 || cfg.Budget.WarnAt != 0.8 {
		t.Fatalf("%v %+v", err, cfg.Budget)
	}
}

func TestMemoryInvalid(t *testing.T) {
	for _, s := range []string{"0", "3651", "-1"} {
		_, err := loadSB(t, "[memory]\nretention_days = "+s+"\n", "")
		wantErr(t, err, "config: memory.retention_days must be between 1 and 3650")
		_, err = loadSB(t, "", "[memory]\nretention_days = "+s+"\n")
		wantErr(t, err, "config: memory.retention_days must be between 1 and 3650")
	}
}

func TestMemoryDefaultsAndHardening(t *testing.T) {
	cfg, err := loadSB(t, "", "")
	if err != nil || !cfg.Memory.On() || !cfg.Memory.ProjectOn() || cfg.Memory.Days() != 90 || cfg.MemoryDisabledBy != "" {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
	cfg, err = loadSB(t, "[memory]\nenabled = false\n", "[memory]\nenabled = true\n")
	if err != nil || cfg.Memory.On() || cfg.MemoryDisabledBy != "config" {
		t.Fatalf("%v %+v %q", err, cfg.Memory, cfg.MemoryDisabledBy)
	}
	cfg, err = loadSB(t, "", "[memory]\nenabled = false\n")
	if err != nil || cfg.Memory.On() || cfg.MemoryDisabledBy != "config" {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
	cfg, err = loadSB(t, "[memory]\nproject = false\n", "[memory]\nproject = true\n")
	if err != nil || cfg.Memory.ProjectOn() {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
	cfg, err = loadSB(t, "", "[memory]\nproject = false\n")
	if err != nil || cfg.Memory.ProjectOn() || !cfg.Memory.On() {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
	cfg, err = loadSB(t, "", "[memory]\nretention_days = 30\n")
	if err != nil || cfg.Memory.Days() != 30 {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
	cfg, err = loadSB(t, "", "[memory]\nretention_days = 365\n")
	if err != nil || cfg.Memory.Days() != 90 {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
	cfg, err = loadSB(t, "[memory]\nretention_days = 10\n", "[memory]\nretention_days = 20\n")
	if err != nil || cfg.Memory.Days() != 10 {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
}

func TestPolicyMemory(t *testing.T) {
	g := write(t, "[memory]\nenabled = false\n")
	cfg, err := LoadPolicy(g, "", env(nil), &orgpolicy.Policy{ID: "p", Memory: orgpolicy.Memory{Disabled: true}})
	if err != nil || cfg.Memory.On() || cfg.MemoryDisabledBy != "policy" {
		t.Fatalf("%v %+v %q", err, cfg.Memory, cfg.MemoryDisabledBy)
	}
	cfg, err = LoadPolicy("", "", env(nil), &orgpolicy.Policy{ID: "p", Memory: orgpolicy.Memory{NoProject: true, MaxRetentionDays: 30}})
	if err != nil || !cfg.Memory.On() || cfg.Memory.ProjectOn() || cfg.Memory.Days() != 30 || cfg.MemoryDisabledBy != "" {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
	cfg, err = LoadPolicy(write(t, "[memory]\nretention_days = 10\n"), "", env(nil), &orgpolicy.Policy{ID: "p", Memory: orgpolicy.Memory{MaxRetentionDays: 30}})
	if err != nil || cfg.Memory.Days() != 10 {
		t.Fatalf("%v %+v", err, cfg.Memory)
	}
}
