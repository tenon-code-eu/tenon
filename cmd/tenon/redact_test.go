package main

import (
	"encoding/json"
	"strings"
	"testing"

	"codeberg.org/tenon/tenon/internal/policy"
)

func TestRedactJSON(t *testing.T) {
	r, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{
		`{"text":"voici :\nMISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf\nfin","n":12345678901234567890}`,
		`{"args":{"api_key":"Zx8kq2LmNp4Rt6Vw9YbC1dEf"}}`,
		`["a <b> & c", "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"]`,
	} {
		out := redactJSON(r, in)
		if strings.Contains(out, "Zx8kq") || !json.Valid([]byte(out)) {
			t.Fatalf("%s -> %s", in, out)
		}
		if strings.Contains(in, "<b>") && !strings.Contains(out, "<b> & c") {
			t.Fatalf("échappement HTML : %s", out)
		}
		if strings.Contains(in, "12345678901234567890") && !strings.Contains(out, "12345678901234567890") {
			t.Fatalf("nombre altéré : %s", out)
		}
	}
	// Texte non JSON : masquage brut.
	if out := redactJSON(r, "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"); strings.Contains(out, "Zx8kq") {
		t.Fatal(out)
	}
}
