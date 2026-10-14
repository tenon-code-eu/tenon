package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const (
	maxGlobResults = 1000
	maxGrepMatches = 500
)

func inGit(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

func (b base) rootTarget(json.RawMessage) (Target, error) {
	return Target{Paths: []string{filepath.Clean(b.root)}}, nil
}

// --- glob ---

type globTool struct{ base }

func (*globTool) Name() string { return "glob" }
func (*globTool) Description() string {
	return "Liste les fichiers dont le chemin correspond à un motif (ex. **/*.go), depuis la racine du projet."
}
func (*globTool) Risk() Risk { return Read }
func (*globTool) Schema() json.RawMessage {
	return schema(`"pattern":{"type":"string","description":"Motif glob (doublestar), ex. **/*.go."}`, "pattern")
}
func (t *globTool) Target(args json.RawMessage) (Target, error) {
	var a struct{ Pattern string }
	if err := decode(args, &a); err != nil {
		return Target{}, err
	}
	return t.rootTarget(args)
}

func (t *globTool) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var a struct {
		Pattern string `json:"pattern"`
	}
	if err := decode(args, &a); err != nil {
		return fail("%v", err), nil
	}
	if a.Pattern == "" {
		return fail("arguments invalides : pattern requis"), nil
	}
	matches, err := doublestar.Glob(os.DirFS(t.root), a.Pattern)
	if err != nil {
		return fail("motif invalide : %v", err), nil
	}
	var out []string
	for _, m := range matches {
		if !inGit(m) {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	suffix := ""
	if len(out) > maxGlobResults {
		out = out[:maxGlobResults]
		suffix = fmt.Sprintf("\n[… limité à %d résultats]", maxGlobResults)
	}
	return t.out(strings.Join(out, "\n") + suffix), nil
}

// --- grep ---

type grepTool struct{ base }

func (*grepTool) Name() string { return "grep" }
func (*grepTool) Description() string {
	return "Recherche une expression régulière (syntaxe Go) dans les fichiers texte ; renvoie chemin:ligne:texte."
}
func (*grepTool) Risk() Risk { return Read }
func (*grepTool) Schema() json.RawMessage {
	return schema(`"pattern":{"type":"string","description":"Expression régulière Go."},`+
		`"glob":{"type":"string","description":"Motif glob pour restreindre les fichiers (ex. **/*.go)."}`, "pattern")
}
func (t *grepTool) Target(args json.RawMessage) (Target, error) {
	var a struct{ Pattern string }
	if err := decode(args, &a); err != nil {
		return Target{}, err
	}
	return t.rootTarget(args)
}

func (t *grepTool) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var a struct {
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
	}
	if err := decode(args, &a); err != nil {
		return fail("%v", err), nil
	}
	if a.Pattern == "" {
		return fail("arguments invalides : pattern requis"), nil
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return fail("expression régulière invalide : %v", err), nil
	}
	if a.Glob != "" && !doublestar.ValidatePattern(a.Glob) {
		return fail("motif glob invalide : %s", a.Glob), nil
	}
	var lines []string
	truncated := false
	errStop := errors.New("stop")
	walkErr := filepath.WalkDir(t.root, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(t.root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if a.Glob != "" {
			if ok, _ := doublestar.Match(a.Glob, rel); !ok {
				return nil
			}
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil || isBinary(data) {
			return nil
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for n := 1; sc.Scan(); n++ {
			if re.Match(sc.Bytes()) {
				if len(lines) >= maxGrepMatches {
					truncated = true
					return errStop
				}
				lines = append(lines, fmt.Sprintf("%s:%d:%s", rel, n, sc.Text()))
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStop) {
		if cerr := ctx.Err(); cerr != nil {
			return Result{}, cerr
		}
		return fail("parcours impossible : %v", walkErr), nil
	}
	s := strings.Join(lines, "\n")
	if truncated {
		s += fmt.Sprintf("\n[… limité à %d correspondances]", maxGrepMatches)
	}
	return t.out(s), nil
}
