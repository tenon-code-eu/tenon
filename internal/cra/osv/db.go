// Package osv lit la base de vulnérabilités OSV hors ligne (une archive
// all.zip par écosystème) et rapproche les composants d'un SBOM de ses entrées.
package osv

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNoArchive signale un écosystème absent de la base locale : ses
// composants ne peuvent pas être vérifiés (ce n'est pas « aucune vulnérabilité »).
var ErrNoArchive = errors.New("no archive for ecosystem")

// indexEntry est une ligne de index.json.
type indexEntry struct {
	Date    string `json:"date"`
	SHA256  string `json:"sha256"`
	Entries int    `json:"entries"`
}

// DB est une base OSV locale ; les archives sont chargées à la demande, par
// écosystème, en ne gardant que les entrées des paquets demandés (MatchAll
// demande d'un coup tous les noms des composants ; un nom nouveau relit
// l'archive pour l'union des noms). Sûre pour un usage concurrent.
type DB struct {
	dir   string
	index map[string]indexEntry
	dates map[string]time.Time

	mu    sync.Mutex
	ecos  map[string]*ecoIndex
	fails map[string]error
}

// ecoIndex associe un nom de paquet OSV (normalisé) aux entrées qui le citent.
// names : noms demandés à la lecture ; kept : entrées gardées.
type ecoIndex struct {
	names  map[string]bool
	byName map[string][]*entry
	kept   int
}

// header est l'en-tête minimal d'une entrée OSV, décodé pour toutes les
// entrées afin de décider lesquelles garder.
type header struct {
	Withdrawn string `json:"withdrawn"`
	Affected  []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
	} `json:"affected"`
}

// entry ne garde que les champs utiles d'une entrée OSV.
type entry struct {
	ID        string     `json:"id"`
	Aliases   []string   `json:"aliases"`
	Summary   string     `json:"summary"`
	Withdrawn string     `json:"withdrawn"`
	Affected  []affected `json:"affected"`
}

type affected struct {
	Package struct {
		Ecosystem string `json:"ecosystem"`
		Name      string `json:"name"`
	} `json:"package"`
	Versions []string   `json:"versions"`
	Ranges   []osvRange `json:"ranges"`
}

type osvRange struct {
	Type   string  `json:"type"`
	Events []event `json:"events"`
}

type event struct {
	Introduced   string `json:"introduced"`
	Fixed        string `json:"fixed"`
	LastAffected string `json:"last_affected"`
	Limit        string `json:"limit"`
}

// Open lit <dir>/index.json. Seuls les écosystèmes présents dans l'index sont
// interrogeables ; l'absence de index.json renvoie une erreur os.ErrNotExist.
func Open(dir string) (*DB, error) {
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("cra: osv: %w", err)
	}
	var idx map[string]indexEntry
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("cra: osv: index.json: %w", err)
	}
	dates := make(map[string]time.Time, len(idx))
	for eco, e := range idx {
		d, err := time.Parse(time.RFC3339, e.Date)
		if err != nil {
			return nil, fmt.Errorf("cra: osv: index.json: %s: invalid date %q", eco, e.Date)
		}
		dates[eco] = d
	}
	return &DB{
		dir:   dir,
		index: idx,
		dates: dates,
		ecos:  map[string]*ecoIndex{},
		fails: map[string]error{},
	}, nil
}

// Date renvoie la date de l'archive d'un écosystème.
func (db *DB) Date(eco string) (time.Time, bool) {
	d, ok := db.dates[eco]
	return d, ok
}

// OldestDate renvoie la date de l'archive la plus ancienne ; false si l'index est vide.
func (db *DB) OldestDate() (time.Time, bool) {
	var oldest time.Time
	found := false
	for _, d := range db.dates {
		if !found || d.Before(oldest) {
			oldest, found = d, true
		}
	}
	return oldest, found
}

// load renvoie l'index d'un écosystème couvrant les noms demandés : l'archive
// est lue au premier appel, puis relue pour l'union des noms si un nom manque.
// Un échec est mémorisé : une archive illisible le reste pour toute la session.
func (db *DB) load(eco string, names map[string]bool) (*ecoIndex, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err, ok := db.fails[eco]; ok {
		return nil, err
	}
	want := names
	if ix, ok := db.ecos[eco]; ok {
		covered := true
		for n := range names {
			if !ix.names[n] {
				covered = false
				break
			}
		}
		if covered {
			return ix, nil
		}
		want = make(map[string]bool, len(ix.names)+len(names))
		for n := range ix.names {
			want[n] = true
		}
		for n := range names {
			want[n] = true
		}
	}
	ie, ok := db.index[eco]
	if !ok {
		return nil, fmt.Errorf("cra: osv: %s: %w", eco, ErrNoArchive)
	}
	ix, err := readArchive(filepath.Join(db.dir, eco, "all.zip"), eco, ie.SHA256, want)
	if err != nil {
		err = fmt.Errorf("cra: osv: %s: unreadable archive: %w", eco, err)
		db.fails[eco] = err
		return nil, err
	}
	db.ecos[eco] = ix
	return ix, nil
}

// readArchive vérifie l'empreinte de toute l'archive (si l'index en donne
// une), décode l'en-tête de chaque entrée JSON et n'indexe, par nom de paquet
// de l'écosystème, que les entrées qui citent un des noms demandés. Une erreur
// JSON dans n'importe quelle entrée rend l'archive illisible (échec fermé).
func readArchive(path, eco, sum string, names map[string]bool) (*ecoIndex, error) {
	if sum != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
			return nil, fmt.Errorf("sha256 mismatch (got %s, index %s)", got, sum)
		}
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	ix := &ecoIndex{names: names, byName: map[string][]*entry{}}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || !strings.HasSuffix(zf.Name, ".json") {
			continue
		}
		data, err := readFile(zf)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", zf.Name, err)
		}
		var h header
		if err := json.Unmarshal(data, &h); err != nil {
			return nil, fmt.Errorf("%s: %w", zf.Name, err)
		}
		if h.Withdrawn != "" {
			continue
		}
		var hit []string
		for _, a := range h.Affected {
			if a.Package.Ecosystem != eco {
				continue
			}
			if n := normName(eco, a.Package.Name); names[n] && !contains(hit, n) {
				hit = append(hit, n)
			}
		}
		if len(hit) == 0 {
			continue
		}
		// Décodage complet des seules entrées gardées.
		e := &entry{}
		if err := json.Unmarshal(data, e); err != nil {
			return nil, fmt.Errorf("%s: %w", zf.Name, err)
		}
		ix.kept++
		for _, n := range hit {
			ix.byName[n] = append(ix.byName[n], e)
		}
	}
	return ix, nil
}

// readFile lit une entrée de l'archive.
func readFile(zf *zip.File) ([]byte, error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var pypiSep = regexp.MustCompile(`[-_.]+`)

// normName normalise un nom de paquet OSV comme sbom.PURL.OSVName (PEP 503
// pour PyPI, minuscules pour NuGet).
func normName(eco, name string) string {
	switch eco {
	case "PyPI":
		return pypiSep.ReplaceAllString(strings.ToLower(name), "-")
	case "NuGet":
		return strings.ToLower(name)
	}
	return name
}
