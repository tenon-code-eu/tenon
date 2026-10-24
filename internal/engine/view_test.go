package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// readTurn renvoie un tour utilisateur : message u<i>, appel read_file c<i>
// (arguments args), son résultat content et une réponse "ok".
func readTurn(i int, args, content string) []llm.Message {
	id := fmt.Sprintf("c%d", i)
	return []llm.Message{
		{Role: llm.User, Content: fmt.Sprintf("u%d", i)},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_file", Args: json.RawMessage(args)}}},
		{Role: llm.Tool, ToolCallID: id, Content: content},
		{Role: llm.Assistant, Content: "ok"},
	}
}

// bigResult : 40 lignes de 100 runes (1000 jetons estimés).
func bigResult(i int) string {
	return strings.Repeat(fmt.Sprintf("%d", i%10)+runes(98)+"\n", 40)
}

func viewHistory(turns ...[]llm.Message) []llm.Message {
	h := []llm.Message{{Role: llm.System, Content: "sys"}}
	for _, t := range turns {
		h = append(h, t...)
	}
	return h
}

func cloneHistory(h []llm.Message) []llm.Message {
	out := make([]llm.Message, len(h))
	for i, m := range h {
		m.ToolCalls = append([]llm.ToolCall(nil), m.ToolCalls...)
		for j := range m.ToolCalls {
			m.ToolCalls[j].Args = append(json.RawMessage(nil), m.ToolCalls[j].Args...)
		}
		out[i] = m
	}
	return out
}

// toolContent renvoie le contenu du résultat de l'appel id dans msgs.
func toolContent(t *testing.T, msgs []llm.Message, id string) string {
	t.Helper()
	for _, m := range msgs {
		if m.Role == llm.Tool && m.ToolCallID == id {
			return m.Content
		}
	}
	t.Fatalf("aucun résultat %s", id)
	return ""
}

func sha12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func TestViewDedupKeepsLatest(t *testing.T) {
	content := bigResult(1)
	h := viewHistory(
		readTurn(1, `{"path":"a"}`, content),
		readTurn(2, `{"path":"a"}`, content),
		readTurn(3, `{"path":"a"}`, content),
	)
	out, sh := view(h, nil, 1_000_000, nil)
	want := "[same content as the read at turn 3]"
	if c := toolContent(t, out, "c1"); c != want {
		t.Fatalf("c1 %q", c)
	}
	if c := toolContent(t, out, "c2"); c != want {
		t.Fatalf("c2 %q", c)
	}
	if c := toolContent(t, out, "c3"); c != content {
		t.Fatalf("c3 modifié %q", c)
	}
	if !reflect.DeepEqual(sh.Deduplicated, []string{"c1", "c2"}) || len(sh.Masked) != 0 {
		t.Fatalf("shaped %+v", sh)
	}
	if want := estimate(h, nil) - estimate(out, nil); sh.SavedTokens != want || want <= 0 {
		t.Fatalf("saved %d, attendu %d", sh.SavedTokens, want)
	}
}

func TestViewDedupNeedsSameArgs(t *testing.T) {
	content := bigResult(1)
	h := viewHistory(
		// Mêmes octets, fichiers différents : jamais dédupliqués.
		readTurn(1, `{"path":"a"}`, content),
		readTurn(2, `{"path":"b"}`, content),
		// Mêmes arguments, contenu différent.
		readTurn(3, `{"path":"c"}`, content),
		readTurn(4, `{"path":"c"}`, bigResult(2)),
	)
	// Même outil ? Non : un autre outil, mêmes arguments et contenu.
	h = append(h,
		llm.Message{Role: llm.User, Content: "u5"},
		llm.Message{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "c5", Name: "grep", Args: json.RawMessage(`{"path":"b"}`)}}},
		llm.Message{Role: llm.Tool, ToolCallID: "c5", Content: content},
	)
	out, sh := view(h, nil, 1_000_000, nil)
	if !reflect.DeepEqual(out, h) || len(sh.Deduplicated) != 0 || len(sh.Masked) != 0 || sh.SavedTokens != 0 {
		t.Fatalf("vue modifiée : %+v", sh)
	}
}

