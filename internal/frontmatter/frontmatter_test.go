package frontmatter

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	plain := "---\nname: review\ndescription: Review code\n---\nBody\n"
	type want struct {
		Name, Description string
		AllowedTools      []string
		DisableModel      bool
	}
	cases := []struct {
		nom  string
		in   string
		want want
		body string
	}{
		{"simple", plain, want{Name: "review", Description: "Review code"}, "Body\n"},
		{"crlf", strings.ReplaceAll(plain, "\n", "\r\n"), want{Name: "review", Description: "Review code"}, "Body\n"},
		{"guillemets doubles", "---\nname: a\ndescription: \"a: b\"\n---\n", want{Name: "a", Description: "a: b"}, ""},
		{"guillemets simples", "---\nname: a\ndescription: 'it''s'\n---\n", want{Name: "a", Description: "it's"}, ""},
		{"plié", "---\nname: a\ndescription: >\n  line one\n  line two\n---\nB", want{Name: "a", Description: "line one line two"}, "B"},
		{"littéral", "---\nname: a\ndescription: |\n  line one\n  line two\n---\nB", want{Name: "a", Description: "line one\nline two"}, "B"},
		{"plain multi-ligne", "---\nname: a\ndescription: Foo\n  bar\n    baz\n---\n", want{Name: "a", Description: "Foo bar baz"}, ""},
		{"valeur sur la ligne suivante", "---\nname: a\ndescription:\n  text one\n  text two\n---\n", want{Name: "a", Description: "text one text two"}, ""},
		{"guillemets multi-ligne", "---\nname: a\ndescription: \"Foo: x\n  bar\"\n---\n", want{Name: "a", Description: "Foo: x bar"}, ""},
		{"outils virgules", "---\nname: a\ndescription: d\nallowed-tools: Read, Grep\n---\n", want{Name: "a", Description: "d", AllowedTools: []string{"Read", "Grep"}}, ""},
		{"outils inline", "---\nname: a\ndescription: d\nallowed-tools: [Read, \"Bash(git:*)\"]\n---\n", want{Name: "a", Description: "d", AllowedTools: []string{"Read", "Bash(git:*)"}}, ""},
		{"outils bloc", "---\nname: a\ndescription: d\nallowed-tools:\n  - Read\n  - Grep\n---\n", want{Name: "a", Description: "d", AllowedTools: []string{"Read", "Grep"}}, ""},
		{"outils bloc sans indentation", "---\nname: a\ndescription: d\nallowed-tools:\n- Read\n- Grep\nlicense: MIT\n---\n", want{Name: "a", Description: "d", AllowedTools: []string{"Read", "Grep"}}, ""},
		{"booléen", "---\nname: a\ndescription: d\ndisable-model-invocation: true\n---\n", want{Name: "a", Description: "d", DisableModel: true}, ""},
		{"table imbriquée sautée", "---\nname: a\nmetadata:\n  author: x\n  tags: [a]\ndescription: d\n---\n", want{Name: "a", Description: "d"}, ""},
		{"clé inconnue", "---\nname: a\nlicense: MIT\ndescription: d\n---\n", want{Name: "a", Description: "d"}, ""},
	}
	for _, c := range cases {
		t.Run(c.nom, func(t *testing.T) {
			fm, body, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			got := want{fm.Name(), fm.Description(), fm.List("allowed-tools"), fm.Bool("disable-model-invocation")}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("fm = %#v, attendu %#v", got, c.want)
			}
			if body != c.body {
				t.Errorf("body = %q, attendu %q", body, c.body)
			}
		})
	}
}

func TestParseFrontmatterErreurs(t *testing.T) {
	longue := "---\nname: a\ndescription: " + strings.Repeat("x", 8192) + "\n---\n"
	lignes := "---\nname: a\ndescription: d\n" + strings.Repeat("k: v\n", 200) + "---\n"
	cases := map[string]string{
		"sans ouverture":        "name: a\ndescription: d\n",
		"non fermé":             "---\nname: a\ndescription: d\n",
		"trop gros":             longue,
		"trop de lignes":        lignes,
		"name manquant":         "---\ndescription: d\n---\n",
		"description manquante": "---\nname: a\n---\n",
	}
	for nom, in := range cases {
		t.Run(nom, func(t *testing.T) {
			if _, _, err := Parse([]byte(in)); err == nil {
				t.Fatal("erreur attendue")
			}
		})
	}
}

func TestHas(t *testing.T) {
	f, _, err := Parse([]byte("---\nname: a\ndescription: b\ntools:\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Has("tools") {
		t.Error("Has(tools) devrait être vrai (clé présente, valeur vide)")
	}
	if got := f.List("tools"); got != nil {
		t.Errorf("List(tools) = %#v, attendu nil", got)
	}
	if f.Has("model") {
		t.Error("Has(model) devrait être faux")
	}
}
