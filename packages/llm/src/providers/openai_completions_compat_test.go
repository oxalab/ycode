package providers

import (
	"testing"

	src "ycode/packages/llm/src"
)

// Agent-loop secondary 5b (docs/agent-loop-context-bug.md §5b): per-model
// compat from models.json must overlay URL/provider detection (the reference's
// getCompat), and the zai/qwen enable_thinking gate must follow the
// truthiness of the reasoning effort (the loop sends "" for thinking-off, so a
// non-nil check alone forced enable_thinking: true forever).

func compatBool(v bool) *bool { return &v }

func compatStr(v string) *string { return &v }

func compatTestModel(baseURL string, reasoning bool, compat *src.OpenAICompletionsCompat) src.Model[src.Api] {
	return src.Model[src.Api]{
		ID: "glm-test", Name: "GLM Test", Api: src.Api("openai-completions"),
		Provider: src.Provider("zai"), BaseURL: baseURL,
		Reasoning: reasoning,
		Compat:    src.Compat{OpenAICompletions: compat},
	}
}

func compatTestParams(t *testing.T, model src.Model[src.Api], opts *OpenAICompletionsOptions) map[string]interface{} {
	t.Helper()
	compat := resolveOpenAICompat(model)
	params, ok := buildOpenAICompletionsParams(model, src.ChatContext{Messages: []src.Message{}}, opts, compat).(map[string]interface{})
	if !ok {
		t.Fatalf("params is not a map: %T", buildOpenAICompletionsParams(model, src.ChatContext{Messages: []src.Message{}}, opts, compat))
	}
	return params
}

func TestResolveOpenAICompatAppliesModelOverrides(t *testing.T) {
	model := compatTestModel("https://api.z.ai/api/paas/v4", true, &src.OpenAICompletionsCompat{
		ZaiToolStream:  compatBool(true),
		MaxTokensField: compatStr("max_tokens"),
	})

	compat := resolveOpenAICompat(model)
	if !compat.ZaiToolStream {
		t.Error("model compat zaiToolStream=true should override detection (false)")
	}
	if compat.MaxTokensField != "max_tokens" {
		t.Errorf("model compat maxTokensField should override, got %q", compat.MaxTokensField)
	}
	if compat.ThinkingFormat != "zai" {
		t.Errorf("unset fields should keep detected values, got thinkingFormat %q", compat.ThinkingFormat)
	}

	plain := resolveOpenAICompat(compatTestModel("https://api.z.ai/api/paas/v4", true, nil))
	if plain.ZaiToolStream {
		t.Error("without model compat, zaiToolStream stays at detected false")
	}
}

func TestZaiEnableThinkingGatesOnEffort(t *testing.T) {
	model := compatTestModel("https://api.z.ai/api/paas/v4", true, nil)

	// Thinking off: the loop maps ThinkingOff to "" which arrives as a non-nil
	// empty ReasoningEffort — enable_thinking must be an explicit false, not true.
	off := buildOpenAICompletionsSimpleOptions(&src.SimpleStreamOptions{Reasoning: ""})
	params := compatTestParams(t, model, off)
	if v, ok := params["enable_thinking"]; !ok || v != false {
		t.Errorf("enable_thinking with empty effort = (%v, %v), want (false, true)", v, ok)
	}

	on := buildOpenAICompletionsSimpleOptions(&src.SimpleStreamOptions{Reasoning: "high"})
	params = compatTestParams(t, model, on)
	if v, ok := params["enable_thinking"]; !ok || v != true {
		t.Errorf("enable_thinking with high effort = (%v, %v), want (true, true)", v, ok)
	}

	// Non-reasoning models omit the key entirely.
	quiet := compatTestModel("https://api.z.ai/api/paas/v4", false, nil)
	params = compatTestParams(t, quiet, on)
	if _, ok := params["enable_thinking"]; ok {
		t.Error("enable_thinking must be omitted when model.Reasoning is false")
	}
}

func TestQwenChatTemplateEnableThinkingGatesOnEffort(t *testing.T) {
	model := compatTestModel("https://api.z.ai/api/paas/v4", true, &src.OpenAICompletionsCompat{
		ThinkingFormat: compatStr("qwen-chat-template"),
	})

	off := buildOpenAICompletionsSimpleOptions(&src.SimpleStreamOptions{Reasoning: ""})
	params := compatTestParams(t, model, off)
	kwargs, ok := params["chat_template_kwargs"].(map[string]interface{})
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %v", params["chat_template_kwargs"])
	}
	if kwargs["enable_thinking"] != false {
		t.Errorf("enable_thinking with empty effort = %v, want false", kwargs["enable_thinking"])
	}
	if kwargs["preserve_thinking"] != true {
		t.Errorf("preserve_thinking = %v, want true", kwargs["preserve_thinking"])
	}
}

func TestZaiToolStreamParamFromCompat(t *testing.T) {
	tool := src.Tool[any]{Name: "read", Description: "read a file", Parameters: map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}}

	withStream := compatTestModel("https://api.z.ai/api/paas/v4", true, &src.OpenAICompletionsCompat{
		ZaiToolStream: compatBool(true),
	})
	ctx := src.ChatContext{Messages: []src.Message{}, Tools: []src.Tool[any]{tool}}
	compat := resolveOpenAICompat(withStream)
	params, ok := buildOpenAICompletionsParams(withStream, ctx, nil, compat).(map[string]interface{})
	if !ok {
		t.Fatal("params is not a map")
	}
	if params["tool_stream"] != true {
		t.Errorf("tool_stream = %v, want true when compat enables it", params["tool_stream"])
	}

	plain := compatTestModel("https://api.z.ai/api/paas/v4", true, nil)
	compat = resolveOpenAICompat(plain)
	params, ok = buildOpenAICompletionsParams(plain, ctx, nil, compat).(map[string]interface{})
	if !ok {
		t.Fatal("params is not a map")
	}
	if _, ok := params["tool_stream"]; ok {
		t.Error("tool_stream must be absent without the compat flag")
	}
}
