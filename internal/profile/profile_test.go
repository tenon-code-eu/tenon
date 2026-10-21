package profile

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/llm"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Name{"": None, "eu-only": EUOnly, "secnumcloud": SecNumCloud, "strict-local": StrictLocal} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"EU-ONLY", "x"} {
		_, err := Parse(in)
		want := `unknown profile "` + in + `" (eu-only|secnumcloud|strict-local)`
		if err == nil || err.Error() != want {
			t.Errorf("Parse(%q) err = %v, want %q", in, err, want)
		}
	}
}

func TestStricter(t *testing.T) {
	names := []Name{None, EUOnly, SecNumCloud, StrictLocal}
	for i, a := range names {
		for j, b := range names {
			want := names[max(i, j)]
			if got := Stricter(a, b); got != want {
				t.Errorf("Stricter(%q,%q) = %q, want %q", a, b, got, want)
			}
		}
	}
}

func TestCheckProvider(t *testing.T) {
	p := func(id string, j llm.Jurisdiction, url string) llm.ProviderInfo {
		return llm.ProviderInfo{ID: id, BaseURL: url, Jurisdiction: j}
	}
	tests := []struct {
		name string
		n    Name
		p    llm.ProviderInfo
		want string // "" = accepté
	}{
		{"none non-eu", None, p("openai", llm.NonEU, "https://api.openai.com/v1"), ""},
		{"none empty", None, p("x", "", ""), ""},
		{"eu-only eu", EUOnly, p("mistral", llm.EU, "https://api.mistral.ai"), ""},
		{"eu-only local", EUOnly, p("ollama", llm.Local, "http://gpu.lan"), ""},
		{"eu-only non-eu", EUOnly, p("openai", llm.NonEU, ""), `profile eu-only: provider "openai" (non-eu) not allowed`},
		{"eu-only empty", EUOnly, p("x", "", ""), `profile eu-only: provider "x" (unknown) not allowed`},
		{"strict localhost", StrictLocal, p("ollama", llm.Local, "http://localhost:11434/v1"), ""},
		{"strict ::1", StrictLocal, p("v", llm.Local, "http://[::1]:8000/v1"), ""},
		{"strict private", StrictLocal, p("v", llm.Local, "http://192.168.0.5:8000/v1"), ""},
		{"strict lan", StrictLocal, p("vllm", llm.Local, "http://gpu.lan:8000/v1"), `profile strict-local: provider "vllm" url host "gpu.lan" is not a local address`},
		{"strict eu", StrictLocal, p("mistral", llm.EU, "http://localhost"), `profile strict-local: provider "mistral" (eu) not allowed`},
		{"strict empty url", StrictLocal, p("v", llm.Local, ""), `profile strict-local: provider "v" url host "" is not a local address`},
		{"strict bad url", StrictLocal, p("v", llm.Local, "::bad"), `profile strict-local: provider "v" url host "" is not a local address`},
		{"strict no scheme", StrictLocal, p("v", llm.Local, "localhost:11434"), `profile strict-local: provider "v" url host "" is not a local address`},
	}
	for _, tt := range tests {
		err := CheckProvider(tt.n, tt.p)
		if tt.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected %v", tt.name, err)
			}
			continue
		}
		if err == nil || err.Error() != tt.want || !errors.Is(err, ErrProfile) {
			t.Errorf("%s: err = %v, want %q wrapping ErrProfile", tt.name, err, tt.want)
		}
	}
}

func TestCheckMCP(t *testing.T) {
	tests := []struct {
		name string
		n    Name
		s    MCPTarget
		want string
	}{
		{"strict stdio local", StrictLocal, MCPTarget{ID: "a", Transport: "stdio", Jurisdiction: "local"}, ""},
		{"strict stdio eu", StrictLocal, MCPTarget{ID: "a", Transport: "stdio", Jurisdiction: "eu"}, `profile strict-local: MCP server "a" (eu) skipped`},
		{"strict http localhost", StrictLocal, MCPTarget{ID: "a", Transport: "http", URL: "http://localhost:9/mcp", Jurisdiction: "local"}, ""},
		{"strict http remote", StrictLocal, MCPTarget{ID: "a", Transport: "http", URL: "https://example.com/mcp", Jurisdiction: "local"}, `profile strict-local: MCP server "a" (url host "example.com" is not a local address) skipped`},
		{"eu-only non-eu", EUOnly, MCPTarget{ID: "b", Transport: "stdio", Jurisdiction: "non-eu"}, `profile eu-only: MCP server "b" (non-eu) skipped`},
		{"eu-only eu", EUOnly, MCPTarget{ID: "b", Transport: "http", URL: "https://x.eu", Jurisdiction: "eu"}, ""},
		{"none", None, MCPTarget{ID: "b", Jurisdiction: "non-eu"}, ""},
	}
	for _, tt := range tests {
		err := CheckMCP(tt.n, tt.s)
		if tt.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected %v", tt.name, err)
			}
			continue
		}
		if err == nil || err.Error() != tt.want || !errors.Is(err, ErrProfile) {
			t.Errorf("%s: err = %v, want %q wrapping ErrProfile", tt.name, err, tt.want)
		}
	}
}

