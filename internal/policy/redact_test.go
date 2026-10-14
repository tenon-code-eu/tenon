package policy

import (
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
	_, counts := r.Redact("a@x.fr b@x.fr token=abcdefghijklmnop")
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
