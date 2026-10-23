package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

// C9 plumbing: WrapToolDefinition must forward ExecuteUpdate partials through
// the SDK update callback — pre-fix the wrapper received onUpdate and dropped
// it entirely, so no tool could ever stream.
func TestWrapToolDefinitionForwardsPartials(t *testing.T) {
	def := &extensions.ToolDefinition{
		Name: "fake",
		ExecuteUpdate: func(ctx context.Context, args json.RawMessage, onUpdate func(partial any)) (any, error) {
			onUpdate(&ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "partial-1"}}})
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "final"}}}, nil
		},
	}
	wrapped := WrapToolDefinition(def, nil)

	var mu sync.Mutex
	var partials []string
	res, err := wrapped.Execute("id", map[string]any{"command": "x"}, context.Background(), func(p sdk.AgentToolResult[any]) {
		mu.Lock()
		defer mu.Unlock()
		if tc, ok := p.Content[0].(*llm.TextContent); ok {
			partials = append(partials, tc.Text)
		}
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(partials) != 1 || partials[0] != "partial-1" {
		t.Errorf("partials = %v, want [partial-1]", partials)
	}
	if tc, ok := res.Content[0].(*llm.TextContent); !ok || tc.Text != "final" {
		t.Errorf("final result = %+v, want text final", res.Content)
	}
}

// The plain Execute fallback must still work for definitions without
// ExecuteUpdate (and must not invoke a nil update callback).
func TestWrapToolDefinitionExecuteFallback(t *testing.T) {
	def := &extensions.ToolDefinition{
		Name: "fake",
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "plain"}}}, nil
		},
	}
	wrapped := WrapToolDefinition(def, nil)
	res, err := wrapped.Execute("id", map[string]any{}, context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tc, ok := res.Content[0].(*llm.TextContent); !ok || tc.Text != "plain" {
		t.Errorf("result = %+v, want text plain", res.Content)
	}
}
