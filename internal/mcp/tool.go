//go:build unix

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"codeberg.org/tenon/tenon/internal/tools"
)

// maxTargetArgs est la longueur maximale des arguments affichés dans Target.
const maxTargetArgs = 2000

// mcpTool expose un outil d'un serveur MCP comme un tools.Tool.
type mcpTool struct {
	srv    *server
	name   string // nom exposé au modèle
	tool   string // nom côté serveur
	desc   string
	schema json.RawMessage
	risk   tools.Risk
	opts   Options
}

func (t *mcpTool) Name() string            { return t.name }
func (t *mcpTool) Description() string     { return t.desc }
func (t *mcpTool) Schema() json.RawMessage { return t.schema }
func (t *mcpTool) Risk() tools.Risk        { return t.risk }

// compactArgs renvoie les arguments en JSON compact ({} si vides).
func compactArgs(args json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, args); err != nil {
		return nil, errors.New("arguments invalides : JSON attendu")
	}
	return buf.Bytes(), nil
}

func (t *mcpTool) Target(args json.RawMessage) (tools.Target, error) {
	c, err := compactArgs(args)
	if err != nil {
		return tools.Target{}, err
	}
	cmd := "mcp " + t.srv.decl.ID + "." + cleanText(t.tool) + " " + tools.Truncate(string(c), maxTargetArgs)
	return tools.Target{Command: cmd}, nil
}

func (t *mcpTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	id := t.srv.decl.ID
	if t.srv.down.Load() {
		return tools.Result{IsError: true, Content: "serveur MCP " + id + " indisponible"}, nil
	}
	if t.opts.Consent != nil && !t.opts.Consent(id) {
		return tools.Result{IsError: true, Denied: "policy", Content: "serveur MCP hors UE sans consentement"}, nil
	}
	c, err := compactArgs(args)
	var obj map[string]json.RawMessage
	if err == nil && (json.Unmarshal(c, &obj) != nil || obj == nil) {
		err = errors.New("arguments invalides : objet JSON attendu")
	}
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}

	tctx, cancel := context.WithTimeout(ctx, t.opts.CallTimeout)
	defer cancel()
	res, err := t.srv.session.CallTool(tctx, &sdk.CallToolParams{Name: t.tool, Arguments: json.RawMessage(c)})
	if err != nil {
		if ctx.Err() != nil {
			return tools.Result{}, ctx.Err()
		}
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return tools.Result{IsError: true, Content: fmt.Sprintf("[délai dépassé après %s]", t.opts.CallTimeout)}, nil
		}
		var werr *jsonrpc.Error
		if errors.As(err, &werr) {
			return tools.Result{IsError: true, Content: "erreur du serveur MCP " + id + " : " + cleanText(werr.Message)}, nil
		}
		r := tools.Result{IsError: true, Content: "serveur MCP " + id + " indisponible : " + cleanText(err.Error())}
		if t.srv.markDown() {
			r.ServerDown = id
		}
		return r, nil
	}
	return tools.Result{
		Content: tools.Truncate(render(res), t.opts.OutputLimit),
		IsError: res.IsError,
	}, nil
}

// render concatène les parties texte et résume les autres.
func render(res *sdk.CallToolResult) string {
	parts := make([]string, 0, len(res.Content))
	for _, c := range res.Content {
		switch v := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, v.Text)
		case *sdk.ImageContent:
			parts = append(parts, "[image "+cleanText(v.MIMEType)+"]")
		case *sdk.AudioContent:
			parts = append(parts, "[audio "+cleanText(v.MIMEType)+"]")
		case *sdk.ResourceLink:
			parts = append(parts, "[ressource "+cleanText(v.URI)+"]")
		case *sdk.EmbeddedResource:
			uri := ""
			if v.Resource != nil {
				uri = v.Resource.URI
			}
			parts = append(parts, "[ressource "+cleanText(uri)+"]")
		default:
			parts = append(parts, "[contenu non textuel]")
		}
	}
	if len(parts) == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			return string(b)
		}
	}
	return strings.Join(parts, "\n")
}
