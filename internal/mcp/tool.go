//go:build unix

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

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
		return nil, errors.New("invalid arguments: JSON expected")
	}
	return buf.Bytes(), nil
}

func (t *mcpTool) Target(args json.RawMessage) (tools.Target, error) {
	c, err := compactArgs(args)
	if err != nil {
		return tools.Target{}, err
	}
	// Arguments complets : l'utilisateur voit tout ce qu'il valide et la clé
	// « pour la session » de l'engine porte sur l'appel exact.
	cmd := "mcp " + t.srv.decl.ID + "." + cleanText(t.tool) + " " + string(c)
	return tools.Target{Command: cmd, ForceAsk: t.needsConsent()}, nil
}

// needsConsent indique qu'un appel exige une validation humaine : serveur
// hors UE (ou de juridiction inconnue) sans consentement d'avance.
func (t *mcpTool) needsConsent() bool {
	if t.opts.AllowNonEU {
		return false
	}
	switch llm.Jurisdiction(t.srv.decl.Jurisdiction) {
	case llm.EU, llm.Local:
		return false
	}
	return true
}

func (t *mcpTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	id := t.srv.decl.ID
	if t.srv.down.Load() {
		return tools.Result{IsError: true, Content: "MCP server " + id + " unavailable"}, nil
	}
	if t.opts.Consent != nil && !t.opts.Consent(id) {
		return tools.Result{IsError: true, Denied: "policy", Content: "non-EU MCP server without consent"}, nil
	}
	c, err := compactArgs(args)
	var obj map[string]json.RawMessage
	if err == nil && (json.Unmarshal(c, &obj) != nil || obj == nil) {
		err = errors.New("invalid arguments: JSON object expected")
	}
	if err != nil {
		return tools.Result{IsError: true, Content: err.Error()}, nil
	}

	tctx, cancel := context.WithTimeout(ctx, t.opts.CallTimeout)
	defer cancel()
	out, done, finished := t.call(tctx, c)
	timeout := tools.Result{IsError: true, Content: fmt.Sprintf("[timed out after %s]", t.opts.CallTimeout)}
	if !finished {
		// L'appel ne rend pas la main malgré l'expiration du contexte (écriture
		// bloquée sur le pipe d'un serveur stdio qui ne lit plus) : le serveur
		// est hors service et son groupe de processus tué, ce qui débloque l'écriture.
		if t.srv.proc != nil {
			if !t.srv.closing.Load() && t.srv.markDown() {
				timeout.ServerDown = id
			}
			t.srv.down.Store(true) // aussi pendant Close : aucun nouvel appel
			t.srv.proc.signal(syscall.SIGKILL)
			select {
			case <-done:
			case <-time.After(killWait):
			}
		}
		if ctx.Err() != nil {
			return tools.Result{}, ctx.Err()
		}
		return timeout, nil
	}
	res, err := out.res, out.err
	if err != nil {
		if ctx.Err() != nil {
			return tools.Result{}, ctx.Err()
		}
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return timeout, nil
		}
		if t.srv.closing.Load() {
			// Fermeture volontaire (Manager.Close) : pas de signalement hors service.
			return tools.Result{IsError: true, Content: "MCP server " + id + " closed"}, nil
		}
		var werr *jsonrpc.Error
		switch {
		case isNetworkErr(err):
		case hasWireCode(err, codeRejected):
			// Refus transitoire du transport (statut 429/5xx, en-têtes…) : erreur, serveur en service.
			return tools.Result{IsError: true, Content: "MCP server " + id + ": " + errText(err)}, nil
		case errors.As(err, &werr):
			return tools.Result{IsError: true, Content: "MCP server " + id + " error: " + cleanText(werr.Message)}, nil
		}
		r := tools.Result{IsError: true, Content: "MCP server " + id + " unavailable: " + errText(err)}
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

// callGrace est le délai laissé à un appel pour rendre la main après
// l'expiration de son contexte ; killWait borne l'attente de sa fin une fois
// le serveur stdio tué.
const (
	callGrace = time.Second
	killWait  = 5 * time.Second
)

// callOut est l'issue d'un appel CallTool.
type callOut struct {
	res *sdk.CallToolResult
	err error
}

// call exécute CallTool dans une goroutine et rend la main au plus tard
// callGrace après l'expiration de ctx. Si l'appel est toujours en cours, ok
// est faux et done recevra son issue.
func (t *mcpTool) call(ctx context.Context, args []byte) (out callOut, done <-chan callOut, ok bool) {
	ch := make(chan callOut, 1)
	go func() {
		res, err := t.srv.session.CallTool(ctx, &sdk.CallToolParams{Name: t.tool, Arguments: json.RawMessage(args)})
		ch <- callOut{res, err}
	}()
	select {
	case o := <-ch:
		return o, ch, true
	case <-ctx.Done():
	}
	select {
	case o := <-ch:
		return o, ch, true
	case <-time.After(callGrace):
		return callOut{}, ch, false
	}
}

// codeRejected est le code JSON-RPC par lequel le SDK signale une requête
// rejetée par le transport (jsonrpc2.ErrRejected), qu'elle ait ou non atteint le serveur.
const codeRejected = -32005

// hasWireCode indique si une erreur JSON-RPC de code code figure dans la chaîne de err.
func hasWireCode(err error, code int64) bool {
	return walkErr(err, func(e error) bool {
		w, ok := e.(*jsonrpc.Error)
		return ok && w.Code == code
	})
}

// isNetworkErr indique si err provient d'une panne réseau ou d'un flux coupé
// (connexion refusée ou réinitialisée, fin de flux inattendue).
func isNetworkErr(err error) bool {
	return walkErr(err, func(e error) bool {
		switch e.(type) {
		case *net.OpError, *url.Error:
			return true
		}
		return e == io.EOF || e == io.ErrUnexpectedEOF || e == syscall.ECONNRESET ||
			e == syscall.ECONNREFUSED || e == syscall.EPIPE
	})
}

// walkErr parcourt l'arbre des erreurs enveloppées de err.
func walkErr(err error, match func(error) bool) bool {
	if err == nil {
		return false
	}
	if match(err) {
		return true
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return walkErr(u.Unwrap(), match)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if walkErr(e, match) {
				return true
			}
		}
	}
	return false
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
			parts = append(parts, "[resource "+cleanText(v.URI)+"]")
		case *sdk.EmbeddedResource:
			uri := ""
			if v.Resource != nil {
				uri = v.Resource.URI
			}
			parts = append(parts, "[resource "+cleanText(uri)+"]")
		default:
			parts = append(parts, "[non-text content]")
		}
	}
	if len(parts) == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			return string(b)
		}
	}
	return strings.Join(parts, "\n")
}
