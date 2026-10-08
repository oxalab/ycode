package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdk "ycode/packages/agent-sdk/src"
)

// Audit A8: prepareEditArguments existed but was never wired into the live
// tool path, so models sending `edits` as a JSON string (Opus/GLM quirk) got
// "invalid arguments". It is now the edit definition's PrepareArguments shim,
// copied to the AgentTool and applied by the agent loop before Execute.

func TestPrepareEditArgumentsRepairsStringEdits(t *testing.T) {
	out := prepareEditArguments(map[string]any{
		"path":  "f.txt",
		"edits": `[{"oldText":"a","newText":"b"},{"oldText":"c","newText":"d","why":"note"}]`,
	})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	edits, ok := m["edits"].([]any)
	if !ok {
		t.Fatalf("edits should be repaired to an array, got %T", m["edits"])
	}
	if len(edits) != 2 {
		t.Fatalf("expected 2 edits, got %d", len(edits))
	}
	// Extra keys in edit objects must survive (TS JSON.parse parity).
	first, ok := edits[0].(map[string]any)
	if !ok || first["why"] != nil {
		// first edit has no "why"; check the second one does.
		second := edits[1].(map[string]any)
		if second["why"] != "note" {
			t.Errorf("extra keys should survive the repair, got %v", edits)
		}
	}
}

func TestPrepareEditArgumentsLegacyTopLevel(t *testing.T) {
	out := prepareEditArguments(map[string]any{
		"path":    "f.txt",
		"oldText": "a",
		"newText": "b",
	})
	m := out.(map[string]any)
	if _, exists := m["oldText"]; exists {
		t.Error("legacy oldText should be removed from top level")
	}
	edits := m["edits"].([]any)
	if len(edits) != 1 {
		t.Fatalf("expected exactly 1 repaired edit, got %d", len(edits))
	}
}

func TestPrepareEditArgumentsNonMapPassthrough(t *testing.T) {
	in := "not-a-map"
	if out := prepareEditArguments(in); out != in {
		t.Errorf("non-map input must pass through unchanged, got %T", out)
	}
}

// CreateEditTool must carry the shim onto the AgentTool (the live loop calls
// AgentTool.PrepareArguments before Execute).
func TestEditAgentToolHasPrepareArguments(t *testing.T) {
	tool := CreateEditTool(t.TempDir(), nil)
	if tool.PrepareArguments == nil {
		t.Fatal("CreateEditTool's AgentTool is missing PrepareArguments")
	}
	out, err := tool.PrepareArguments(map[string]any{
		"path":  "f.txt",
		"edits": `[{"oldText":"a","newText":"b"}]`,
	})
	if err != nil {
		t.Fatalf("PrepareArguments error: %v", err)
	}
	if _, ok := out.(map[string]any)["edits"].([]any); !ok {
		t.Errorf("edits not repaired through the AgentTool shim: %v", out)
	}
}

// End-to-end composition (what the agent loop does): shim → marshal → Execute.
func TestEditToolStringEditsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("aaa\nbbb\nccc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := CreateEditToolDefinition(dir, nil)
	prepared, err := def.PrepareArguments(map[string]any{
		"path":  "f.txt",
		"edits": `[{"oldText":"bbb","newText":"BBB"}]`,
	})
	if err != nil {
		t.Fatalf("prepare error: %v", err)
	}
	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := def.Execute(context.Background(), raw); err != nil {
		t.Fatalf("Execute after prepare failed: %v", err)
	}
	content, _ := os.ReadFile(file)
	if string(content) != "aaa\nBBB\nccc\n" {
		t.Errorf("file not edited correctly, got %q", content)
	}
}

// Legacy top-level fields through the full path must apply exactly one edit
// (prepare converts them; validateEditInput's fallback must not double-append).
func TestEditToolLegacyFieldsSingleApply(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "g.txt")
	if err := os.WriteFile(file, []byte("xxx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := CreateEditToolDefinition(dir, nil)
	prepared, err := def.PrepareArguments(map[string]any{
		"path":    "g.txt",
		"oldText": "xxx",
		"newText": "yyy",
	})
	if err != nil {
		t.Fatalf("prepare error: %v", err)
	}
	raw, _ := json.Marshal(prepared)
	if _, err := def.Execute(context.Background(), raw); err != nil {
		t.Fatalf("Execute after prepare failed: %v", err)
	}
	content, _ := os.ReadFile(file)
	if string(content) != "yyy\n" {
		t.Errorf("expected single edit result %q, got %q", "yyy\n", content)
	}
}

// The shim is invoked through the same signature the SDK loop uses.
func TestWrapToolDefinitionCarriesPrepare(t *testing.T) {
	def := CreateEditToolDefinition(".", nil)
	tool := WrapToolDefinition(def, nil)
	if tool.PrepareArguments == nil {
		t.Fatal("WrapToolDefinition dropped PrepareArguments")
	}
	// Reverse direction: synthesized definition keeps it too.
	back := CreateToolDefinitionFromAgentTool(&sdk.AgentTool[any]{
		Name: "x", PrepareArguments: tool.PrepareArguments,
	})
	if back.PrepareArguments == nil {
		t.Fatal("CreateToolDefinitionFromAgentTool dropped PrepareArguments")
	}
}
