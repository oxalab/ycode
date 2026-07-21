package extensions

// js_context_test.go verifies ScrubRunnerCtx (the JSON-safe ctx converter).

import (
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
