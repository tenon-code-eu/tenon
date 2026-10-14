package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FS renvoie les outils fichiers et recherche ancrés sur root.
func FS(root string, limit int, opts ...Option) []Tool {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	b := base{root: root, limit: limit, filter: o.readFilter}
	return []Tool{
		&readFile{b}, &writeFile{b}, &editFile{b},
		&globTool{b}, &grepTool{b},
	}
}

type options struct {
	readFilter func(tool, absPath string) bool
}

// Option configure FS.
type Option func(*options)

// WithReadFilter fait ignorer à grep et glob les fichiers pour lesquels f
// renvoie false.
func WithReadFilter(f func(tool, absPath string) bool) Option {
	return func(o *options) { o.readFilter = f }
}

type base struct {
	root   string
	limit  int
	filter func(tool, absPath string) bool
}

func (b base) allowed(tool, abs string) bool { return b.filter == nil || b.filter(tool, abs) }

func (b base) resolve(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(b.root, p)
}

func (b base) out(content string) Result { return Result{Content: Truncate(content, b.limit)} }

func fail(format string, a ...any) Result {
	return Result{IsError: true, Content: fmt.Sprintf(format, a...)}
}

func hashOf(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

func isBinary(data []byte) bool {
	if len(data) > 8000 {
		data = data[:8000]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// decode analyse les arguments JSON ; une erreur signale des arguments invalides.
func decode(args json.RawMessage, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("arguments invalides : %w", err)
	}
	return nil
}

func schema(props string, required ...string) json.RawMessage {
	req, _ := json.Marshal(required)
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(req) + `}`)
}

// pathTool factorise Target pour les outils à chemin unique.
type pathArgs struct {
	Path string `json:"path"`
}

func (b base) pathTarget(args json.RawMessage) (Target, error) {
	var a pathArgs
	if err := decode(args, &a); err != nil {
		return Target{}, err
	}
	if a.Path == "" {
		return Target{}, errors.New("arguments invalides : path requis")
	}
	return Target{Paths: []string{b.resolve(a.Path)}}, nil
}

// --- read_file ---

type readFile struct{ base }

func (*readFile) Name() string { return "read_file" }
func (*readFile) Description() string {
	return "Lit un fichier texte et renvoie ses lignes numérotées."
}
func (*readFile) Risk() Risk { return Read }
func (*readFile) Schema() json.RawMessage {
	return schema(`"path":{"type":"string","description":"Chemin du fichier (relatif à la racine du projet ou absolu)."},`+
		`"offset":{"type":"integer","description":"Première ligne à lire (1 par défaut)."},`+
		`"limit":{"type":"integer","description":"Nombre maximal de lignes (2000 par défaut)."}`, "path")
}
func (t *readFile) Target(args json.RawMessage) (Target, error) { return t.pathTarget(args) }

func (t *readFile) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decode(args, &a); err != nil {
		return fail("%v", err), nil
	}
	if a.Path == "" {
		return fail("arguments invalides : path requis"), nil
	}
	if a.Offset < 1 {
		a.Offset = 1
	}
	if a.Limit < 1 {
		a.Limit = 2000
	}
	p := t.resolve(a.Path)
	data, err := os.ReadFile(p)
	if err != nil {
		return fail("lecture impossible : %v", err), nil
	}
	if isBinary(data) {
		return fail("fichier binaire : %s", a.Path), nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var sb strings.Builder
	for i := a.Offset - 1; i < len(lines) && i < a.Offset-1+a.Limit; i++ {
		fmt.Fprintf(&sb, "%d\t%s", i+1, lines[i])
		if !strings.HasSuffix(lines[i], "\n") {
			sb.WriteByte('\n')
		}
	}
	res := t.out(sb.String())
	res.Path = p
	return res, nil
}

// --- write_file ---

type writeFile struct{ base }

func (*writeFile) Name() string { return "write_file" }
func (*writeFile) Description() string {
	return "Crée ou remplace un fichier (dossiers parents créés)."
}
func (*writeFile) Risk() Risk { return Write }
func (*writeFile) Schema() json.RawMessage {
	return schema(`"path":{"type":"string","description":"Chemin du fichier."},`+
		`"content":{"type":"string","description":"Contenu complet du fichier."}`, "path", "content")
}
func (t *writeFile) Target(args json.RawMessage) (Target, error) { return t.pathTarget(args) }

func (t *writeFile) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(args, &a); err != nil {
		return fail("%v", err), nil
	}
	if a.Path == "" {
		return fail("arguments invalides : path requis"), nil
	}
	p := t.resolve(a.Path)
	res := Result{Path: p}
	mode := os.FileMode(0o644)
	if old, err := os.ReadFile(p); err == nil {
		res.HashBefore = hashOf(old)
		if st, err := os.Stat(p); err == nil {
			mode = st.Mode().Perm()
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fail("création du dossier impossible : %v", err), nil
	}
	if err := os.WriteFile(p, []byte(a.Content), mode); err != nil {
		return fail("écriture impossible : %v", err), nil
	}
	res.HashAfter = hashOf([]byte(a.Content))
	res.Content = fmt.Sprintf("fichier écrit : %s", a.Path)
	return res, nil
}

// --- edit_file ---

type editFile struct{ base }

func (*editFile) Name() string { return "edit_file" }
func (*editFile) Description() string {
	return "Remplace un texte exact dans un fichier ; échoue si le texte est absent ou ambigu (sauf replace_all)."
}
func (*editFile) Risk() Risk { return Write }
func (*editFile) Schema() json.RawMessage {
	return schema(`"path":{"type":"string","description":"Chemin du fichier."},`+
		`"old_string":{"type":"string","description":"Texte exact à remplacer."},`+
		`"new_string":{"type":"string","description":"Texte de remplacement."},`+
		`"replace_all":{"type":"boolean","description":"Remplacer toutes les occurrences."}`, "path", "old_string", "new_string")
}
func (t *editFile) Target(args json.RawMessage) (Target, error) { return t.pathTarget(args) }

func (t *editFile) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var a struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decode(args, &a); err != nil {
		return fail("%v", err), nil
	}
	if a.Path == "" || a.OldString == "" {
		return fail("arguments invalides : path et old_string requis"), nil
	}
	p := t.resolve(a.Path)
	data, err := os.ReadFile(p)
	if err != nil {
		return fail("lecture impossible : %v", err), nil
	}
	s := string(data)
	n := strings.Count(s, a.OldString)
	if n == 0 {
		return fail("texte introuvable dans %s", a.Path), nil
	}
	if n > 1 && !a.ReplaceAll {
		return fail("texte présent %d fois dans %s ; précisez le contexte ou utilisez replace_all", n, a.Path), nil
	}
	out := strings.ReplaceAll(s, a.OldString, a.NewString)
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(p, []byte(out), mode); err != nil {
		return fail("écriture impossible : %v", err), nil
	}
	return Result{
		Content:    fmt.Sprintf("%d remplacement(s) dans %s", n, a.Path),
		Path:       p,
		HashBefore: hashOf(data),
		HashAfter:  hashOf([]byte(out)),
	}, nil
}
