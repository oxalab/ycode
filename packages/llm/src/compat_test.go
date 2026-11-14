package llm

import "testing"

// MergeCompat backs the models.json compat plumbing: provider-level compat is
// the base, model-level compat the override, per-field override-wins (the
// reference's mergeCompat), with routing sub-objects deep-merged.

func boolPtr(b bool) *bool { return &b }

func strPtr(s string) *string { return &s }

func TestMergeCompatOverrideWinsPerField(t *testing.T) {
	base := Compat{OpenAICompletions: &OpenAICompletionsCompat{
		SupportsUsageInStreaming: boolPtr(false),
		MaxTokensField:          strPtr("max_tokens"),
	}}
	override := Compat{OpenAICompletions: &OpenAICompletionsCompat{
		SupportsUsageInStreaming: boolPtr(true),
	}}

	merged := MergeCompat(base, override)
	if merged.OpenAICompletions == nil {
		t.Fatal("merged compat lost OpenAICompletions")
	}
	if merged.OpenAICompletions.SupportsUsageInStreaming == nil || !*merged.OpenAICompletions.SupportsUsageInStreaming {
		t.Errorf("override supportsUsageInStreaming should win, got %v", merged.OpenAICompletions.SupportsUsageInStreaming)
	}
	if merged.OpenAICompletions.MaxTokensField == nil || *merged.OpenAICompletions.MaxTokensField != "max_tokens" {
		t.Errorf("base maxTokensField should survive, got %v", merged.OpenAICompletions.MaxTokensField)
	}
}

func TestMergeCompatDeepMergesRouting(t *testing.T) {
	base := Compat{OpenAICompletions: &OpenAICompletionsCompat{
		OpenRouterRouting: &OpenRouterRouting{Only: []string{"alpha"}},
	}}
	override := Compat{OpenAICompletions: &OpenAICompletionsCompat{
		OpenRouterRouting: &OpenRouterRouting{Order: []string{"beta", "gamma"}},
	}}

	merged := MergeCompat(base, override)
	routing := merged.OpenAICompletions.OpenRouterRouting
	if routing == nil {
		t.Fatal("routing sub-object lost in merge")
	}
	if len(routing.Only) != 1 || routing.Only[0] != "alpha" {
		t.Errorf("base routing.Only should survive, got %v", routing.Only)
	}
	if len(routing.Order) != 2 || routing.Order[0] != "beta" {
		t.Errorf("override routing.Order should be applied, got %v", routing.Order)
	}
}

func TestMergeCompatNilSides(t *testing.T) {
	withOpenAI := Compat{OpenAICompletions: &OpenAICompletionsCompat{ZaiToolStream: boolPtr(true)}}

	if got := MergeCompat(Compat{}, withOpenAI); got.OpenAICompletions == nil || got.OpenAICompletions.ZaiToolStream == nil || !*got.OpenAICompletions.ZaiToolStream {
		t.Errorf("nil base should yield override, got %+v", got)
	}
	if got := MergeCompat(withOpenAI, Compat{}); got.OpenAICompletions == nil || got.OpenAICompletions.ZaiToolStream == nil || !*got.OpenAICompletions.ZaiToolStream {
		t.Errorf("nil override should yield base, got %+v", got)
	}
	if got := MergeCompat(Compat{}, Compat{}); got.OpenAICompletions != nil || got.OpenAIResponses != nil || got.AnthropicMessages != nil {
		t.Errorf("nil+nil should stay empty, got %+v", got)
	}
}
