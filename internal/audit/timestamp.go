package audit

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/tsp"
)

// TimestampDir est le sous-dossier de dir qui reçoit les jetons RFC 3161.
const TimestampDir = "timestamps"

// clockTolerance : écart toléré entre l'horloge locale et la TSA.
const clockTolerance = 5 * time.Minute

// Head renvoie le dernier hash de la chaîne, "" si le journal est vide ou absent.
func Head(dir string) (string, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	_, h, err := readTail(dir)
	if err != nil {
		return "", err
	}
	if h == genesis {
		return "", nil
	}
	return h, nil
}

// Digest décode "sha256:<64 hex>" en 32 octets bruts (l'empreinte horodatée).
func Digest(head string) ([]byte, error) {
	h, ok := strings.CutPrefix(head, "sha256:")
	if !ok || len(h) != 64 {
		return nil, fmt.Errorf("invalid chain hash %q", head)
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("invalid chain hash %q: %w", head, err)
	}
	return b, nil
}

// tokenFiles liste les *.tsr de dir/timestamps triés par nom ; dossier absent : aucun.
func tokenFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(dir, TimestampDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading timestamps directory: %w", err)
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".tsr" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Anchored indique si un jeton lisible de dir/timestamps porte l'empreinte de head.
func Anchored(dir, head string) (bool, error) {
	want, err := Digest(head)
	if err != nil {
		return false, err
	}
	names, err := tokenFiles(dir)
	if err != nil {
		return false, err
	}
	suffix := "-" + strings.TrimPrefix(head, "sha256:")[:12] + ".tsr"
	for _, n := range names {
		if !strings.HasSuffix(n, suffix) {
			continue
		}
		der, err := os.ReadFile(filepath.Join(dir, TimestampDir, n))
		if err != nil {
			continue
		}
		info, err := tsp.ParseToken(der)
		if err == nil && bytes.Equal(info.Digest, want) {
			return true, nil
		}
	}
	return false, nil
}

// SaveToken range le jeton (DER seul) sous un nom dérivé de genTime et de la
// tête ancrée. Un jeton déjà présent sous ce nom est conservé tel quel.
func SaveToken(dir string, info tsp.Info, token []byte) (string, error) {
	if len(info.Digest) != 32 {
		return "", fmt.Errorf("token digest has %d bytes, want 32", len(info.Digest))
	}
	td := filepath.Join(dir, TimestampDir)
	if err := os.MkdirAll(td, 0o700); err != nil {
		return "", fmt.Errorf("creating timestamps directory: %w", err)
	}
	name := info.GenTime.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(info.Digest)[:12] + ".tsr"
	path := filepath.Join(td, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return path, nil
		}
		return "", fmt.Errorf("writing %s: %w", name, err)
	}
	if _, err := f.Write(token); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("writing %s: %w", name, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("writing %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("writing %s: %w", name, err)
	}
	return path, nil
}

// TimestampSummary résume l'état de l'ancrage de la chaîne.
type TimestampSummary struct {
	Count, Valid     int
	LastFile         string
	LastLine         int
	LastTime         time.Time
	TSA              string
	LinesAfter       int
	Errors, Warnings []string
}

// chainPos situe une ligne de la chaîne ; maxFile/maxLine désignent la ligne
// de plus grand ts parmi le préfixe jusqu'à celle-ci.
type chainPos struct {
	file    string
	line    int
	index   int // rang global, dès 0
	maxTS   time.Time
	maxFile string
	maxLine int
}

// VerifyTimestamps vérifie la chaîne puis chaque jeton de dir/timestamps.
// L'erreur n'est non nil que si la chaîne est cassée ou illisible.
func VerifyTimestamps(dir string, roots *x509.CertPool) (TimestampSummary, error) {
	sum := TimestampSummary{Errors: []string{}, Warnings: []string{}}
	pos := map[string]chainPos{}
	var maxTS time.Time
	var maxFile string
	var maxLine, idx int
	_, total, err := walk(dir, func(file string, n int, l Line) {
		// Un ts illisible est ignoré ici : l'intégrité relève de la chaîne.
		if ts, perr := time.Parse(time.RFC3339Nano, l.TS); perr == nil && (maxFile == "" || ts.After(maxTS)) {
			maxTS, maxFile, maxLine = ts, file, n
		}
		pos[l.Hash] = chainPos{file, n, idx, maxTS, maxFile, maxLine}
		idx++
	})
	if err != nil {
		return sum, err
	}
	names, err := tokenFiles(dir)
	if err != nil {
		return sum, err
	}
	lastIdx := -1
	var lastGen time.Time
	for _, name := range names {
		sum.Count++
		der, err := os.ReadFile(filepath.Join(dir, TimestampDir, name))
		if err != nil {
			sum.Errors = append(sum.Errors, fmt.Sprintf("timestamp %s: %v", name, err))
			continue
		}
		info, err := tsp.Verify(der, roots)
		if err != nil {
			sum.Errors = append(sum.Errors, fmt.Sprintf("timestamp %s: %v", name, err))
			continue
		}
		p, ok := pos["sha256:"+hex.EncodeToString(info.Digest)]
		if !ok {
			sum.Errors = append(sum.Errors, fmt.Sprintf("timestamp %s: anchored hash not in chain", name))
			continue
		}
		sum.Valid++
		if p.maxFile != "" && p.maxTS.After(info.GenTime.Add(clockTolerance)) {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("timestamp %s: %s:%d ts after genTime (local clock?)", name, p.maxFile, p.maxLine))
		}
		if p.index > lastIdx || (p.index == lastIdx && info.GenTime.After(lastGen)) {
			lastIdx, lastGen = p.index, info.GenTime
			sum.LastFile, sum.LastLine, sum.LastTime, sum.TSA = p.file, p.line, info.GenTime, info.TSA
		}
	}
	sum.LinesAfter = total - (lastIdx + 1)
	return sum, nil
}
