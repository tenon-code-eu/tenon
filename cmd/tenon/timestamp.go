package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/tsp"
)

// timestampTimeout borne chaque requête à l'autorité d'horodatage.
const timestampTimeout = 10 * time.Second

// errAuditEmpty : rien à ancrer, le journal d'audit est vide ou absent.
var errAuditEmpty = errors.New("audit log is empty")

// auditDir est le dossier du journal d'audit sous le dossier de données.
func auditDir(dataDir string) string { return filepath.Join(dataDir, "audit") }

// timestampURL renvoie l'URL de l'autorité d'horodatage de cfg ; "" (table
// absente, vide ou url = "") : horodatage désactivé. Seule source de vérité
// pour tous les appelants.
func timestampURL(cfg config.Config) string {
	if cfg.Audit.Timestamp == nil {
		return ""
	}
	return cfg.Audit.Timestamp.URL
}

// timestampClient renvoie le client HTTP de la TSA : sans proxy sous
// strict-local (une TSA locale ne doit pas sortir par un proxy), proxy de
// l'environnement sinon. Les redirections ne sont jamais suivies : une
// redirection https → http contournerait tsp.CheckURL (un 30x échoue alors
// en « HTTP 30x »).
func timestampClient(p profile.Name) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if p == profile.StrictLocal {
		tr.Proxy = nil
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// timestampProfileErr refuse, sous strict-local, une autorité d'horodatage
// dont l'hôte n'est pas local (nil sinon ou sans horodatage).
func timestampProfileErr(lang i18n.Lang, cfg config.Config, p profile.Name) error {
	u := timestampURL(cfg)
	if u == "" || p != profile.StrictLocal {
		return nil
	}
	if h := tsp.Host(u); !profile.IsLocalHost(h) {
		return errors.New(i18n.T(lang, "err.profile_timestamp_host", p, h))
	}
	return nil
}

// anchor fait horodater la tête de la chaîne d'audit de dataDir par la TSA de
// cfg et range le jeton. already : un jeton porte déjà cette tête (aucune
// requête). Chaque requête est bornée par timestampTimeout.
func anchor(ctx context.Context, cfg config.Config, p profile.Name, dataDir string) (path string, info tsp.Info, already bool, err error) {
	u := timestampURL(cfg)
	if u == "" {
		return "", tsp.Info{}, false, errors.New("not configured")
	}
	dir := auditDir(dataDir)
	head, err := audit.Head(dir)
	if err != nil {
		return "", tsp.Info{}, false, err
	}
	if head == "" {
		return "", tsp.Info{}, false, errAuditEmpty
	}
	if already, err = audit.Anchored(dir, head); err != nil || already {
		return "", tsp.Info{}, already, err
	}
	digest, err := audit.Digest(head)
	if err != nil {
		return "", tsp.Info{}, false, err
	}
	roots, _, err := cfg.TimestampRoots()
	if err != nil {
		return "", tsp.Info{}, false, err
	}
	if err := tsp.NeedRoots(roots); err != nil {
		return "", tsp.Info{}, false, err
	}
	rctx, cancel := context.WithTimeout(ctx, timestampTimeout)
	defer cancel()
	token, info, err := tsp.Request(rctx, timestampClient(p), u, digest, roots)
	if err != nil {
		return "", tsp.Info{}, false, err
	}
	if path, err = audit.SaveToken(dir, info, token); err != nil {
		return "", tsp.Info{}, false, err
	}
	return path, info, false, nil
}

// anchorAtEnd ancre la chaîne en fin de session, une fois le journal fermé.
// Un échec n'est qu'un avis (masqué par redact) : le code de sortie ne change
// pas. Contexte propre : celui de la session peut déjà être annulé (Ctrl+C),
// le délai borne l'attente.
func anchorAtEnd(lang i18n.Lang, cfg config.Config, dataDir string, redact func(string) string, stderr io.Writer) {
	if timestampURL(cfg) == "" {
		return
	}
	_, _, _, err := anchor(context.Background(), cfg, cfg.Profile, dataDir)
	if err != nil && stderr != nil {
		fmt.Fprintln(stderr, redact(i18n.T(lang, "audit.timestamp_failed", err)))
	}
}

// stripControl remplace par « ? » les caractères de contrôle (C0, C1) et de
// format : le texte vient d'un jeton non fiable et irait sur le terminal.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		return r
	}, s)
}

