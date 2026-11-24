package ycode

// Story 42 (C16 — JS runner context): bindExtensionCore must bind ALL nine
// ctx.* actions (production previously bound only IsIdle + Shutdown, so even
// event handlers got no model/usage/systemPrompt/abort), flush pending
// provider registrations through real providerActions (the old empty struct
// silently dropped them), and thread the real tool-call ID into def.Execute.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/coding-agent/src/core/tools"
	llm "ycode/packages/llm/src"
)

// bindCoreTestSession builds a minimal session + runner with bindExtensionCore
// applied (the production wiring point). A real SessionManager backs
// GetContextUsage's branch math.
func bindCoreTestSession(t *testing.T, model llm.Model[llm.Api]) (*AgentSession, *extensions.ExtensionRunner) {
	t.Helper()
	agent := sdk.NewAgent(sdk.AgentOptions{
		InititalState: &sdk.PartialAgentState{Model: &model},
	})
	s := &AgentSession{
		Agent:          agent,
		SessionManager: NewSessionManager("/proj", filepath.Join(t.TempDir(), "sessions")),
	}
	runner := extensions.NewExtensionRunner(nil, extensions.CreateExtensionRuntime(), "/proj", nil, nil)
	s.extensionRunner = runner
	s.bindExtensionCore(runner)
	return s, runner
}

func TestBindExtensionCoreBindsAllContextActions(t *testing.T) {
	model := llm.Model[llm.Api]{ID: "m1", Provider: "p", Api: llm.OpenAIResponses, ContextWindow: 1000}
	s, runner := bindCoreTestSession(t, model)

	ctx := runner.CreateContext()

	// GetModel: previously dead code returned nil unconditionally — ctx.model
	// was always absent. Now the session's model flows through.
	if m, ok := ctx["model"].(llm.Model[llm.Api]); !ok || m.ID != "m1" {
		t.Errorf("model = %v (%T), want m1", ctx["model"], ctx["model"])
	}
	// GetSignal: idle session → nil (the constructor default), non-nil type assert.
	if sig, ok := ctx["signal"].(context.Context); ok && sig != nil {
		t.Errorf("signal = %v, want nil while idle", sig)
	}
	// IsIdle: not streaming → true.
	if v, ok := ctx["isIdle"].(bool); !ok || !v {
		t.Errorf("isIdle = %v, want true", ctx["isIdle"])
	}
	// HasPendingMessages: empty queues → false.
	if v, ok := ctx["hasPendingMessages"].(bool); !ok || v {
		t.Errorf("hasPendingMessages = %v, want false", ctx["hasPendingMessages"])
	}
	// GetContextUsage routes to s.GetContextUsage (real SessionManager math).
	getCU, ok := ctx["getContextUsage"].(func() *extensions.ContextUsage)
	if !ok {
		t.Fatalf("getContextUsage = %T, want func", ctx["getContextUsage"])
	}
	if cu := getCU(); cu == nil || cu.ContextWindow != 1000 {
		t.Errorf("getContextUsage() = %+v, want ContextWindow 1000", cu)
	}
	// GetSystemPrompt routes to s.GetSystemPrompt (empty here, but non-nil).
	getSP, ok := ctx["getSystemPrompt"].(func() string)
	if !ok {
		t.Fatalf("getSystemPrompt = %T, want func", ctx["getSystemPrompt"])
	}
	if sp := getSP(); sp != "" {
		t.Errorf("getSystemPrompt() = %q, want empty", sp)
	}
	// Abort routes to s.Abort() — nil-safe on a minimal session; must not panic.
	abort, ok := ctx["abort"].(func())
	if !ok {
		t.Fatalf("abort = %T, want func", ctx["abort"])
	}
	abort()
	// Compact is bound (fire-and-forget goroutine) — assert non-nil; invoking it
	// would start a real compaction, which the session-level wiring pins by type.
	if _, ok := ctx["compact"].(func(*extensions.CompactOptions) error); !ok {
		t.Fatalf("compact = %T, want func", ctx["compact"])
	}
	_ = s
}

// The old bindExtensionCore passed an EMPTY providerActions struct: queued
// pi.registerProvider calls were silently dropped at bind time. With real
// actions, a pre-bind registration must land in the model registry.
func TestBindExtensionCoreFlushesPendingProviderRegistrations(t *testing.T) {
	dir := t.TempDir()
	registry := NewModelRegistry(NewAuthStorage(filepath.Join(dir, "auth.json")), filepath.Join(dir, "models.json"))
	agent := sdk.NewAgent(sdk.AgentOptions{})
	s := &AgentSession{Agent: agent, modelRegistry: registry}

	rt := extensions.CreateExtensionRuntime()
	rt.PendingProviderRegistrations = append(rt.PendingProviderRegistrations, extensions.ProviderRegistration{
		Name: "acme",
		Config: &extensions.ProviderConfig{
			Name:    "Acme",
			BaseURL: "https://api.acme.com/v1",
			ApiKey:  "sk-test",
			Api:     "openai-completions",
			Models: []*extensions.ProviderModelConfig{
				{ID: "m1", ContextWindow: 4096},
			},
		},
	})
	runner := extensions.NewExtensionRunner(nil, rt, "/proj", nil, registry)
	s.extensionRunner = runner
	s.bindExtensionCore(runner)

	if registry.Find("acme", "m1") == nil {
		t.Fatal("acme/m1 not in registry after bind — pending provider registration was dropped")
	}
}

// buildAgentToolFromDefinition must thread the real tool-call ID through the
// signal context so the JS bridge can pass it as execute's first argument.
func TestBuildAgentToolFromDefinitionThreadsToolCallID(t *testing.T) {
	calledID := ""
	def := extensions.ToolDefinition{
		Name: "probe",
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			calledID = extensions.ToolCallIDFromContext(ctx)
			return &tools.ToolExecuteResult{Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "ok"}}}, nil
		},
	}
	tool := buildAgentToolFromDefinition("probe", ToolDefinitionEntry{Definition: def}, nil)

	res, err := tool.Execute("call-77", map[string]any{}, context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if calledID != "call-77" {
		t.Errorf("def.Execute saw toolCallID %q, want call-77", calledID)
	}
	tc, ok := res.Content[0].(*llm.TextContent)
	if !ok || tc.Text != "ok" {
		t.Fatalf("result = %v", res.Content)
	}
}
