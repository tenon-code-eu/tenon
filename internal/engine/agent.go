package engine

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// agentTool est l'outil « agent » : il n'est jamais exécuté par Run, le moteur
// le traite lui-même (runSubagent) pour mener le sous-agent dans un tour imbriqué.
type agentTool struct{}

type agentArgs struct {
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

func (agentTool) Name() string { return "agent" }
func (agentTool) Description() string {
	return "Launch a sub-agent for a self-contained task. It does not see this conversation; it returns its final answer."
}
func (agentTool) Risk() tools.Risk { return tools.Read }
func (agentTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"agent":{"type":"string","description":"Name of the agent to launch (default: general-purpose)"},` +
		`"description":{"type":"string","description":"Short description of the task (3-5 words)"},` +
		`"prompt":{"type":"string","description":"Complete instructions for the sub-agent"}},` +
		`"required":["description","prompt"]}`)
}

func (agentTool) parse(args json.RawMessage) (agentArgs, error) {
	var a agentArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return a, err
	}
	if a.Prompt == "" {
		return a, errors.New("prompt is required")
	}
	if a.Agent == "" {
		a.Agent = agents.GeneralPurpose
	}
	return a, nil
}

func (t agentTool) Target(args json.RawMessage) (tools.Target, error) {
	a, err := t.parse(args)
	if err != nil {
		return tools.Target{}, err
	}
	return tools.Target{Command: a.Agent}, nil
}

func (agentTool) Run(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{IsError: true, Content: "the agent tool is run by the engine"}, nil
}

// runSubagent mène l'appel agent c dans un tour imbriqué : historique local,
// outils de d.Tools (jamais agent) restreints par l'agent, sans deltas. Une
// error renvoyée (annulation, audit) met fin au tour principal.
func (t *turn) runSubagent(c llm.ToolCall) (tools.Result, error) {
	d := t.e.d
	args, err := agentTool{}.parse(c.Args)
	if err != nil {
		return toolError("invalid arguments: %s", err), nil
	}
	a, ok := d.Agents.Get(args.Agent)
	if !ok {
		return toolError("unknown agent: %s", args.Agent), nil
	}
	// Budget épuisé : le sous-agent ne démarre pas, comme un tour.
	if used, max, over := t.s.overBudget(d.Budget); over {
		return toolError("%s", budgetRefused(used, max)), nil
	}
	if err := t.emit(event.SubagentStarted, event.SubagentStartedPayload{
		CallID: c.ID, Agent: a.Name, Description: args.Description,
	}); err != nil {
		return tools.Result{}, err
	}

	reg := d.Tools
	if a.Tools != nil {
		reg = reg.Only(a.Tools)
	}
	_, withSkills := reg.Get("skill")
	h := []llm.Message{
		{Role: llm.System, Content: agents.SystemPrompt(d.Lang, d.BasePrompt, d.SkillsSection, a, withSkills)},
		{Role: llm.User, Content: args.Prompt},
	}
	child := &turn{e: t.e, s: t.s, ctx: t.ctx, h: &h, tools: reg, parent: c.ID, agent: a.Name, purpose: purposeSubagent}
	res, err := child.loop()
	if err != nil {
		if t.ctx.Err() != nil {
			// Au mieux : le contexte est annulé, l'émission peut échouer.
			_ = t.emit(event.SubagentCompleted, event.SubagentCompletedPayload{
				CallID: c.ID, Status: "cancelled", Iterations: res.Iterations,
			})
		}
		return tools.Result{}, err
	}

	status, content := "ok", res.Final
	if res.Reason == "done" && content == "" {
		content = "sub-agent returned no text"
	}
	if res.Reason != "done" {
		status = "error"
		switch {
		case res.ProvErr != nil:
			content = "sub-agent stopped: " + res.ProvErr.Error()
		case res.Reason == "max_iterations":
			content = "sub-agent stopped: iteration limit reached"
		default:
			content = "sub-agent stopped: " + res.Reason
		}
		if res.Text != "" {
			content += "\n\n" + res.Text
		}
	}
	if err := t.emit(event.SubagentCompleted, event.SubagentCompletedPayload{
		CallID: c.ID, Status: status, Iterations: res.Iterations,
	}); err != nil {
		return tools.Result{}, err
	}
	return tools.Result{Content: tools.Truncate(content, d.OutputLimit), IsError: status != "ok"}, nil
}
