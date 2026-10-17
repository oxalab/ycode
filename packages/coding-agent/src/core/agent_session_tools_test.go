package ycode

// B4/B5 wiring: buildAgentToolFromDefinition must prefer ExecuteContext (the
// revived ExtensionContext path — TS passes ctx as the 5th execute param) when
// a context factory is provided, and fall back to plain Execute otherwise.

import (
	"context"
	"encoding/json"
	"testing"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/coding-agent/src/core/tools"
	llm "ycode/packages/llm/src"
)

func contextProbeDef(called *string) extensions.ToolDefinition {
	return extensions.ToolDefinition{
		Name: "probe",
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			*called = "plain"
			return &tools.ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "plain"}}}, nil
		},
		ExecuteContext: func(ctx context.Context, args json.RawMessage, extCtx extensions.ExtensionContext) (any, error) {
			*called = "ctx:" + extCtx.Model.ID
			return &tools.ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "ctx"}}}, nil
		},
	}
}

func probeResultText(t *testing.T, res sdk.AgentToolResult[any]) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*llm.TextContent)
	if !ok {
		t.Fatalf("block is %T", res.Content[0])
	}
	return tc.Text
}

func TestBuildAgentToolFromDefinitionPrefersExecuteContext(t *testing.T) {
	called := ""
	factory := func() extensions.ExtensionContext {
		return extensions.ExtensionContext{Model: llm.Model[llm.Api]{ID: "glm-x"}}
	}
	tool := buildAgentToolFromDefinition("probe", ToolDefinitionEntry{Definition: contextProbeDef(&called)}, factory)

	res, err := tool.Execute("call-1", map[string]any{}, context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called != "ctx:glm-x" {
		t.Errorf("ExecuteContext should run with the factory's model, got %q", called)
	}
	if got := probeResultText(t, res); got != "ctx" {
		t.Errorf("result text = %q, want ctx", got)
	}
}

func TestBuildAgentToolFromDefinitionFallsBackWithoutFactory(t *testing.T) {
	called := ""
	tool := buildAgentToolFromDefinition("probe", ToolDefinitionEntry{Definition: contextProbeDef(&called)}, nil)

	res, err := tool.Execute("call-1", map[string]any{}, context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called != "plain" {
		t.Errorf("nil factory must fall back to plain Execute, got %q", called)
	}
	if got := probeResultText(t, res); got != "plain" {
		t.Errorf("result text = %q, want plain", got)
	}
}