func TestViewMaskOnlyAbove50(t *testing.T) {
	var turns [][]llm.Message
	for i := 1; i <= 6; i++ {
		turns = append(turns, readTurn(i, fmt.Sprintf(`{"path":"f%d"}`, i), bigResult(i)))
	}
	h := viewHistory(turns...)
	est := estimate(h, nil)
	// Exactement 50 % : pas au-delà, rien n'est masqué.
	out, sh := view(h, nil, 2*est, nil)
	if !reflect.DeepEqual(out, h) || len(sh.Masked) != 0 || sh.SavedTokens != 0 {
		t.Fatalf("masqué à 50 %% : %+v", sh)
	}
	// Juste au-delà : masquage.
	if _, sh := view(h, nil, 2*est-2, nil); len(sh.Masked) == 0 {
		t.Fatal("rien masqué au-delà de 50 %")
	}
}

func TestViewMaskOldestUntil40(t *testing.T) {
	longArgs := `{"path":"` + strings.Repeat("d/", 40) + `f1"}`
	turns := [][]llm.Message{readTurn(1, longArgs, bigResult(1))}
	for i := 2; i <= 6; i++ {
		turns = append(turns, readTurn(i, fmt.Sprintf(`{"path":"f%d"}`, i), bigResult(i)))
	}
	h := viewHistory(turns...)
	const w = 10500
	if est := estimate(h, nil); 2*est <= w {
		t.Fatalf("préalable : estimation %d pas au-delà de 50 %%", est)
	}
	out, sh := view(h, nil, w, nil)
	if !reflect.DeepEqual(sh.Masked, []string{"c1", "c2"}) || len(sh.Deduplicated) != 0 {
		t.Fatalf("shaped %+v", sh)
	}
	after := estimate(out, nil)
	if 10*after >= 4*w {
		t.Fatalf("estimation après masquage %d pas sous 40 %%", after)
	}
	// Un seul masquage ne suffisait pas : le second était nécessaire.
	if c := toolContent(t, out, "c3"); c != bigResult(3) {
		t.Fatalf("c3 masqué alors que 40 %% était atteint")
	}
	abridged := string([]rune(longArgs)[:60]) + "…"
	want1 := "[output hidden to save context: read_file " + abridged + ", 40 lines, sha256 " + sha12(bigResult(1)) + "; run the tool again if you need it]"
	if c := toolContent(t, out, "c1"); c != want1 {
		t.Fatalf("talon c1\n%q\nattendu\n%q", c, want1)
	}
	want2 := `[output hidden to save context: read_file {"path":"f2"}, 40 lines, sha256 ` + sha12(bigResult(2)) + "; run the tool again if you need it]"
	if c := toolContent(t, out, "c2"); c != want2 {
		t.Fatalf("talon c2 %q", c)
	}
	if sh.SavedTokens != estimate(h, nil)-after {
		t.Fatalf("saved %d", sh.SavedTokens)
	}
}

func TestViewSparesRecentAndSummary(t *testing.T) {
	h := []llm.Message{
		{Role: llm.System, Content: "sys"},
		summaryMessage(runes(4000), ""),
	}
	for i := 1; i <= 4; i++ {
		tt := readTurn(i, fmt.Sprintf(`{"path":"f%d"}`, i), bigResult(i))
		tt[3].Content = runes(4000) // texte d'assistant volumineux
		h = append(h, tt...)
	}
	h[len(h)-4].Content = runes(4000) // message utilisateur volumineux
	out, sh := view(h, nil, 100, nil)
	// 3 dernières itérations : c2, c3, c4 ; seul c1 est éligible.
	if !reflect.DeepEqual(sh.Masked, []string{"c1"}) {
		t.Fatalf("masked %v", sh.Masked)
	}
	for i, m := range out {
		if m.Role == llm.Tool && m.ToolCallID == "c1" {
			continue
		}
		if !reflect.DeepEqual(m, h[i]) {
			t.Fatalf("message %d modifié : %+v", i, m)
		}
	}
}

