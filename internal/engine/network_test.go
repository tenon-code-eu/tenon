package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// netShell est un faux outil shell qui note le drapeau réseau de chaque appel
// et le recopie dans Result.Sandbox, comme l'outil réel isolé.
type netShell struct {
	mu  sync.Mutex
	got []bool
}

func (n *netShell) Name() string            { return "shell" }
func (n *netShell) Description() string     { return "faux shell" }
func (n *netShell) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (n *netShell) Risk() tools.Risk        { return tools.Exec }
func (n *netShell) Target(args json.RawMessage) (tools.Target, error) {
	var a struct {
		Command string `json:"command"`
	}
	err := json.Unmarshal(args, &a)
	return tools.Target{Command: a.Command}, err
}

func (n *netShell) Run(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
	on := tools.NetworkFrom(ctx)
	n.mu.Lock()
	n.got = append(n.got, on)
	n.mu.Unlock()
	return tools.Result{Content: "ok", Sandbox: &event.SandboxUse{Confined: true, Network: on}}, nil
}

func (n *netShell) calls() []bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]bool(nil), n.got...)
}

// withNetShell installe le faux shell, les règles données et l'isolation.
func withNetShell(t *testing.T, sh *netShell, rules ...policy.Rule) func(*Deps) {
	return func(d *Deps) {
		perms, err := policy.NewPermissions(d.ProjectRoot, rules)
		if err != nil {
			t.Fatal(err)
		}
		d.Permissions = perms
		d.Tools = tools.NewRegistry(sh)
		d.ShellSandboxed = true
	}
}

func TestNetworkFromRule(t *testing.T) {
	sh := &netShell{}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"npm install"}`),
		call("c2", "shell", `{"command":"npm install; curl x"}`),
		text("fini"),
	}, withNetShell(t, sh, policy.Rule{Tool: "shell", Match: "npm install*", Decision: policy.Allow, Network: true}))
	evs, err := h.submit(t, "s_1", "installe", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} })
	if err != nil {
		t.Fatal(err)
	}
	// c1 : allow avec réseau, sans demande ; c2 : enchaînée, demande et
	// acceptation simple, sans réseau.
	if got := sh.calls(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("réseau par appel = %v, attendu [true false]", got)
	}
	if n := len(ofType(evs, event.PermissionRequested)); n != 1 {
		t.Fatalf("%d demandes, attendu 1", n)
	}
	res := ofType(evs, event.ToolResult)
	if p := decode[event.ToolResultPayload](t, res[0]); p.Sandbox == nil || !p.Sandbox.Confined || !p.Sandbox.Network {
		t.Fatalf("ToolResult.sandbox = %+v", p.Sandbox)
	}
	if p := decode[event.ToolResultPayload](t, res[1]); p.Sandbox == nil || p.Sandbox.Network {
		t.Fatalf("ToolResult.sandbox = %+v", p.Sandbox)
	}
}

func TestNetworkFromReply(t *testing.T) {
	sh := &netShell{}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"curl https://example.eu"}`),
		text("fini"),
	}, withNetShell(t, sh))
	var req event.PermissionRequestedPayload
	evs, err := h.submit(t, "s_1", "télécharge", func(p event.PermissionRequestedPayload) Reply {
		req = p
		return Reply{Allow: true, Network: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !req.Sandboxed {
		t.Fatalf("PermissionRequested.sandboxed absent : %+v", req)
	}
	if got := sh.calls(); len(got) != 1 || !got[0] {
		t.Fatalf("réseau = %v, attendu [true]", got)
	}
	rs := ofType(evs, event.PermissionResolved)
	if len(rs) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.PermissionResolvedPayload](t, rs[0]); !p.Allowed || !p.Network {
		t.Fatalf("PermissionResolved %+v", p)
	}
}

func TestNetworkFromAskRule(t *testing.T) {
	sh := &netShell{}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"curl https://example.eu"}`),
		text("fini"),
	}, withNetShell(t, sh, policy.Rule{Tool: "shell", Match: "curl *", Decision: policy.Ask, Network: true}))
	var req event.PermissionRequestedPayload
	evs, err := h.submit(t, "s_1", "télécharge", func(p event.PermissionRequestedPayload) Reply {
		req = p
		return Reply{Allow: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !req.RuleNetwork || !req.Sandboxed {
		t.Fatalf("PermissionRequested %+v, attendu rule_network et sandboxed", req)
	}
	if got := sh.calls(); len(got) != 1 || !got[0] {
		t.Fatalf("réseau = %v, attendu [true]", got)
	}
	if p := decode[event.PermissionResolvedPayload](t, ofType(evs, event.PermissionResolved)[0]); !p.Network {
		t.Fatalf("PermissionResolved %+v", p)
	}
}

func TestNetworkDeniedReply(t *testing.T) {
	sh := &netShell{}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"curl https://example.eu"}`),
		text("fini"),
	}, withNetShell(t, sh, policy.Rule{Tool: "shell", Match: "curl *", Decision: policy.Ask, Network: true}))
	evs, err := h.submit(t, "s_1", "télécharge", func(event.PermissionRequestedPayload) Reply { return Reply{Network: true} })
	if err != nil {
		t.Fatal(err)
	}
	if got := sh.calls(); len(got) != 0 {
		t.Fatalf("outil lancé malgré le refus : %v", got)
	}
	if p := decode[event.PermissionResolvedPayload](t, ofType(evs, event.PermissionResolved)[0]); p.Allowed || p.Network {
		t.Fatalf("PermissionResolved %+v", p)
	}
}

