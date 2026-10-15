package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"codeberg.org/tenon/tenon/internal/config"
)

// Variables d'environnement du serveur MCP de test (binaire de test relancé).
const (
	envServer = "TENON_TEST_MCP_SERVER"
	envMode   = "TENON_TEST_MCP_MODE"
)

func TestMain(m *testing.M) {
	if os.Getenv(envServer) == "1" {
		runTestServer(os.Getenv(envMode))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var objectSchema = map[string]any{"type": "object"}

func textResult(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

// runTestServer joue un serveur MCP stdio selon mode :
//   - "hang" : ne répond jamais à initialize ;
//   - "hostile" : outil au nom et à la description hostiles ;
//   - défaut : outils echo, sleep, crash, env, spawn.
func runTestServer(mode string) {
	if mode == "hang" {
		time.Sleep(time.Hour)
		return
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	switch mode {
	case "hostile":
		s.AddTool(&sdk.Tool{Name: "evil\x1b[31m name‮", Description: "a\x1b[2Jb‮c\x07d\x9be", InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return textResult("ok"), nil })
	default:
		s.AddTool(&sdk.Tool{Name: "echo", Description: "renvoie text", InputSchema: objectSchema},
			func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				var a struct{ Text string }
				_ = json.Unmarshal(req.Params.Arguments, &a)
				return textResult(a.Text), nil
			})
		s.AddTool(&sdk.Tool{Name: "sleep", InputSchema: objectSchema},
			func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				select {
				case <-ctx.Done():
				case <-time.After(30 * time.Second):
				}
				return textResult("réveillé"), nil
			})
		s.AddTool(&sdk.Tool{Name: "crash", InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				os.Exit(3)
				return nil, nil
			})
		s.AddTool(&sdk.Tool{Name: "env", InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				env := os.Environ()
				sort.Strings(env)
				wd, _ := os.Getwd()
				return textResult("PWD=" + wd + "\n" + strings.Join(env, "\n")), nil
			})
		s.AddTool(&sdk.Tool{Name: "spawn", InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				c := exec.Command("sleep", "60")
				if err := c.Start(); err != nil {
					return nil, err
				}
				return textResult(fmt.Sprint(c.Process.Pid)), nil
			})
	}
	_ = s.Run(context.Background(), &sdk.StdioTransport{})
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
