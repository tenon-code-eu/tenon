package acp

import (
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func TestEditorMCPStdio(t *testing.T) {
	out, notes := EditorMCP([]MCPServer{{
		Name: "fs", Command: "/bin/mcp-fs", Args: []string{"--ro", "/p"},
		Env: []EnvVariable{{Name: "TOKEN", Value: "s3cret"}},
	}}, nil, i18n.EN)
	if len(out) != 1 {
		t.Fatalf("out %+v notes %v", out, notes)
	}
	s := out[0]
	if s.ID != "fs" || s.Transport != "stdio" || s.Jurisdiction != "local" || s.Risk != "" ||
		strings.Join(s.Command, " ") != "/bin/mcp-fs --ro /p" {
		t.Fatalf("%+v", s)
	}
	for _, n := range notes {
		if strings.Contains(n, "s3cret") {
			t.Fatalf("valeur loguée : %q", n)
		}
	}
}

func TestEditorMCPHTTP(t *testing.T) {
	out, notes := EditorMCP([]MCPServer{
		{Name: "remote", Type: "http", URL: "https://mcp.example.com/x", Headers: []HTTPHeader{{Name: "Authorization", Value: "Bearer zzz"}}},
		{Name: "local1", Type: "http", URL: "http://localhost:8080/mcp"},
		{Name: "local2", URL: "http://127.0.0.1:9/mcp"},
		{Name: "local3", Type: "http", URL: "http://[::1]:9/mcp"},
		{Name: "evil1", Type: "http", URL: "https://localhost.evil.com/mcp"},
		{Name: "evil2", Type: "http", URL: "https://127.0.0.1.nip.io/mcp"},
	}, nil, i18n.EN)
	if len(out) != 6 {
		t.Fatalf("out %+v notes %v", out, notes)
	}
	want := []string{"non-eu", "local", "local", "local", "non-eu", "non-eu"}
	for i, s := range out {
		if s.Transport != "http" || s.Jurisdiction != want[i] || s.URL == "" || s.Risk != "" {
			t.Fatalf("%d: %+v", i, s)
		}
	}
	for _, n := range notes {
		if strings.Contains(n, "zzz") {
			t.Fatalf("valeur loguée : %q", n)
		}
	}
}

func TestEditorMCPSkipsSSEAndInvalid(t *testing.T) {
	out, notes := EditorMCP([]MCPServer{
		{Name: "old", Type: "sse", URL: "https://x/sse"},
		{Name: "empty"},
		{Name: "BAD NAME", Command: "/bin/x"},
	}, nil, i18n.EN)
	if len(out) != 0 || len(notes) != 3 {
		t.Fatalf("out %+v notes %v", out, notes)
	}
	if !strings.Contains(notes[0], "old") || !strings.Contains(notes[1], "empty") || !strings.Contains(notes[2], "BAD NAME") {
		t.Fatalf("notes %v", notes)
	}
}

func TestEditorMCPClash(t *testing.T) {
	existing := []config.MCPServer{{ID: "fs", Transport: "stdio", Command: []string{"/cfg"}}}
	out, notes := EditorMCP([]MCPServer{
		{Name: "fs", Command: "/editor"},
		{Name: "dup", Command: "/a"},
		{Name: "dup", Command: "/b"},
	}, existing, i18n.FR)
	if len(out) != 1 || out[0].ID != "dup" || out[0].Command[0] != "/a" {
		t.Fatalf("out %+v", out)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "fs") || !strings.Contains(notes[1], "dup") {
		t.Fatalf("notes %v", notes)
	}
}

func TestEditorMCPRejectsBadURLs(t *testing.T) {
	for _, u := range []string{
		"http://mcp.example.com/x", "ftp://localhost/x", "file://localhost/x",
		"https://u:p@host/", "https://host/x?token=abc", "https://host/x#f", "https://",
	} {
		out, notes := EditorMCP([]MCPServer{{Name: "s", Type: "http", URL: u}}, nil, i18n.EN)
		if len(out) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "invalid definition") {
			t.Errorf("%s: out %+v notes %v", u, out, notes)
		}
		if strings.Contains(strings.Join(notes, ""), "abc") {
			t.Errorf("%s: valeur loguée", u)
		}
	}
}

func TestEditorMCPExplicitStdio(t *testing.T) {
	out, _ := EditorMCP([]MCPServer{{Name: "s", Type: "stdio", Command: "/bin/x"}}, nil, i18n.EN)
	if len(out) != 1 || out[0].Transport != "stdio" {
		t.Fatalf("%+v", out)
	}
}