func TestViewNothingEligible(t *testing.T) {
	var turns [][]llm.Message
	for i := 1; i <= 3; i++ {
		turns = append(turns, readTurn(i, fmt.Sprintf(`{"path":"f%d"}`, i), bigResult(i)))
	}
	h := viewHistory(turns...)
	out, sh := view(h, nil, 10, nil)
	if !reflect.DeepEqual(out, h) || len(sh.Masked) != 0 {
		t.Fatalf("vue modifiée : %+v", sh)
	}
	// Un tour de plus : c1 seul est masqué, la vue reste au-dessus de 40 %.
	h = append(h, readTurn(4, `{"path":"f4"}`, bigResult(4))...)
	out, sh = view(h, nil, 10, nil)
	if !reflect.DeepEqual(sh.Masked, []string{"c1"}) {
		t.Fatalf("masked %v", sh.Masked)
	}
	if c := toolContent(t, out, "c2"); c != bigResult(2) {
		t.Fatal("c2 masqué")
	}
	// Résultat plus court que son talon : jamais masqué (aucun gain).
	h = viewHistory(readTurn(1, `{"path":"p"}`, "x"), readTurn(2, `{"path":"q"}`, "y"),
		readTurn(3, `{"path":"r"}`, "z"), readTurn(4, `{"path":"s"}`, "w"))
	if out, sh := view(h, nil, 1, nil); !reflect.DeepEqual(out, h) || len(sh.Masked) != 0 {
		t.Fatalf("petit résultat masqué : %+v", sh)
	}
}

func TestViewPure(t *testing.T) {
	content := bigResult(1)
	var turns [][]llm.Message
	for i := 1; i <= 5; i++ {
		turns = append(turns, readTurn(i, `{"path":"a"}`, content))
	}
	turns = append(turns, readTurn(6, `{"path":"b"}`, bigResult(6)))
	h := viewHistory(turns...)
	orig := cloneHistory(h)
	out1, sh1 := view(h, nil, 3000, nil)
	out2, sh2 := view(h, nil, 3000, nil)
	if !reflect.DeepEqual(h, orig) {
		t.Fatal("historique modifié")
	}
	if !reflect.DeepEqual(out1, out2) || !reflect.DeepEqual(sh1, sh2) {
		t.Fatal("vue non déterministe")
	}
	if len(sh1.Deduplicated) == 0 {
		t.Fatalf("préalable : rien dédupliqué %+v", sh1)
	}
	out1[3].Content = "changé"
	if !reflect.DeepEqual(h, orig) {
		t.Fatal("la vue partage la tranche de l'historique")
	}
}

func TestContextShapedOnChange(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		call("c2", "read_file", `{"path":"a.txt"}`),
		text("vu"),
		text("encore"),
		call("c3", "read_file", `{"path":"a.txt"}`),
		text("fin"),
	})
	if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte(strings.Repeat(runes(80)+"\n", 30)), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "lis deux fois", nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := ofType(evs, event.ContextShaped)
	if len(cs) != 1 {
		t.Fatalf("ContextShaped x%d, events %v", len(cs), types(evs))
	}
	p := decode[event.ContextShapedPayload](t, cs[0])
	if !reflect.DeepEqual(p.Deduplicated, []string{"c1"}) || len(p.Masked) != 0 || p.SavedTokens <= 0 {
		t.Fatalf("payload %+v", p)
	}
	reqs := h.prov.reqs()
	if c := toolContent(t, reqs[2].Messages, "c1"); c != "[same content as the read at turn 1]" {
		t.Fatalf("requête : c1 %q", c)
	}
	// L'historique de la session garde le contenu complet.
	hist := h.eng.session("s_1").history
	if c := toolContent(t, hist, "c1"); c != toolContent(t, hist, "c2") {
		t.Fatalf("historique modifié : %q", c)
	}

	// Tour suivant, mêmes ensembles : aucun nouvel événement.
	evs, err = h.submit(t, "s_1", "et alors", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.ContextShaped)); n != 0 {
		t.Fatalf("ContextShaped x%d sans changement", n)
	}

	// Nouvelle lecture identique : l'ensemble change, nouvel événement.
	evs, err = h.submit(t, "s_1", "relis", nil)
	if err != nil {
		t.Fatal(err)
	}
	cs = ofType(evs, event.ContextShaped)
	if len(cs) != 1 {
		t.Fatalf("ContextShaped x%d après changement", len(cs))
	}
	if p := decode[event.ContextShapedPayload](t, cs[0]); !reflect.DeepEqual(p.Deduplicated, []string{"c1", "c2"}) {
		t.Fatalf("payload %+v", p)
	}
	if c := toolContent(t, h.prov.reqs()[5].Messages, "c2"); c != "[same content as the read at turn 3]" {
		t.Fatalf("requête : c2 %q", c)
	}
}

