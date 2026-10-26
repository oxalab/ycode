package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// C14: the core wrapper must honor ctxFactory — an ExecuteContext definition
// receives the factory's context instead of silently running context-free
// (pre-fix the ctxFactory param was dead and ExecuteContext was never called
// through this wrapper). Preference order matches the session bridge:
// ExecuteUpdate > ExecuteContext (factory present) > Execute.
func TestWrapToolDefinitionPrefersExecuteContext(t *testing.T) {
	ranExecute := false
	def := &extensions.ToolDefinition{
		Name: "fake",
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			ranExecute = true
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "plain"}}}, nil
		},
		ExecuteContext: func(ctx context.Context, args json.RawMessage, extCtx extensions.ExtensionContext) (any, error) {
			if extCtx.Model.ID != "test-model" {
				return nil, fmt.Errorf("ExecuteContext got model %q, want test-model", extCtx.Model.ID)
			}
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "contextual"}}}, nil
		},
	}
	wrapped := WrapToolDefinition(def, func() extensions.ExtensionContext {
		return extensions.ExtensionContext{Model: llm.Model[llm.Api]{ID: "test-model"}}
	})
	res, err := wrapped.Execute("id", map[string]any{}, context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if ranExecute {
		t.Error("plain Execute ran even though ExecuteContext + factory were available")
	}
	if tc, ok := res.Content[0].(*llm.TextContent); !ok || tc.Text != "contextual" {
		t.Errorf("result = %+v, want ExecuteContext's text", res.Content)
	}
}

// C14: nil factory falls back to plain Execute (context-free), same as before.
func TestWrapToolDefinitionNilFactoryFallsBackToExecute(t *testing.T) {
	ranContext := false
	def := &extensions.ToolDefinition{
		Name: "fake",
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "plain"}}}, nil
		},
		ExecuteContext: func(ctx context.Context, args json.RawMessage, extCtx extensions.ExtensionContext) (any, error) {
			ranContext = true
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "contextual"}}}, nil
		},
	}
	wrapped := WrapToolDefinition(def, nil)
	res, err := wrapped.Execute("id", map[string]any{}, context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if ranContext {
		t.Error("ExecuteContext ran despite nil factory")
	}
	if tc, ok := res.Content[0].(*llm.TextContent); !ok || tc.Text != "plain" {
		t.Errorf("result = %+v, want Execute's text", res.Content)
	}
}

// Order pin: ExecuteUpdate takes precedence over ExecuteContext, mirroring
// buildAgentToolFromDefinition (story 21 ordering, now locked in both bridges).
func TestWrapToolDefinitionExecuteUpdateBeatsContext(t *testing.T) {
	ranContext := false
	def := &extensions.ToolDefinition{
		Name: "fake",
		ExecuteUpdate: func(ctx context.Context, args json.RawMessage, onUpdate func(partial any)) (any, error) {
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "update"}}}, nil
		},
		ExecuteContext: func(ctx context.Context, args json.RawMessage, extCtx extensions.ExtensionContext) (any, error) {
			ranContext = true
			return &ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "contextual"}}}, nil
		},
	}
	wrapped := WrapToolDefinition(def, func() extensions.ExtensionContext {
		return extensions.ExtensionContext{}
	})
	res, err := wrapped.Execute("id", map[string]any{}, context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if ranContext {
		t.Error("ExecuteContext ran despite ExecuteUpdate being set")
	}
	if tc, ok := res.Content[0].(*llm.TextContent); !ok || tc.Text != "update" {
		t.Errorf("result = %+v, want ExecuteUpdate's text", res.Content)
	}
}
