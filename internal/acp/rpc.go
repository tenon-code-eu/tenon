// Package acp implémente l'Agent Client Protocol (JSON-RPC 2.0, un objet JSON
// par ligne) pour que tenon soit lancé par un éditeur via stdio.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Codes d'erreur JSON-RPC et ACP.
const (
	CodeParse          = -32700
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeTenon          = -32000
	CodeUnknownSession = -32002
)

// maxLine est la taille maximale d'une ligne entrante (16 MiB).
const maxLine = 16 << 20

// Error est une erreur JSON-RPC.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// Handler traite les requêtes et notifications client → agent. Pour une
// notification, le résultat est ignoré et rien n'est écrit.
type Handler interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (result any, err error)
}

// ErrClosed est renvoyée par Call quand la connexion ne sert plus.
var ErrClosed = errors.New("acp: connection closed")

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// out est un message sortant ; l'ordre des champs est stable.
type out struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    *Error
}

// Conn est une connexion JSON-RPC 2.0 par lignes. Les écritures sont
// sérialisées ; chaque requête entrante est traitée dans sa goroutine.
type Conn struct {
	r io.Reader
	h Handler

	wmu sync.Mutex
	w   io.Writer

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan response
	closed  bool
}

// NewConn crée une connexion ; rien n'est lu avant Serve.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	return &Conn{r: r, w: w, h: h, pending: map[int64]chan response{}}
}

// Serve lit les lignes jusqu'à EOF (renvoie nil), l'annulation de ctx ou une
// erreur de lecture. À son retour, les Call en attente échouent.
func (c *Conn) Serve(ctx context.Context) error {
	hctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		wg.Wait()
	}()

	lines := make(chan []byte)
	scanErr := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		sc := bufio.NewScanner(c.r)
		sc.Buffer(make([]byte, 0, 64<<10), maxLine)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-stop:
				return
			}
		}
		scanErr <- sc.Err()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-scanErr:
			return err
		case line := <-lines:
			c.dispatch(hctx, &wg, line)
		}
	}
}

func (c *Conn) dispatch(ctx context.Context, wg *sync.WaitGroup, line []byte) {
	if len(trimSpace(line)) == 0 {
		return
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		c.write(out{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &Error{Code: CodeParse, Message: "parse error: " + err.Error()}})
		return
	}
	if m.Method == "" {
		c.resolve(m)
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := c.h.Handle(ctx, m.Method, m.Params)
		if len(m.ID) == 0 {
			return
		}
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				e = &Error{Code: CodeInternal, Message: err.Error()}
			}
			c.write(out{JSONRPC: "2.0", ID: m.ID, Error: e})
			return
		}
		raw, merr := json.Marshal(res)
		if merr != nil {
			c.write(out{JSONRPC: "2.0", ID: m.ID, Error: &Error{Code: CodeInternal, Message: merr.Error()}})
			return
		}
		c.write(out{JSONRPC: "2.0", ID: m.ID, Result: raw})
	}()
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	return b
}

func (c *Conn) resolve(m message) {
	var id int64
	if err := json.Unmarshal(m.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		ch <- response{result: m.Result, err: m.Error} // buffered (1)
	}
}

// write sérialise v en une seule ligne ; json.Marshal échappe \n, U+2028 et
// remplace l'UTF-8 invalide, donc une ligne = un objet.
func (c *Conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(b)
	return err
}

// Notify envoie une notification agent → client.
func (c *Conn) Notify(method string, params any) error {
	return c.write(out{JSONRPC: "2.0", Method: method, Params: params})
}

// Call envoie une requête agent → client et attend la réponse ou ctx.
// result peut être nil.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	drop := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	if err := c.write(out{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: params}); err != nil {
		drop()
		return err
	}
	select {
	case <-ctx.Done():
		drop()
		return ctx.Err()
	case r, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if r.err != nil {
			return r.err
		}
		if result != nil && len(r.result) > 0 {
			return json.Unmarshal(r.result, result)
		}
		return nil
	}
}