// iterations renvoie n itérations d'un même tour : appel read_file c<i> puis
// son résultat volumineux.
func iterations(n int) []llm.Message {
	var out []llm.Message
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("c%d", i)
		out = append(out,
			llm.Message{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_file", Args: json.RawMessage(fmt.Sprintf(`{"path":"f%d"}`, i))}}},
			llm.Message{Role: llm.Tool, ToolCallID: id, Content: bigResult(i)})
	}
	return out
}

func TestViewSingleTurnIterations(t *testing.T) {
	h := viewHistory([]llm.Message{{Role: llm.User, Content: "fais tout"}}, iterations(10),
		[]llm.Message{{Role: llm.Assistant, Content: "fini"}})
	out, sh := view(h, nil, 6000, nil)
	// Au-delà de 50 % : les plus anciens sont masqués jusqu'à 40 %
	// (2400 jetons), les 3 dernières itérations (c8, c9, c10) jamais.
	if !reflect.DeepEqual(sh.Masked, []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7"}) {
		t.Fatalf("masked %v", sh.Masked)
	}
	for i := 8; i <= 10; i++ {
		if c := toolContent(t, out, fmt.Sprintf("c%d", i)); c != bigResult(i) {
			t.Fatalf("c%d masqué : %.40q", i, c)
		}
	}
	// Fenêtre moins serrée : arrêt sous 40 % dès c4 masqué.
	_, sh = view(h, nil, 16000, nil)
	if !reflect.DeepEqual(sh.Masked, []string{"c1", "c2", "c3", "c4"}) {
		t.Fatalf("masked (16000) %v", sh.Masked)
	}
}

func TestViewSubagentHistory(t *testing.T) {
	// Historique d'un sous-agent : prompt système, un seul message user, des
	// itérations dont une à deux appels parallèles.
	h := []llm.Message{{Role: llm.System, Content: "agent"}, {Role: llm.User, Content: "tâche"}}
	h = append(h, iterations(4)...)
	h = append(h,
		llm.Message{Role: llm.Assistant, Content: "deux lectures", ToolCalls: []llm.ToolCall{
			{ID: "p1", Name: "read_file", Args: json.RawMessage(`{"path":"p1"}`)},
			{ID: "p2", Name: "read_file", Args: json.RawMessage(`{"path":"p2"}`)}}},
		llm.Message{Role: llm.Tool, ToolCallID: "p1", Content: bigResult(7)},
		llm.Message{Role: llm.Tool, ToolCallID: "p2", Content: bigResult(8)},
	)
	out, sh := view(h, nil, 100, nil)
	// 3 dernières itérations : c3, c4, (p1, p2) ; c1 et c2 masqués.
	if !reflect.DeepEqual(sh.Masked, []string{"c1", "c2"}) {
		t.Fatalf("masked %v", sh.Masked)
	}
	for _, id := range []string{"c3", "c4", "p1", "p2"} {
		if c := toolContent(t, out, id); strings.HasPrefix(c, "[output hidden") {
			t.Fatalf("%s masqué", id)
		}
	}
	if out[len(out)-3].Content != "deux lectures" || out[1].Content != "tâche" {
		t.Fatal("texte modifié")
	}
}

func TestContextShapedIgnoresPromptHook(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		call("c2", "read_file", `{"path":"a.txt"}`),
		call("c3", "write_file", `{"path":"b.txt","content":"x"}`),
		text(`{"ok":true}`),
		text("fin"),
	}, withHooks(t, hk{"PreToolUse", "Write", map[string]any{"type": "prompt", "prompt": "Judge: $ARGUMENTS"}}))
	if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte(strings.Repeat(runes(80)+"\n", 30)), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "lis puis écris", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 5 {
		t.Fatalf("%d requêtes", n)
	}
	// Un seul événement : la déduplication de c1 ; ni la requête du hook
	// prompt ni l'envoi suivant n'en émettent.
	cs := ofType(evs, event.ContextShaped)
	if len(cs) != 1 {
		t.Fatalf("ContextShaped x%d, events %v", len(cs), types(evs))
	}
	if p := decode[event.ContextShapedPayload](t, cs[0]); !reflect.DeepEqual(p.Deduplicated, []string{"c1"}) {
		t.Fatalf("payload %+v", p)
	}
	if s := h.eng.session("s_1").shaped; !reflect.DeepEqual(s.Deduplicated, []string{"c1"}) {
		t.Fatalf("état de la session %+v", s)
	}
}

