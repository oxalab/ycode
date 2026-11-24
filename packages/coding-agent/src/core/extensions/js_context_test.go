package extensions

// js_context_test.go verifies ScrubRunnerCtx (the JSON-safe ctx converter) and
// the BuildJSContext object rendering (the shape JS handlers/tools receive).

import (
	"context"
	"testing"
)

func TestScrubRunnerCtx_Primitives(t *testing.T) {
	in := map[string]any{
		"cwd":                "/home/user",
		"hasUI":              true,
		"isIdle":             false,
		"hasPendingMessages": true,
		"model":              nil,
	}
	out := ScrubRunnerCtx(in)
	if out["cwd"] != "/home/user" {
		t.Errorf("cwd = %v", out["cwd"])
	}
	if out["hasUI"] != true {
		t.Errorf("hasUI = %v", out["hasUI"])
	}
	if out["isIdle"] != false {
		t.Errorf("isIdle = %v", out["isIdle"])
	}
}

func TestScrubRunnerCtx_DropsNonPrimitives(t *testing.T) {
	// A func and a struct should NOT appear in the output (they don't JSON-marshal).
	in := map[string]any{
		"cwd":   "/x",
		"abort": func() {}, // non-primitive
		"model": struct{ X int }{42}, // struct — may or may not be JSON-safe; ScrubRunnerCtx surfaces it if so
	}
	out := ScrubRunnerCtx(in)
	if _, present := out["abort"]; present {
		t.Error("abort (func) should have been dropped")
	}
	// cwd always preserved
	if out["cwd"] != "/x" {
		t.Errorf("cwd = %v", out["cwd"])
	}
}

func TestScrubRunnerCtx_ResolvesGettersEagerly(t *testing.T) {
	calledCU := false
	calledSP := false
	in := map[string]any{
		"cwd": "/x",
		"getContextUsage": func() any {
			calledCU = true
			return map[string]any{"tokens": 100, "percent": 25}
		},
		"getSystemPrompt": func() string {
			calledSP = true
			return "You are an agent."
		},
	}
	out := ScrubRunnerCtx(in)
	if !calledCU {
		t.Error("getContextUsage was not resolved eagerly")
	}
	if !calledSP {
		t.Error("getSystemPrompt was not resolved eagerly")
	}
	if cu, ok := out["contextUsage"].(map[string]any); !ok || cu["tokens"] != 100 {
		t.Errorf("contextUsage = %v", out["contextUsage"])
	}
	if out["systemPrompt"] != "You are an agent." {
		t.Errorf("systemPrompt = %v", out["systemPrompt"])
	}
}

func TestScrubRunnerCtx_NilSafe(t *testing.T) {
	out := ScrubRunnerCtx(nil)
	if out != nil {
		t.Errorf("ScrubRunnerCtx(nil) = %v, want nil", out)
	}
}

// Story 42 (C16): BuildJSContext renders the runner ctx's signal member as
// {aborted: bool} (a Go context.Context can't cross the bridge — a snapshot,
// not the reference's live AbortSignal) plus the eager getter fields.
func TestBuildJSContextRendersSignalAndGetters(t *testing.T) {
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })

	aborted, cancel := context.WithCancel(context.Background())
	cancel()
	runnerCtx := map[string]any{
		"cwd":       "/proj",
		"hasUI":     true,
		"isIdle":    true,
		"model":     map[string]any{"id": "m1"},
		"signal":    aborted,
		"getContextUsage": func() any { return map[string]any{"tokens": 100, "percent": 25} },
		"getSystemPrompt": func() string { return "You are an agent." },
	}
	jsCtx, err := BuildJSContext(jrt, runnerCtx)
	if err != nil {
		t.Fatalf("BuildJSContext: %v", err)
	}
	t.Cleanup(func() { jrt.safeCall(func() error { jsCtx.Free(); return nil }) })

	if v := jsCtx.GetPropertyStr("cwd"); v == nil || v.String() != "/proj" {
		t.Errorf("cwd = %v, want /proj", v)
	}
	if v := jsCtx.GetPropertyStr("isIdle"); v == nil || !v.Bool() {
		t.Errorf("isIdle = %v, want true", v)
	}
	model := jsCtx.GetPropertyStr("model")
	if model == nil {
		t.Fatal("model missing")
	}
	if id := model.GetPropertyStr("id"); id == nil || id.String() != "m1" {
		t.Errorf("model.id = %v, want m1", id)
	}
	sig := jsCtx.GetPropertyStr("signal")
	if sig == nil {
		t.Fatal("signal missing")
	}
	if v := sig.GetPropertyStr("aborted"); v == nil || !v.Bool() {
		t.Errorf("signal.aborted = %v, want true (cancelled context)", v)
	}
	if v := jsCtx.GetPropertyStr("contextUsage"); v == nil {
		t.Fatal("contextUsage (eager getter) missing")
	}
	if v := jsCtx.GetPropertyStr("systemPrompt"); v == nil || v.String() != "You are an agent." {
		t.Errorf("systemPrompt = %v, want the getter's result", v)
	}
	// Ownership note: the object was NOT handed to a bridge method here, so the
	// test frees it itself (raw Free under safeCall).
}
