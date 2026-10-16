package skills

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	plain := "---\nname: review\ndescription: Review code\n---\nBody\n"
	cases := []struct {
		nom  string
		in   string
		want frontmatter
		body string
	}{
		{"simple", plain, frontmatter{Name: "review", Description: "Review code"}, "Body\n"},
		{"crlf", strings.ReplaceAll(plain, "\n", "\r\n"), frontmatter{Name: "review", Description: "Review code"}, "Body\n"},
		{"guillemets doubles", "---\nname: a\ndescription: \"a: b\"\n---\n", frontmatter{Name: "a", Description: "a: b"}, ""},
		{"guillemets simples", "---\nname: a\ndescription: 'it''s'\n---\n", frontmatter{Name: "a", Description: "it's"}, ""},
		{"plié", "---\nname: a\ndescription: >\n  line one\n  line two\n---\nB", frontmatter{Name: "a", Description: "line one line two"}, "B"},
		{"littéral", "---\nname: a\ndescription: |\n  line one\n  line two\n---\nB", frontmatter{Name: "a", Description: "line one\nline two"}, "B"},
		{"outils virgules", "---\nname: a\ndescription: d\nallowed-tools: Read, Grep\n---\n", frontmatter{Name: "a", Description: "d", AllowedTools: []string{"Read", "Grep"}}, ""},
		{"outils inline", "---\nname: a\ndescription: d\nallowed-tools: [Read, \"Bash(git:*)\"]\n---\n", frontmatter{Name: "a", Description: "d", AllowedTools: []string{"Read", "Bash(git:*)"}}, ""},
		{"outils bloc", "---\nname: a\ndescription: d\nallowed-tools:\n  - Read\n  - Grep\n---\n", frontmatter{Name: "a", Description: "d", AllowedTools: []string{"Read", "Grep"}}, ""},
		{"outils bloc sans indentation", "---\nname: a\ndescription: d\nallowed-tools:\n- Read\n- Grep\nlicense: MIT\n---\n", frontmatter{Name: "a", Description: "d", AllowedTools: []string{"Read", "Grep"}}, ""},
		{"booléen", "---\nname: a\ndescription: d\ndisable-model-invocation: true\n---\n", frontmatter{Name: "a", Description: "d", DisableModelInvocation: true}, ""},
		{"table imbriquée sautée", "---\nname: a\nmetadata:\n  author: x\n  tags: [a]\ndescription: d\n---\n", frontmatter{Name: "a", Description: "d"}, ""},
		{"clé inconnue", "---\nname: a\nlicense: MIT\ndescription: d\n---\n", frontmatter{Name: "a", Description: "d"}, ""},
	}
	for _, c := range cases {
		t.Run(c.nom, func(t *testing.T) {
			fm, body, err := parseFrontmatter([]byte(c.in))
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if !reflect.DeepEqual(fm, c.want) {
				t.Errorf("fm = %#v, attendu %#v", fm, c.want)
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
			if _, _, err := parseFrontmatter([]byte(in)); err == nil {
				t.Fatal("erreur attendue")
			}
		})
	}
}