// Ruling R12 : le masquage est collant. L'ensemble masqué précédent est
// réappliqué ; il n'est étendu (jusqu'à 40 %) qu'au-delà de 50 %.
func TestViewStickyMask(t *testing.T) {
	var turns [][]llm.Message
	for i := 1; i <= 6; i++ {
		turns = append(turns, readTurn(i, fmt.Sprintf(`{"path":"f%d"}`, i), bigResult(i)))
	}
	const w = 10500
	_, sh := view(viewHistory(turns...), nil, w, nil)
	if !reflect.DeepEqual(sh.Masked, []string{"c1", "c2"}) {
		t.Fatalf("préalable : masked %v", sh.Masked)
	}
	// Un tour de plus : la vue avec {c1, c2} est entre 40 et 50 % ; rien de
	// nouveau n'est masqué (sans l'ensemble précédent, c3 le serait).
	turns = append(turns, readTurn(7, `{"path":"f7"}`, bigResult(7)))
	h := viewHistory(turns...)
	out, sh2 := view(h, nil, w, sh.Masked)
	if !reflect.DeepEqual(sh2.Masked, []string{"c1", "c2"}) {
		t.Fatalf("masked %v", sh2.Masked)
	}
	if e := estimate(out, nil); 10*e < 4*w || 10*e > 5*w {
		t.Fatalf("préalable : vue %d hors de 40-50 %%", e)
	}
	if _, fresh := view(h, nil, w, nil); reflect.DeepEqual(fresh.Masked, sh2.Masked) {
		t.Fatalf("préalable : sans ensemble précédent, masked %v", fresh.Masked)
	}
	// Encore un tour : au-delà de 50 %, extension jusqu'à 40 %.
	turns = append(turns, readTurn(8, `{"path":"f8"}`, bigResult(8)))
	h = viewHistory(turns...)
	out, sh3 := view(h, nil, w, sh2.Masked)
	if !reflect.DeepEqual(sh3.Masked[:2], []string{"c1", "c2"}) || len(sh3.Masked) <= 2 {
		t.Fatalf("masked %v", sh3.Masked)
	}
	if e := estimate(out, nil); 10*e >= 4*w {
		t.Fatalf("vue %d pas sous 40 %%", e)
	}
	// Un call_id inconnu ou protégé de l'ensemble précédent est ignoré.
	if _, sh4 := view(h, nil, 1_000_000, []string{"zz", "c8"}); len(sh4.Masked) != 0 {
		t.Fatalf("masked %v", sh4.Masked)
	}
}

// Ruling R13 : les résultats de l'outil agent ne sont jamais masqués ; la
// déduplication s'applique.
func TestViewNeverMasksAgent(t *testing.T) {
	agentTurn := func(i int, content string) []llm.Message {
		tt := readTurn(i, `{"description":"d","prompt":"p"}`, content)
		tt[1].ToolCalls[0].Name = "agent"
		return tt
	}
	h := viewHistory(agentTurn(1, bigResult(1)), agentTurn(2, bigResult(1)), agentTurn(3, bigResult(3)))
	for i := 4; i <= 7; i++ {
		h = append(h, readTurn(i, fmt.Sprintf(`{"path":"f%d"}`, i), bigResult(i))...)
	}
	out, sh := view(h, nil, 100, []string{"c3"})
	if !reflect.DeepEqual(sh.Deduplicated, []string{"c1"}) {
		t.Fatalf("deduplicated %v", sh.Deduplicated)
	}
	if !reflect.DeepEqual(sh.Masked, []string{"c4"}) {
		t.Fatalf("masked %v", sh.Masked)
	}
	for _, id := range []string{"c2", "c3"} {
		if c := toolContent(t, out, id); strings.HasPrefix(c, "[output hidden") {
			t.Fatalf("%s masqué", id)
		}
	}
}
