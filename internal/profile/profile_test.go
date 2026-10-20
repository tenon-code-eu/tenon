package profile

import (
	"errors"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/llm"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Name{"": None, "eu-only": EUOnly, "strict-local": StrictLocal} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"EU-ONLY", "x"} {
		_, err := Parse(in)
		want := `unknown profile "` + in + `" (eu-only|strict-local)`
		if err == nil || err.Error() != want {
			t.Errorf("Parse(%q) err = %v, want %q", in, err, want)
		}
	}
}

func TestStricter(t *testing.T) {
	names := []Name{None, EUOnly, StrictLocal}
	want := [3][3]Name{
		{None, EUOnly, StrictLocal},
		{EUOnly, EUOnly, StrictLocal},
		{StrictLocal, StrictLocal, StrictLocal},
	}
	for i, a := range names {
		for j, b := range names {
			if got := Stricter(a, b); got != want[i][j] {
				t.Errorf("Stricter(%q,%q) = %q, want %q", a, b, got, want[i][j])
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
