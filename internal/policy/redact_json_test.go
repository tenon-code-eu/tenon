package policy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactJSON(t *testing.T) {
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{
		`{"text":"voici :\nMISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf\nfin","n":12345678901234567890}`,
		`{"text":"voici :\napi_key = Zx8kq2LmNp4Rt6Vw9YbC1dEf\nfin"}`,
		`{"args":{"api_key":"Zx8kq2LmNp4Rt6Vw9YbC1dEf"}}`,
		`["a <b> & c", "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"]`,
	} {
		out, counts := r.RedactJSON(in)
		if strings.Contains(out, "Zx8kq") || !json.Valid([]byte(out)) || counts["SECRET"] == 0 {
			t.Fatalf("%s -> %s %v", in, out, counts)
		}
		if strings.Contains(in, "<b>") && !strings.Contains(out, "<b> & c") {
			t.Fatalf("échappement HTML : %s", out)
		}
		if strings.Contains(in, "12345678901234567890") && !strings.Contains(out, "12345678901234567890") {
			t.Fatalf("nombre altéré : %s", out)
		}
	}
	// Texte non JSON : masquage brut.
	if out, _ := r.RedactJSON("MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"); strings.Contains(out, "Zx8kq") {
		t.Fatal(out)
	}
}
