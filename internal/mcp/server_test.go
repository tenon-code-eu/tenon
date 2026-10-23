package mcp

import (
	"context"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/mcp/mcptest"
	"github.com/tenon-code-eu/tenon/internal/sandbox"
)

// Variables d'environnement du serveur MCP de test (binaire de test relancé).
const (
	envServer = mcptest.EnvServer
	envMode   = mcptest.EnvMode
)

// TestMain : relancé avec __sandbox, le binaire de test joue l'assistant
// d'isolation (Linux) ; avec TENON_TEST_MCP_SERVER=1, le serveur stdio.
func TestMain(m *testing.M) {
	sandbox.MaybeMain()
	mcptest.Main()
	os.Exit(m.Run())
}

var objectSchema = map[string]any{"type": "object"}

func textResult(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

// stdioServer renvoie la déclaration d'un serveur stdio de test et la fonction Getenv associée.
func stdioServer(t *testing.T, id, mode string, extra map[string]string) (config.MCPServer, func(string) string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"PATH":    os.Getenv("PATH"),
		"HOME":    os.Getenv("HOME"),
		envServer: "1",
		envMode:   mode,
	}
	for k, v := range extra {
		env[k] = v
	}
	srv := config.MCPServer{
		ID:           id,
		Transport:    "stdio",
		Command:      []string{exe},
		EnvFrom:      []string{envServer, envMode},
		Jurisdiction: "local",
	}
	return srv, func(k string) string { return env[k] }
}

// memServer démarre un serveur MCP en mémoire et renvoie l'option de transport de test.
func memServer(t *testing.T, setup func(s *sdk.Server)) func(config.MCPServer) (sdk.Transport, error) {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "mem", Version: "1"}, nil)
	setup(s)
	return func(config.MCPServer) (sdk.Transport, error) {
		ct, st := sdk.NewInMemoryTransports()
		ss, err := s.Connect(context.Background(), st, nil)
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { _ = ss.Close() })
		return ct, nil
	}
}

func httpDecl(id string) config.MCPServer {
	return config.MCPServer{ID: id, Transport: "http", URL: "http://127.0.0.1:1/mcp", Jurisdiction: "eu"}
}