// genTime formate l'heure d'un jeton (RFC 3339, UTC).
func genTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// auditTimestamp exécute « tenon audit timestamp » : ancre la tête actuelle
// de la chaîne. Codes : 0 ancré (ou déjà ancré), 1 sinon.
func auditTimestamp(ctx context.Context, lang i18n.Lang, stdout, stderr io.Writer, getenv func(string) string, wd, globalPath string) int {
	s, err := loadSetup(lang, getenv, wd, globalPath)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	cfg := s.cfg
	if timestampURL(cfg) == "" {
		fmt.Fprintln(stderr, i18n.T(lang, "audit.timestamp_unconfigured"))
		return 1
	}
	if err := timestampProfileErr(lang, cfg, cfg.Profile); err != nil {
		for _, n := range profileNotices(lang, cfg) {
			fmt.Fprintln(stderr, n)
		}
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	path, info, already, err := anchor(ctx, cfg, cfg.Profile, config.DataDir(getenv))
	switch {
	case errors.Is(err, errAuditEmpty):
		fmt.Fprintln(stderr, i18n.T(lang, "audit.timestamp_empty"))
		return 1
	case err != nil:
		fmt.Fprintln(stderr, redactText(s.redactor)(i18n.T(lang, "audit.timestamp_failed", err)))
		return 1
	case already:
		fmt.Fprintln(stdout, i18n.T(lang, "audit.timestamp_already"))
		return 0
	}
	fmt.Fprintln(stdout, i18n.T(lang, "audit.timestamp_ok", filepath.Base(path), genTime(info.GenTime), info.TSA))
	return 0
}

// hasTokens dit si le dossier d'audit dir contient au moins un jeton .tsr.
func hasTokens(dir string) bool {
	ents, err := os.ReadDir(filepath.Join(dir, audit.TimestampDir))
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tsr") {
			return true
		}
	}
	return false
}

// verifyRoots choisit les racines de vérification des jetons du dossier
// d'audit dir : fichier tsaCA (--tsa-ca, erreur s'il est illisible ou sans
// certificat), sinon, si dir contient un jeton, celles de la configuration
// (politique > ca_file > système ; une configuration illisible est signalée
// sur stderr et les racines système sont utilisées), sinon nil (système).
func verifyRoots(lang i18n.Lang, tsaCA, dir string, getenv func(string) string, wd, globalPath string, stderr io.Writer) (*x509.CertPool, error) {
	if tsaCA != "" {
		b, err := os.ReadFile(tsaCA)
		if err != nil {
			return nil, fmt.Errorf("--tsa-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(b) {
			return nil, fmt.Errorf("--tsa-ca: %s: no certificate", tsaCA)
		}
		return pool, nil
	}
	if !hasTokens(dir) {
		return nil, nil
	}
	s, err := loadSetup(lang, getenv, wd, globalPath)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return nil, nil
	}
	roots, _, err := s.cfg.TimestampRoots()
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return nil, nil
	}
	return roots, nil
}

// auditVerify exécute « tenon audit verify [--tsa-ca <pem>] » : la chaîne,
// puis les jetons d'horodatage. Codes : 0 tout est valide, 1 chaîne cassée,
// jeton invalide ou orphelin, ou erreur d'usage.
func auditVerify(lang i18n.Lang, args []string, stdout, stderr io.Writer, getenv func(string) string, wd, globalPath string) int {
	flags := flag.NewFlagSet("tenon audit verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	tsaCA := flags.String("tsa-ca", "", i18n.T(lang, "flag.tsa_ca"))
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(stderr, i18n.T(lang, "err.unexpected_args", flags.Arg(0)))
		return 1
	}
	dir := auditDir(config.DataDir(getenv))
	hash, n, err := audit.Verify(dir)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	fmt.Fprintln(stdout, i18n.T(lang, "audit.ok", n, hash))
	roots, err := verifyRoots(lang, *tsaCA, dir, getenv, wd, globalPath, stderr)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	sum, err := audit.VerifyTimestamps(dir, roots)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	for _, l := range sum.Errors {
		fmt.Fprintln(stderr, stripControl(l))
	}
	for _, l := range sum.Warnings {
		fmt.Fprintln(stderr, stripControl(l))
	}
	switch {
	case sum.Count == 0:
		fmt.Fprintln(stdout, i18n.T(lang, "audit.timestamps_none"))
	case sum.Valid == 0:
		fmt.Fprintln(stdout, i18n.T(lang, "audit.timestamps_none_valid", sum.Count))
	default:
		fmt.Fprintln(stdout, i18n.T(lang, "audit.timestamps_summary", sum.Valid, sum.LastFile, sum.LastLine, genTime(sum.LastTime), stripControl(sum.TSA), sum.LinesAfter))
	}
	if len(sum.Errors) > 0 {
		return 1
	}
	return 0
}

// timestampLine renvoie la ligne « config check » de l'horodatage ("" sans
// horodatage) ; une erreur si ses racines sont illisibles.
func timestampLine(lang i18n.Lang, cfg config.Config) (string, error) {
	u := timestampURL(cfg)
	if u == "" {
		return "", nil
	}
	_, src, err := cfg.TimestampRoots()
	if err != nil {
		return "", err
	}
	return i18n.T(lang, "config.timestamp", u, src), nil
}