func TestIsLocalHost(t *testing.T) {
	for _, h := range []string{"localhost", "LOCALHOST", "127.0.0.1", "127.255.0.1", "::1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.0.5", "fd00::1", "::1%lo0"} {
		if !IsLocalHost(h) {
			t.Errorf("IsLocalHost(%q) = false", h)
		}
	}
	for _, h := range []string{"fe80::1", "172.32.0.1", "8.8.8.8", "example.com", "gpu.lan", "localhost.", "127.1", "",
		"::ffff:127.0.0.1", "::ffff:8.8.8.8", "0.0.0.0", "::"} {
		if IsLocalHost(h) {
			t.Errorf("IsLocalHost(%q) = true", h)
		}
	}
}

func TestCheckProviderSecNumCloud(t *testing.T) {
	q := []string{QualSecNumCloud}
	tests := []struct {
		name string
		p    llm.ProviderInfo
		want string
	}{
		{"local localhost", llm.ProviderInfo{ID: "o", BaseURL: "http://localhost:11434/v1", Jurisdiction: llm.Local}, ""},
		{"local remote host", llm.ProviderInfo{ID: "o", BaseURL: "http://gpu.lan:8000/v1", Jurisdiction: llm.Local}, `profile secnumcloud: provider "o" url host "gpu.lan" is not a local address`},
		{"eu qualified", llm.ProviderInfo{ID: "m", BaseURL: "https://api.m.eu", Jurisdiction: llm.EU, Qualifications: q}, ""},
		{"eu unqualified", llm.ProviderInfo{ID: "m", BaseURL: "https://api.m.eu", Jurisdiction: llm.EU}, `profile secnumcloud: provider "m" is not declared secnumcloud-qualified`},
		{"non-eu qualified", llm.ProviderInfo{ID: "x", Jurisdiction: llm.NonEU, Qualifications: q}, `profile secnumcloud: provider "x" (non-eu) not allowed`},
		{"empty jurisdiction", llm.ProviderInfo{ID: "x", Qualifications: q}, `profile secnumcloud: provider "x" (unknown) not allowed`},
	}
	for _, tc := range tests {
		err := CheckProvider(SecNumCloud, tc.p)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || err.Error() != tc.want || !errors.Is(err, ErrProfile) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestCheckMCPSecNumCloud(t *testing.T) {
	q := []string{QualSecNumCloud}
	tests := []struct {
		name string
		s    MCPTarget
		want string
	}{
		{"stdio local", MCPTarget{ID: "a", Transport: "stdio", Jurisdiction: "local"}, ""},
		{"stdio eu", MCPTarget{ID: "a", Transport: "stdio", Jurisdiction: "eu"}, `profile secnumcloud: MCP server "a" (eu) skipped`},
		{"stdio eu qualified", MCPTarget{ID: "a", Transport: "stdio", Jurisdiction: "eu", Qualifications: q}, `profile secnumcloud: MCP server "a" (eu) skipped`},
		{"http local", MCPTarget{ID: "a", Transport: "http", URL: "http://localhost:9/mcp", Jurisdiction: "local"}, ""},
		{"http eu qualified", MCPTarget{ID: "a", Transport: "http", URL: "https://x.eu/mcp", Jurisdiction: "eu", Qualifications: q}, ""},
		{"http eu unqualified", MCPTarget{ID: "a", Transport: "http", URL: "https://x.eu/mcp", Jurisdiction: "eu"}, `profile secnumcloud: MCP server "a" (not declared secnumcloud-qualified) skipped`},
		{"http local remote host", MCPTarget{ID: "a", Transport: "http", URL: "http://gpu.lan/mcp", Jurisdiction: "local"}, `profile secnumcloud: MCP server "a" (url host "gpu.lan" is not a local address) skipped`},
		{"non-eu", MCPTarget{ID: "a", Transport: "http", Jurisdiction: "non-eu", Qualifications: q}, `profile secnumcloud: MCP server "a" (non-eu) skipped`},
	}
	for _, tc := range tests {
		err := CheckMCP(SecNumCloud, tc.s)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestQualified(t *testing.T) {
	if !Qualified([]string{"x", QualSecNumCloud}) || Qualified(nil) || Qualified([]string{"SecNumCloud"}) {
		t.Error("Qualified")
	}
}

func TestProviderInfoQualificationsOmitted(t *testing.T) {
	b, _ := json.Marshal(llm.ProviderInfo{ID: "a"})
	if strings.Contains(string(b), "qualifications") {
		t.Errorf("json = %s", b)
	}
}