func TestRememberNoNetwork(t *testing.T) {
	sh := &netShell{}
	cmd := `{"command":"curl https://example.eu"}`
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", cmd),
		call("c2", "shell", cmd),
		text("fini"),
	}, withNetShell(t, sh))
	evs, err := h.submit(t, "s_1", "deux fois", func(event.PermissionRequestedPayload) Reply {
		return Reply{Allow: true, Remember: true, Network: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.PermissionRequested)); n != 1 {
		t.Fatalf("%d demandes, attendu 1", n)
	}
	// La réponse vaut pour c1 ; c2, approuvé par la mémoire, reste sans réseau.
	if got := sh.calls(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("réseau par appel = %v, attendu [true false]", got)
	}
}

// R22 : une approbation mémorisée d'une règle ask avec network = true garde
// le réseau de la règle aux appels suivants (la règle l'accorde, pas la
// réponse).
func TestRememberRuleNetwork(t *testing.T) {
	sh := &netShell{}
	cmd := `{"command":"curl https://example.eu"}`
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", cmd),
		call("c2", "shell", cmd),
		text("fini"),
	}, withNetShell(t, sh, policy.Rule{Tool: "shell", Match: "curl *", Decision: policy.Ask, Network: true}))
	evs, err := h.submit(t, "s_1", "deux fois", func(event.PermissionRequestedPayload) Reply {
		return Reply{Allow: true, Remember: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.PermissionRequested)); n != 1 {
		t.Fatalf("%d demandes, attendu 1", n)
	}
	if got := sh.calls(); len(got) != 2 || !got[0] || !got[1] {
		t.Fatalf("réseau par appel = %v, attendu [true true]", got)
	}
}

func TestSandboxedOnlyForShell(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
		text("fini"),
	}, func(d *Deps) { d.ShellSandboxed = true })
	var req event.PermissionRequestedPayload
	if _, err := h.submit(t, "s_1", "écris", func(p event.PermissionRequestedPayload) Reply {
		req = p
		return Reply{Allow: true}
	}); err != nil {
		t.Fatal(err)
	}
	if req.Tool != "write_file" || req.Sandboxed {
		t.Fatalf("PermissionRequested %+v", req)
	}

	// Isolation inactive : pas de Sandboxed pour le shell.
	sh := &netShell{}
	h = newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"ls"}`),
		text("fini"),
	}, withNetShell(t, sh), func(d *Deps) { d.ShellSandboxed = false })
	req = event.PermissionRequestedPayload{}
	if _, err := h.submit(t, "s_1", "liste", func(p event.PermissionRequestedPayload) Reply {
		req = p
		return Reply{Allow: true}
	}); err != nil {
		t.Fatal(err)
	}
	if req.Tool != "shell" || req.Sandboxed {
		t.Fatalf("PermissionRequested %+v", req)
	}
}

func TestNetworkInSubagent(t *testing.T) {
	sh := &netShell{}
	h := newAgentEngine(t, [][]llm.Chunk{
		call("a1", "agent", agentCall),
		call("s1", "shell", `{"command":"npm install"}`),
		text("sous-agent fini"),
		text("fini"),
	}, nil, withNetShell(t, sh, policy.Rule{Tool: "shell", Match: "npm install*", Decision: policy.Allow, Network: true}))
	evs, err := h.submit(t, "s_1", "délègue", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := sh.calls(); len(got) != 1 || !got[0] {
		t.Fatalf("réseau dans le sous-agent = %v, attendu [true] ; events %v", got, types(evs))
	}
}

func TestRuleNetworkHookForcedPrompt(t *testing.T) {
	sh := &netShell{}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"npm install"}`),
		text("fini"),
	}, withNetShell(t, sh, policy.Rule{Tool: "shell", Match: "npm install*", Decision: policy.Allow, Network: true}),
		withHooks(t, hk{"PreToolUse", "Bash", jsonHook(t, askOut)}))
	var req event.PermissionRequestedPayload
	if _, err := h.submit(t, "s_1", "installe", func(p event.PermissionRequestedPayload) Reply {
		req = p
		return Reply{Allow: true}
	}); err != nil {
		t.Fatal(err)
	}
	if req.Tool != "shell" || !req.RuleNetwork || req.HookReason == "" {
		t.Fatalf("PermissionRequested %+v, attendu rule_network et hook_reason", req)
	}
	if got := sh.calls(); len(got) != 1 || !got[0] {
		t.Fatalf("réseau = %v, attendu [true]", got)
	}
}

func TestRuleNetworkAbsentByDefault(t *testing.T) {
	sh := &netShell{}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"ls"}`),
		text("fini"),
	}, withNetShell(t, sh))
	var req event.PermissionRequestedPayload
	if _, err := h.submit(t, "s_1", "liste", func(p event.PermissionRequestedPayload) Reply {
		req = p
		return Reply{Allow: true}
	}); err != nil {
		t.Fatal(err)
	}
	if req.RuleNetwork {
		t.Fatalf("PermissionRequested %+v : rule_network inattendu", req)
	}
}
