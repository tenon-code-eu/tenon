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
	b := base{root: root, limit: limit, filter: o.readFilter, save: o.save}
	return []Tool{
		&readFile{b}, &writeFile{b}, &editFile{b},
		&globTool{b}, &grepTool{b},
	}
}

type options struct {
	readFilter func(tool, absPath string) bool
	save       func(path string, old []byte) error
}

// SnapshotMaxBytes est la taille au-delà de laquelle un fichier n'est pas copié
// avant écriture (même valeur que checkpoint.MaxBytes ; tools n'importe pas checkpoint).
const SnapshotMaxBytes = 10 << 20

// Option configure FS.
type Option func(*options)

// WithSnapshot fait appeler save(chemin, ancien contenu) par write_file et
// edit_file avant d'écraser un fichier existant de taille <= SnapshotMaxBytes.
// Une erreur de save annule l'écriture.
func WithSnapshot(save func(path string, old []byte) error) Option {
	return func(o *options) { o.save = save }
}

// WithReadFilter fait ignorer à grep et glob les fichiers pour lesquels f
// renvoie false.
func WithReadFilter(f func(tool, absPath string) bool) Option {
	return func(o *options) { o.readFilter = f }
}

type base struct {
	root   string
	limit  int
	filter func(tool, absPath string) bool
	save   func(path string, old []byte) error
}

func (b base) allowed(tool, abs string) bool { return b.filter == nil || b.filter(tool, abs) }

// snapshot sauvegarde old avant écriture. skipped vaut true quand le fichier
// dépasse SnapshotMaxBytes (pas de copie). Sans save, ne fait rien.
func (b base) snapshot(p string, old []byte) (skipped bool, err error) {
	if b.save == nil {
		return false, nil
	}
	if len(old) > SnapshotMaxBytes {
		return true, nil
	}
	return false, b.save(p, old)
}

// skipNote est ajouté au contenu du résultat quand la copie a été ignorée.
const skipNote = "\ncheckpoint skipped (file too large)"

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
		return fmt.Errorf("invalid arguments: %w", err)
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
		return Target{}, errors.New("invalid arguments: path required")
	}
	return Target{Paths: []string{b.resolve(a.Path)}}, nil
}

// --- read_file ---

type readFile struct{ base }

func (*readFile) Name() string { return "read_file" }
func (*readFile) Description() string {
	return "Reads a text file and returns its numbered lines."
}
func (*readFile) Risk() Risk { return Read }
func (*readFile) Schema() json.RawMessage {
	return schema(`"path":{"type":"string","description":"File path (relative to the project root, or absolute)."},`+
		`"offset":{"type":"integer","description":"First line to read (default 1)."},`+
		`"limit":{"type":"integer","description":"Maximum number of lines (default 2000)."}`, "path")
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
		return fail("invalid arguments: path required"), nil
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
		return fail("cannot read: %v", err), nil
	}
	if isBinary(data) {
		return fail("binary file: %s", a.Path), nil
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
	return "Creates or replaces a file (parent directories are created)."
}
func (*writeFile) Risk() Risk { return Write }
func (*writeFile) Schema() json.RawMessage {
	return schema(`"path":{"type":"string","description":"File path."},`+
		`"content":{"type":"string","description":"Full file content."}`, "path", "content")
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
		return fail("invalid arguments: path required"), nil
	}
	p := t.resolve(a.Path)
	res := Result{Path: p}
	mode := os.FileMode(0o644)
	skipped := false
	if old, err := os.ReadFile(p); err == nil {
		res.HashBefore = hashOf(old)
		if st, err := os.Stat(p); err == nil {
			mode = st.Mode().Perm()
		}
		sk, err := t.snapshot(p, old)
		if err != nil {
			return fail("cannot save checkpoint: %v", err), nil
		}
		skipped = sk
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fail("cannot create directory: %v", err), nil
	}
	if err := os.WriteFile(p, []byte(a.Content), mode); err != nil {
		return fail("cannot write: %v", err), nil
	}
	res.HashAfter = hashOf([]byte(a.Content))
	res.Content = fmt.Sprintf("file written: %s", a.Path)
	if skipped {
		res.Content += skipNote
		res.NoCheckpoint = true
	}
	return res, nil
}

// --- edit_file ---

type editFile struct{ base }

func (*editFile) Name() string { return "edit_file" }
func (*editFile) Description() string {
	return "Replaces an exact text in a file; fails if the text is missing or ambiguous (unless replace_all)."
}
func (*editFile) Risk() Risk { return Write }
func (*editFile) Schema() json.RawMessage {
	return schema(`"path":{"type":"string","description":"File path."},`+
		`"old_string":{"type":"string","description":"Exact text to replace."},`+
		`"new_string":{"type":"string","description":"Replacement text."},`+
		`"replace_all":{"type":"boolean","description":"Replace all occurrences."}`, "path", "old_string", "new_string")
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
		return fail("invalid arguments: path and old_string required"), nil
	}
	p := t.resolve(a.Path)
	data, err := os.ReadFile(p)
	if err != nil {
		return fail("cannot read: %v", err), nil
	}
	s := string(data)
	n := strings.Count(s, a.OldString)
	if n == 0 {
		return fail("text not found in %s", a.Path), nil
	}
	if n > 1 && !a.ReplaceAll {
		return fail("text found %d times in %s; add context or use replace_all", n, a.Path), nil
	}
	skipped, err := t.snapshot(p, data)
	if err != nil {
		return fail("cannot save checkpoint: %v", err), nil
	}
	out := strings.ReplaceAll(s, a.OldString, a.NewString)
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(p, []byte(out), mode); err != nil {
		return fail("cannot write: %v", err), nil
	}
	res := Result{
		Content:      fmt.Sprintf("%d replacement(s) in %s", n, a.Path),
		Path:         p,
		HashBefore:   hashOf(data),
		HashAfter:    hashOf([]byte(out)),
		NoCheckpoint: skipped,
	}
	if skipped {
		res.Content += skipNote
	}
	return res, nil
}
