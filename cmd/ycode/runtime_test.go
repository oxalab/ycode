package main

import (
	"testing"

	core "ycode/packages/coding-agent/src/core"
	cli "ycode/packages/coding-agent/src/cli"
	llm "ycode/packages/llm/src"
)

// fakeRegistry is a minimal core.ModelRegistryResolver for buildSessionOptions
// tests, so the mapping logic is exercised without constructing a real
// ModelRegistry (and its auth.json/models.json side effects).
type fakeRegistry struct{ all []*llm.Model[llm.Api] }

func (f fakeRegistry) GetAvailable() ([]*llm.Model[llm.Api], error) { return f.all, nil }
func (f fakeRegistry) GetAll() []*llm.Model[llm.Api]                { return f.all }
func (f fakeRegistry) Find(provider, modelID string) *llm.Model[llm.Api] {
	for _, m := range f.all {
		if string(m.Provider) == provider && m.ID == modelID {
			return m
		}
	}
	return nil
}
func (f fakeRegistry) HasConfiguredAuth(model *llm.Model[llm.Api]) bool { return true }

func testModel(provider, id string) *llm.Model[llm.Api] {
	return &llm.Model[llm.Api]{Provider: llm.Provider(provider), ID: id}
}

func thinkingPtr(level string) *llm.ThinkingLevel {
	tl := llm.ThinkingLevel(level)
	return &tl
}

// TestFindScopedModel verifies nil-target safety, a match, and a miss. This is
// the extracted seam behind the saved-default-in-scope branch.
func TestFindScopedModel(t *testing.T) {
	m1 := testModel("openai", "gpt-4o")
	scoped := []core.ScopedModel{{Model: m1}}

	if got := findScopedModel(scoped, nil); got != nil {
		t.Error("nil target → non-nil, want nil")
	}
	if got := findScopedModel(scoped, testModel("openai", "gpt-4o")); got == nil || got.Model != m1 {
		t.Errorf("match → %+v, want the scoped model", got)
	}
	if got := findScopedModel(scoped, testModel("anthropic", "sonnet")); got != nil {
		t.Errorf("miss → %+v, want nil", got)
	}
}

// TestBuildSessionOptions_Empty verifies the nothing-chosen case yields an empty
// result (no model, no tools, no thinking).
func TestBuildSessionOptions_Empty(t *testing.T) {
	sm := core.InMemorySettingsManager(core.Settings{})
	res := buildSessionOptions(cli.Args{}, nil, false, fakeRegistry{}, sm)
	if res.Model != nil {
		t.Errorf("Model = %+v, want nil", res.Model)
	}
	if res.NoTools != "" || res.ThinkingLevel != "" || res.Tools != nil {
		t.Errorf("empty result = %+v, want zero values for tools/thinking", res)
	}
	if len(res.ScopedModels) != 0 {
		t.Errorf("ScopedModels = %d, want 0", len(res.ScopedModels))
	}
}

// TestBuildSessionOptions_Tools verifies --no-tools/--no-builtin-tools/--tools
// mapping onto NoTools ("all"/"builtin") and a copied Tools allowlist.
func TestBuildSessionOptions_Tools(t *testing.T) {
	sm := core.InMemorySettingsManager(core.Settings{})
	reg := fakeRegistry{}

	if res := buildSessionOptions(cli.Args{NoTools: true}, nil, false, reg, sm); res.NoTools != "all" {
		t.Errorf("--no-tools → NoTools %q, want all", res.NoTools)
	}
	if res := buildSessionOptions(cli.Args{NoBuiltinTools: true}, nil, false, reg, sm); res.NoTools != "builtin" {
		t.Errorf("--no-builtin-tools → NoTools %q, want builtin", res.NoTools)
	}
	// --no-tools takes precedence over --no-builtin-tools.
	if res := buildSessionOptions(cli.Args{NoTools: true, NoBuiltinTools: true}, nil, false, reg, sm); res.NoTools != "all" {
		t.Errorf("both → NoTools %q, want all", res.NoTools)
	}
	res := buildSessionOptions(cli.Args{Tools: []string{"read", "grep"}}, nil, false, reg, sm)
	if len(res.Tools) != 2 || res.Tools[0] != "read" || res.Tools[1] != "grep" {
		t.Errorf("--tools → %v, want [read grep]", res.Tools)
	}
}

// TestBuildSessionOptions_ThinkingPrecedence verifies explicit --thinking sets
// the level (and is independent of model resolution).
func TestBuildSessionOptions_ThinkingPrecedence(t *testing.T) {
	sm := core.InMemorySettingsManager(core.Settings{})
	// "high" is an untyped string constant assignable to sdk.ThinkingLevel.
	res := buildSessionOptions(cli.Args{Thinking: "high"}, nil, false, fakeRegistry{}, sm)
	if res.ThinkingLevel != "high" {
		t.Errorf("ThinkingLevel = %q, want high", res.ThinkingLevel)
	}
	if res.CLIThinkingFromModel {
		t.Error("CLIThinkingFromModel = true, want false (no model shorthand)")
	}
}

// TestBuildSessionOptions_ScopedFallback verifies that with no CLI model, a fresh
// session, and scoped models, the first scoped model is chosen; and that scoped
// models are converted to entries (thinking level preserved, nil → empty).
func TestBuildSessionOptions_ScopedFallback(t *testing.T) {
	sm := core.InMemorySettingsManager(core.Settings{})
	m1 := testModel("openai", "gpt-4o")
	m2 := testModel("anthropic", "sonnet")
	scoped := []core.ScopedModel{
		{Model: m1, ThinkingLevel: thinkingPtr("low")},
		{Model: m2}, // no thinking level
	}

	res := buildSessionOptions(cli.Args{}, scoped, false, fakeRegistry{}, sm)
	if res.Model != m1 {
		t.Errorf("fallback Model = %+v, want first scoped m1", res.Model)
	}
	if res.ThinkingLevel != "low" {
		t.Errorf("fallback ThinkingLevel = %q, want low (from first scoped)", res.ThinkingLevel)
	}
	if len(res.ScopedModels) != 2 {
		t.Fatalf("ScopedModels len = %d, want 2", len(res.ScopedModels))
	}
	if res.ScopedModels[0].ThinkingLevel != "low" {
		t.Errorf("entry[0].ThinkingLevel = %q, want low", res.ScopedModels[0].ThinkingLevel)
	}
	if res.ScopedModels[1].ThinkingLevel != "" {
		t.Errorf("entry[1].ThinkingLevel = %q, want empty (nil → inherit)", res.ScopedModels[1].ThinkingLevel)
	}
}

// TestBuildSessionOptions_ScopedSkippedOnExistingSession verifies the scoped-
// model fallback is suppressed when resuming a session that already has a model
// (hasExistingSession=true), matching the TS guard.
func TestBuildSessionOptions_ScopedSkippedOnExistingSession(t *testing.T) {
	sm := core.InMemorySettingsManager(core.Settings{})
	m1 := testModel("openai", "gpt-4o")
	scoped := []core.ScopedModel{{Model: m1}}

	res := buildSessionOptions(cli.Args{}, scoped, true, fakeRegistry{}, sm)
	if res.Model != nil {
		t.Errorf("existing-session Model = %+v, want nil (fallback suppressed)", res.Model)
	}
	// Scoped-model *entries* are still produced for cycling regardless.
	if len(res.ScopedModels) != 1 {
		t.Errorf("ScopedModels len = %d, want 1 (cycling entries preserved)", len(res.ScopedModels))
	}
}
