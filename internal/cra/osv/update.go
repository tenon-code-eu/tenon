package osv

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/profile"
)

// DefaultBase est le miroir public des archives OSV.
const DefaultBase = "https://storage.googleapis.com/osv-vulnerabilities"

// MaxArchive borne la taille d'une archive téléchargée.
const MaxArchive = 1 << 30

// maxArchive est la borne effective ; variable pour que les tests l'abaissent.
var maxArchive int64 = MaxArchive

// maxRedirects borne les redirections suivies.
const maxRedirects = 5

// ecoName : liste blanche des noms d'écosystème (composant de chemin sûr).
var ecoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)

var errBase = errors.New("cra: osv mirror must be https (http only for local hosts)")

// CheckBase accepte https, ou http vers un hôte local (miroir interne, test).
func CheckBase(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return errBase
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if profile.IsLocalHost(u.Hostname()) {
			return nil
		}
	}
	return errBase
}

// Client renvoie un client HTTP pour le téléchargement : redirections bornées,
// jamais vers du http en clair, et jamais hors d'un hôte local quand la
// requête initiale était en http. noProxy ignore les proxys d'environnement ;
// localOnly refuse toute redirection vers un hôte non local, avant toute
// connexion. Les deux valent pour le profil strict-local seulement.
// http.DefaultTransport reste intact.
func Client(noProxy, localOnly bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if noProxy {
		tr.Proxy = nil
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("cra: osv: too many redirects")
			}
			// strict-local : aucune redirection ne quitte les hôtes locaux.
			if localOnly && !profile.IsLocalHost(req.URL.Hostname()) {
				return errors.New("cra: osv: redirect off local host refused")
			}
			if len(via) > 0 && via[0].URL.Scheme == "http" {
				// Base en http (hôte local) : on ne la quitte pas.
				if req.URL.Scheme != "http" || !profile.IsLocalHost(req.URL.Hostname()) {
					return errors.New("cra: osv: redirect off local host refused")
				}
				return nil
			}
			if req.URL.Scheme != "https" {
				return errors.New("cra: osv: non-https redirect refused")
			}
			return nil
		},
	}
}

// Update télécharge l'archive de chaque écosystème depuis <base>/<eco>/all.zip
// vers dir. Par écosystème : fichier temporaire dans le répertoire cible,
// contrôle (zip lisible, au moins une entrée .json), puis rename de l'archive
// et réécriture atomique de index.json. Un échec laisse l'archive et l'index
// précédents de cet écosystème intacts ; les écosystèmes déjà réussis restent
// en place. L'erreur nomme l'écosystème en échec.
func Update(ctx context.Context, c *http.Client, base string, ecos []string, dir string, now func() time.Time) error {
	if err := CheckBase(base); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cra: osv: %w", err)
	}
	idx, err := readIndexFile(dir, now())
	if err != nil {
		return err
	}
	for _, eco := range ecos {
		if err := updateOne(ctx, c, strings.TrimRight(base, "/"), eco, dir, now(), idx); err != nil {
			return fmt.Errorf("cra: osv: %s: %w", eco, err)
		}
	}
	return nil
}

// readIndexFile lit index.json. Un index illisible est mis de côté sous
// index.json.corrupt-<horodatage UTC> et remplacé par un index vide : db update
// est le seul moyen de réparer. Les archives sans entrée d'index restent
// illisibles tant qu'elles ne sont pas mises à jour (échec fermé).
func readIndexFile(dir string, now time.Time) (map[string]indexEntry, error) {
	idx := map[string]indexEntry{}
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cra: osv: %w", err)
	}
	if err := json.Unmarshal(b, &idx); err != nil || idx == nil {
		aside := filepath.Join(dir, "index.json.corrupt-"+now.UTC().Format("20060102T150405Z"))
		if err := os.Rename(filepath.Join(dir, "index.json"), aside); err != nil {
			return nil, fmt.Errorf("cra: osv: index.json: %w", err)
		}
		return map[string]indexEntry{}, nil
	}
	return idx, nil
}

func updateOne(ctx context.Context, c *http.Client, base, eco, dir string, now time.Time, idx map[string]indexEntry) error {
	if !ecoName.MatchString(eco) {
		return errors.New("invalid ecosystem name")
	}
	ecoDir := filepath.Join(dir, eco)
	if err := os.MkdirAll(ecoDir, 0o700); err != nil {
		return err
	}
	tmp, sum, err := download(ctx, c, base+"/"+url.PathEscape(eco)+"/all.zip", ecoDir)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // sans effet après un rename réussi
	n, err := countJSON(tmp)
	if err != nil {
		return err
	}
	if n < 1 {
		return errors.New("archive has no .json entry")
	}
	if err := os.Rename(tmp, filepath.Join(ecoDir, "all.zip")); err != nil {
		return err
	}
	idx[eco] = indexEntry{Date: now.UTC().Format(time.RFC3339), SHA256: sum, Entries: n}
	return writeIndexFile(dir, idx)
}

// download écrit le corps dans un fichier temporaire de dir, borné à
// maxArchive, et renvoie son chemin et son sha256. Le fichier est supprimé
// en cas d'erreur.
func download(ctx context.Context, c *http.Client, u, dir string) (path, sum string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArchive {
		return "", "", errors.New("archive too large")
	}
	f, err := os.CreateTemp(dir, "all.zip.tmp-*") // mode 0600
	if err != nil {
		return "", "", err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return "", "", fmt.Errorf("download: %w", err)
	}
	if n > maxArchive {
		return "", "", errors.New("archive too large")
	}
	if err = f.Sync(); err != nil {
		return "", "", err
	}
	if err = f.Close(); err != nil {
		return "", "", err
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// countJSON ouvre l'archive et compte ses entrées .json.
func countJSON(path string) (int, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, fmt.Errorf("invalid archive: %w", err)
	}
	defer zr.Close()
	n := 0
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".json") {
			n++
		}
	}
	return n, nil
}

// writeIndexFile réécrit index.json via un fichier temporaire et un rename.
func writeIndexFile(dir string, idx map[string]indexEntry) (err error) {
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "index.json.tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "index.json"))
}
