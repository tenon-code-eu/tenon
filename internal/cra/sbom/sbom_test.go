package sbom

import (
	"strings"
	"testing"
	"time"
)

func TestReadCycloneDX(t *testing.T) {
	b, err := Read("testdata/gomod.cdx.json")
	if err != nil {
		t.Fatal(err)
	}
	if b.SpecVersion != "1.5" || b.Generator != "cyclonedx-gomod v1.7.0" {
		t.Fatalf("meta: %+v", b)
	}
	if !b.Timestamp.Equal(time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC)) {
		t.Fatalf("timestamp: %v", b.Timestamp)
	}
	if len(b.Components) != 3 {
		t.Fatalf("components: %+v", b.Components)
	}
	c := b.Components[0]
	if c.Name != "github.com/foo/bar" || c.Version != "v1.2.3" || c.PURL != "pkg:golang/github.com/foo/bar@v1.2.3" || c.BOMRef != c.PURL {
		t.Fatalf("component: %+v", c)
	}
	if b.Components[2].PURL != "" || b.Components[2].Name != "vendored-blob" {
		t.Fatalf("sans purl: %+v", b.Components[2])
	}
}

func TestReadNested(t *testing.T) {
	b, err := Read("testdata/syft.cdx.json")
	if err != nil {
		t.Fatal(err)
	}
	if b.Generator != "syft 1.4.1" || b.SpecVersion != "1.4" {
		t.Fatalf("meta: %+v", b)
	}
	if len(b.Components) != 4 {
		t.Fatalf("components aplatis: %+v", b.Components)
	}
	var names []string
	for _, c := range b.Components {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "commons-text,commons-lang3,left-pad,lodash" {
		t.Fatalf("ordre: %s", got)
	}
	p, err := ParsePURL(b.Components[1].PURL)
	if err != nil {
		t.Fatal(err)
	}
	if p.OSVName() != "org.apache.commons:commons-lang3" || p.Version != "3.12.0" {
		t.Fatalf("purl: %+v", p)
	}
}

func TestReadRejects(t *testing.T) {
	cases := map[string]string{
		"testdata/v13.cdx.json":    "sbom: testdata/v13.cdx.json: unsupported CycloneDX version 1.3",
		"testdata/spdx.json":       "sbom: testdata/spdx.json:",
		"testdata/invalid.json":    "sbom: testdata/invalid.json:",
		"testdata/absent.cdx.json": "sbom: testdata/absent.cdx.json:",
	}
	for path, want := range cases {
		_, err := Read(path)
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: err = %v, want prefix %q", path, err, want)
		}
	}
}

func TestParsePURL(t *testing.T) {
	cases := []struct {
		in                      string
		typ, ns, name, ver, osv string
	}{
		{"pkg:golang/github.com/foo/bar@v1.2.3", "golang", "github.com/foo", "bar", "v1.2.3", "github.com/foo/bar"},
		{"pkg:npm/%40scope/pkg@1.0.0", "npm", "@scope", "pkg", "1.0.0", "@scope/pkg"},
		{"pkg:npm/@scope/pkg@1.0.0", "npm", "@scope", "pkg", "1.0.0", "@scope/pkg"},
		{"pkg:maven/org.apache/commons-text@1.9?type=jar", "maven", "org.apache", "commons-text", "1.9", "org.apache:commons-text"},
		{"pkg:pypi/Django_REST.framework@3.0", "pypi", "", "Django_REST.framework", "3.0", "django-rest-framework"},
		{"pkg:cargo/serde@1.0.0", "cargo", "", "serde", "1.0.0", "serde"},
		{"pkg:NPM/lodash@4.17.20#sub/path", "npm", "", "lodash", "4.17.20", "lodash"},
		{"pkg:npm/lodash", "npm", "", "lodash", "", "lodash"},
		{"pkg:nuget/Newtonsoft.Json@13.0.1", "nuget", "", "Newtonsoft.Json", "13.0.1", "newtonsoft.json"},
	}
	for _, c := range cases {
		p, err := ParsePURL(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if p.Type != c.typ || p.Namespace != c.ns || p.Name != c.name || p.Version != c.ver || p.OSVName() != c.osv {
			t.Errorf("%s: %+v osv=%q", c.in, p, p.OSVName())
		}
	}
	for _, bad := range []string{"", "golang/x@1", "pkg:", "pkg:npm", "pkg:npm/", "pkg:/x@1", "pkg:npm/%zz@1"} {
		if _, err := ParsePURL(bad); err == nil {
			t.Errorf("%q: erreur attendue", bad)
		}
	}
}

func TestEcosystem(t *testing.T) {
	want := map[string]string{
		"golang": "Go", "npm": "npm", "cargo": "crates.io", "pypi": "PyPI",
		"maven": "Maven", "nuget": "NuGet", "gem": "RubyGems", "composer": "Packagist",
	}
	for typ, eco := range want {
		got, ok := PURL{Type: typ, Name: "x"}.Ecosystem()
		if !ok || got != eco {
			t.Errorf("%s: %q %v", typ, got, ok)
		}
	}
	if _, ok := (PURL{Type: "deb", Name: "x"}).Ecosystem(); ok {
		t.Error("deb ne doit pas avoir d'écosystème")
	}
}
