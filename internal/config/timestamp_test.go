package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
)

func tsFile(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func loadTS(t *testing.T, body string) error {
	t.Helper()
	_, err := Load(write(t, body), "", env(nil))
	return err
}

func TestAuditTimestampValid(t *testing.T) {
	ca := tsptest.NewCA(t)
	p := tsFile(t, ca.PEM)
	cfg, err := Load(write(t, "[audit.timestamp]\nurl = \"https://tsa.example.eu/tsr\"\nca_file = \""+p+"\"\n"), "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audit.Timestamp == nil || cfg.Audit.Timestamp.URL != "https://tsa.example.eu/tsr" || cfg.Audit.Timestamp.CAFile != p {
		t.Fatalf("timestamp = %+v", cfg.Audit.Timestamp)
	}
}

func TestAuditTimestampHTTPRemote(t *testing.T) {
	err := loadTS(t, "[audit.timestamp]\nurl = \"http://tsa.example.eu\"\n")
	if err == nil || err.Error() != "config: audit.timestamp.url must be https (http only for local hosts)" {
		t.Fatalf("err = %v", err)
	}
}

func TestAuditTimestampHTTPLocal(t *testing.T) {
	if err := loadTS(t, "[audit.timestamp]\nurl = \"http://127.0.0.1:8318\"\n"); err != nil {
		t.Fatal(err)
	}
}

func TestAuditTimestampCAFileMissing(t *testing.T) {
	err := loadTS(t, "[audit.timestamp]\nurl = \"https://t.eu\"\nca_file = \"/nonexistent/ca.pem\"\n")
	if err == nil || !strings.HasPrefix(err.Error(), "config: audit.timestamp.ca_file: ") || strings.HasSuffix(err.Error(), "no certificate") {
		t.Fatalf("err = %v", err)
	}
}

func TestAuditTimestampCAFileNoCert(t *testing.T) {
	p := tsFile(t, []byte("garbage"))
	err := loadTS(t, "[audit.timestamp]\nurl = \"https://t.eu\"\nca_file = \""+p+"\"\n")
	if err == nil || err.Error() != "config: audit.timestamp.ca_file: no certificate" {
		t.Fatalf("err = %v", err)
	}
}

func TestAuditTimestampCAFileWithoutURL(t *testing.T) {
	err := loadTS(t, "[audit.timestamp]\nca_file = \"/x\"\n")
	if err == nil || err.Error() != "config: audit.timestamp.ca_file without url" {
		t.Fatalf("err = %v", err)
	}
}

func TestAuditTimestampProjectRefused(t *testing.T) {
	_, err := Load("", write(t, "[audit.timestamp]\nurl = \"https://t.eu\"\n"), env(nil))
	if err == nil || !strings.Contains(err.Error(), "key not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestTimestampRoots(t *testing.T) {
	ca := tsptest.NewCA(t)
	cfg := baseCfg()
	pool, src, err := cfg.TimestampRoots()
	if pool != nil || src != "system" || err != nil {
		t.Fatalf("none: %v %q %v", pool, src, err)
	}
	cfg.Audit.Timestamp = &AuditTimestamp{URL: "https://t.eu"}
	if pool, src, err = cfg.TimestampRoots(); pool != nil || src != "system" || err != nil {
		t.Fatalf("system: %v %q %v", pool, src, err)
	}
	cfg.Audit.Timestamp.CAFile = tsFile(t, ca.PEM)
	if pool, src, err = cfg.TimestampRoots(); pool == nil || src != "ca_file" || err != nil {
		t.Fatalf("ca_file: %v %q %v", pool, src, err)
	}
	cfg.Audit.Timestamp.CAPEM = ca.PEM
	cfg.Audit.Timestamp.CAFile = "/nonexistent"
	if pool, src, err = cfg.TimestampRoots(); pool == nil || src != "policy" || err != nil {
		t.Fatalf("policy: %v %q %v", pool, src, err)
	}
	cfg.Audit.Timestamp.CAPEM = []byte("junk")
	if _, _, err = cfg.TimestampRoots(); err == nil {
		t.Fatal("want error on bad PEM")
	}
}

func TestApplyPolicyTimestamp(t *testing.T) {
	ca := tsptest.NewCA(t)
	cfg := baseCfg()
	cfg.Audit.Timestamp = &AuditTimestamp{URL: "https://a.eu", CAFile: tsFile(t, ca.PEM)}
	pol := &orgpolicy.Policy{ID: "p", Timestamp: &orgpolicy.AuditTimestamp{URL: "https://b.eu", CA: ca.PEM}}
	got, err := ApplyPolicy(cfg, pol)
	if err != nil {
		t.Fatal(err)
	}
	ts := got.Audit.Timestamp
	if ts == nil || ts.URL != "https://b.eu" || ts.CAFile != "" || string(ts.CAPEM) != string(ca.PEM) || !got.PolicyTimestampImposed {
		t.Fatalf("ts=%+v imposed=%v", ts, got.PolicyTimestampImposed)
	}
	if cfg.Audit.Timestamp.URL != "https://a.eu" {
		t.Error("input config mutated")
	}
	// Politique sans ca : le ca_file de l'utilisateur reste, l'URL est celle de la politique.
	userCA := tsFile(t, ca.PEM)
	cfg.Audit.Timestamp = &AuditTimestamp{URL: "https://a.eu", CAFile: userCA}
	got, err = ApplyPolicy(cfg, &orgpolicy.Policy{ID: "p", Timestamp: &orgpolicy.AuditTimestamp{URL: "https://b.eu"}})
	if err != nil {
		t.Fatal(err)
	}
	if ts := got.Audit.Timestamp; ts.URL != "https://b.eu" || ts.CAFile != userCA || len(ts.CAPEM) != 0 {
		t.Fatalf("sans ca : %+v", ts)
	}
	if _, src, err := got.TimestampRoots(); err != nil || src != "ca_file" {
		t.Fatalf("sans ca : src=%q err=%v", src, err)
	}
	cfg.Audit.Timestamp = &AuditTimestamp{URL: "https://a.eu", CAFile: userCA}
	got, err = ApplyPolicy(cfg, pol)
	if err != nil || got.Audit.Timestamp.CAFile != "" {
		t.Fatalf("avec ca : %+v %v", got.Audit.Timestamp, err)
	}
	if _, src, err := got.TimestampRoots(); err != nil || src != "policy" {
		t.Fatalf("avec ca : src=%q err=%v", src, err)
	}
	cfg.Audit.Timestamp = &AuditTimestamp{URL: "https://b.eu"}
	got, err = ApplyPolicy(cfg, pol)
	if err != nil || got.PolicyTimestampImposed || got.Audit.Timestamp.URL != "https://b.eu" {
		t.Fatalf("same url: imposed=%v err=%v", got.PolicyTimestampImposed, err)
	}
	cfg.Audit.Timestamp = nil
	got, _ = ApplyPolicy(cfg, pol)
	if !got.PolicyTimestampImposed {
		t.Error("absent must count as imposed")
	}
}
