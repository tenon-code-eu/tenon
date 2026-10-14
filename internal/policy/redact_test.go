package policy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRedactTable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----", "[SECRET_1]"},
		{"MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf", "MISTRAL_API_KEY=[SECRET_1]"},
		{"FR76 3000 6000 0112 3456 7890 189", "[IBAN_1]"},
		{"FR76 3000 6000 0112 3456 7890 188", "FR76 3000 6000 0112 3456 7890 188"},
		{"NIR 1 85 05 78 006 084 91", "NIR [NIR_1]"},
		{"NIR 1 85 05 78 006 084 92", "NIR 1 85 05 78 006 084 92"},
		{"jean.dupont@exemple.fr et jean.dupont@exemple.fr", "[EMAIL_1] et [EMAIL_1]"},
		{"06 12 34 56 78", "[TEL_1]"},
		{"+33 6 12 34 56 78", "[TEL_1]"},
		{"version 1.2.3, port 8080, func main()", "version 1.2.3, port 8080, func main()"},
		{"550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
		// extras
		{"AKIAIOSFODNN7EXAMPLE", "[SECRET_1]"},
		{"ghp_" + strings.Repeat("a", 36), "[SECRET_1]"},
		{"sk-" + strings.Repeat("a1", 15), "[SECRET_1]"},
		{"eyJhbGciOi.eyJzdWIiOiIx.SflKxwRJSMeKKF2QT4", "[SECRET_1]"},
		{"disk-usage-monitoring-service-config", "disk-usage-monitoring-service-config"},
		{`{"password":"hunter2hunter2"}`, `{"password":"[SECRET_1]"}`},
		{"tel 0612345678, 0612345678.", "tel [TEL_1], [TEL_1]."},
		{"id 12345678901234567890", "id 12345678901234567890"},
		// code that must not change
		{`token = r.URL.Query().Get("token")`, `token = r.URL.Query().Get("token")`},
		{`tokenizer = NewTokenizer(cfg)`, `tokenizer = NewTokenizer(cfg)`},
		{`accessToken = getAccessToken()`, `accessToken = getAccessToken()`},
		{`apiKey: process.env.OPENAI_API_KEY,`, `apiKey: process.env.OPENAI_API_KEY,`},
		{`password = getpass.getpass()`, `password = getpass.getpass()`},
		{`token: CancellationToken)`, `token: CancellationToken)`},
		{`tokens = tokenize(text, opts)`, `tokens = tokenize(text, opts)`},
		{`secretName=projects/x/secrets/y`, `secretName=projects/x/secrets/y`},
		{`git@github.com:org/repo.git`, `git@github.com:org/repo.git`},
		{`npm i react@18.3.1`, `npm i react@18.3.1`},
		{`gopls@v0.15.0`, `gopls@v0.15.0`},
		{`a@b.T`, `a@b.T`},
		{`const digits = "0123456789"`, `const digits = "0123456789"`},
		// new masking
		{`password = "Zx8kq2LmNp4Rt6Vw"`, `password = "[SECRET_1]"`},
		{`{"api_key": "Zx8kq2LmNp4Rt6Vw9YbC"}`, `{"api_key": "[SECRET_1]"}`},
		{`{"c":"\"api_key\": \"Zx8kq2LmNp4Rt6Vw9YbC\""}`, `{"c":"\"api_key\": \"[SECRET_1]\""}`},
		{"export TOKEN=Zx8kq2LmNp4Rt6Vw; ls", "export TOKEN=[SECRET_1]; ls"},
		{"github_pat_" + strings.Repeat("aB3", 10), "[SECRET_1]"},
		{"sk_live_" + strings.Repeat("aB3d", 5), "[SECRET_1]"},
		{"xoxb-1234567890-abcdef", "[SECRET_1]"},
		{"postgres://user:p4ssw0rd!@db:5432/x", "postgres://user:[SECRET_1]@db:5432/x"},
		{"Authorization: Bearer abcdefghij0123456789xyz", "Authorization: Bearer [SECRET_1]"},
		// tool-prefixed lines (read_file "n\t", grep "path:n:") and env-style keys
		{"1\tMISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf", "1\tMISTRAL_API_KEY=[SECRET_1]"},
		{"3\t  api_key: Zx8kq2LmNp4Rt6Vw9YbC1dEf", "3\t  api_key: [SECRET_1]"},
		{"conf/app.yml:3:  api_key: Zx8kq2LmNp4Rt6Vw9YbC1dEf", "conf/app.yml:3:  api_key: [SECRET_1]"},
		{"  - password: Zx8kq2LmNp4Rt6Vw9YbC1dEf", "  - password: [SECRET_1]"},
		{"docker run -e API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf img", "docker run -e API_KEY=[SECRET_1] img"},
		{"TOKEN=Zx8kq2LmNp4Rt6Vw9YbC1dEf ./run.sh", "TOKEN=[SECRET_1] ./run.sh"},
	}
	for _, c := range cases {
		r, err := NewRedactor(nil)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := r.Redact(c.in)
		if got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactCounts(t *testing.T) {
	r, _ := NewRedactor(nil)
	_, counts := r.Redact("a@x.fr b@x.fr\nMISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw")
	want := map[string]int{"EMAIL": 2, "SECRET": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts = %v, want %v", counts, want)
	}
}

func TestRedactStableAcrossCalls(t *testing.T) {
	r, _ := NewRedactor(nil)
	a, _ := r.Redact("a@x.fr")
	b, _ := r.Redact("b@x.fr a@x.fr")
	if a != "[EMAIL_1]" || b != "[EMAIL_2] [EMAIL_1]" {
		t.Fatalf("got %q %q", a, b)
	}
}

func TestRedactCorsicaNIR(t *testing.T) {
	// 2A: compute with 19. n=1850519..., key derived accordingly.
	n := int64(1850519006084)
	key := 97 - n%97
	in := "1 85 05 2A 006 084 " + twoDigits(key)
	r, _ := NewRedactor(nil)
	got, _ := r.Redact(in)
	if got != "[NIR_1]" {
		t.Fatalf("got %q", got)
	}
}

func twoDigits(n int64) string {
	s := "0" + string(rune('0'+n/10))
	_ = s
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}

func TestCustomRule(t *testing.T) {
	r, err := NewRedactor([]RedactionRule{{Name: "client", Pattern: `CLT-\d{6}`}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := r.Redact("dossier CLT-123456")
	if got != "dossier [CLIENT_1]" {
		t.Fatalf("got %q", got)
	}
	if _, err := NewRedactor([]RedactionRule{{Name: "x", Pattern: `(`}}); err == nil {
		t.Fatal("want compile error")
	}
}

func TestRedactConcurrent(t *testing.T) {
	r, _ := NewRedactor(nil)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				r.Redact("a@x.fr 0612345678 token=abcdefghijklmnop")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestRedactNeverRemasksMarkers(t *testing.T) {
	r, _ := NewRedactor(nil)
	for i := 0; i < 100; i++ {
		r.Redact(fmt.Sprintf("a%d@x.fr", i))
		r.Redact(fmt.Sprintf("sk-%s%d", strings.Repeat("a", 20), i))
	}
	got, _ := r.Redact("token=[SECRET_100]")
	if got != "token=[SECRET_100]" {
		t.Fatalf("got %q", got)
	}
	got, _ = r.Redact("MISTRAL_API_KEY=[SECRET_100]")
	if got != "MISTRAL_API_KEY=[SECRET_100]" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactLiteral(t *testing.T) {
	r, _ := NewRedactor(nil)
	r.AddLiteral("plainkeyvalue42")
	r.AddLiteral("") // ignorée
	got, counts := r.Redact("printenv -> plainkeyvalue42\nencore plainkeyvalue42")
	if got != "printenv -> [SECRET_1]\nencore [SECRET_1]" || counts["SECRET"] != 2 {
		t.Fatalf("got %q %v", got, counts)
	}
	// La valeur littérale garde le même marqueur qu'un masquage par règle.
	got, _ = r.Redact("MISTRAL_API_KEY=plainkeyvalue42")
	if got != "MISTRAL_API_KEY=[SECRET_1]" {
		t.Fatalf("got %q", got)
	}
	if got, _ := r.Redact("rien ici"); got != "rien ici" {
		t.Fatalf("got %q", got)
	}
}
