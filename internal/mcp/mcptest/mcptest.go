//go:build unix

// Package mcptest fournit le serveur MCP stdio des tests : le binaire de test
// relancé avec EnvServer=1 joue un serveur selon EnvMode. Réservé aux tests :
// seul du code _test.go l'importe, il n'est jamais lié au binaire tenon.
package mcptest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Variables d'environnement du serveur MCP de test (binaire de test relancé).
const (
	EnvServer = "TENON_TEST_MCP_SERVER"
	EnvMode   = "TENON_TEST_MCP_MODE"
	// EnvLog : fichier où chaque appel d'outil reçu ajoute une ligne (son nom).
	EnvLog = "TENON_TEST_MCP_LOG"
)

// Main joue le serveur de test et quitte si EnvServer vaut "1" ; sinon ne fait
// rien. À appeler en tête de TestMain.
func Main() {
	if os.Getenv(EnvServer) == "1" {
		Serve(os.Getenv(EnvMode))
		os.Exit(0)
	}
}

var objectSchema = map[string]any{"type": "object"}

func textResult(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

// Serve joue un serveur MCP stdio selon mode :
//   - "hang" : ne répond jamais à initialize ;
//   - "deaf" : répond à initialize et tools/list (outil echo), puis cesse
//     à jamais de lire son entrée standard ;
//   - "hostile" : outil au nom et à la description hostiles ;
//   - "badschema" : outils echo et bad (schéma invalide) ;
//   - "stubborn" : outils par défaut, mais ignore SIGTERM et survit à la
//     fin de son entrée standard (seul SIGKILL l'arrête) ;
//   - défaut : outils echo, sleep, crash, env, spawn.
//
// Si EnvLog est définie, le pid du serveur (« pid N ») puis chaque appel
// reçu (nom de l'outil) y sont consignés.
func Serve(mode string) {
	if mode == "hang" {
		time.Sleep(time.Hour)
		return
	}
	if mode == "deaf" {
		serveDeaf()
		return
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	if log := os.Getenv(EnvLog); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "pid %d\n", os.Getpid())
			_ = f.Close()
		}
		s.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
				if p, ok := req.GetParams().(*sdk.CallToolParamsRaw); ok && method == "tools/call" {
					if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
						fmt.Fprintln(f, p.Name)
						_ = f.Close()
					}
				}
				return next(ctx, method, req)
			}
		})
	}
	echo := func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct{ Text string }
		_ = json.Unmarshal(req.Params.Arguments, &a)
		return textResult(a.Text), nil
	}
	switch mode {
	case "badschema":
		s.AddTool(&sdk.Tool{Name: "echo", Description: "renvoie text", InputSchema: objectSchema}, echo)
		s.AddTool(&sdk.Tool{Name: "bad", InputSchema: map[string]any{"type": "object", "properties": "x"}}, echo)
	case "hostile":
		s.AddTool(&sdk.Tool{Name: "evil\x1b[31m name‮", Description: "a\x1b[2Jb‮c\x07d\x9be", InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return textResult("ok"), nil })
	default:
		s.AddTool(&sdk.Tool{Name: "echo", Description: "renvoie text", InputSchema: objectSchema}, echo)
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
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	_ = s.Run(context.Background(), &sdk.StdioTransport{})
	if mode == "stubborn" {
		time.Sleep(time.Hour)
	}
}

// serveDeaf mène la poignée de main à la main (JSON-RPC ligne à ligne), sert
// tools/list puis ne lit plus jamais l'entrée standard : une écriture de
// plus d'un tampon de pipe du client y reste bloquée.
func serveDeaf() {
	if log := os.Getenv(EnvLog); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "pid %d\n", os.Getpid())
			_ = f.Close()
		}
	}
	in := bufio.NewReader(os.Stdin)
	reply := func(id json.RawMessage, result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		_, _ = os.Stdout.Write(append(b, '\n'))
	}
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			reply(msg.ID, map[string]any{
				"protocolVersion": msg.Params.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "deaf", "version": "1"},
			})
		case "tools/list":
			reply(msg.ID, map[string]any{"tools": []any{map[string]any{
				"name": "echo", "inputSchema": objectSchema,
			}}})
			time.Sleep(time.Hour) // plus aucune lecture
			return
		default:
			if len(msg.ID) > 0 { // requête inconnue (server/discover…) : méthode absente
				b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID,
					"error": map[string]any{"code": -32601, "message": "method not found"}})
				_, _ = os.Stdout.Write(append(b, '\n'))
			}
		}
	}
}
