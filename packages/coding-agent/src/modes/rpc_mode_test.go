package modes

import (
	"testing"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

// ============================================================================
// buildCommandsList
// ============================================================================

// TestBuildCommandsList_AssemblesAllSources verifies extension/prompt/skill
// commands are gathered with the right name, source, and sourceInfo passthrough
// (skills are prefixed "skill:").
func TestBuildCommandsList_AssemblesAllSources(t *testing.T) {
	extSrc := core.SourceInfo{Path: "/ext", Source: "extension"}
	promptSrc := core.SourceInfo{Path: "/p", Source: "prompt"}
	skillSrc := core.SourceInfo{Path: "/s", Source: "skill"}

	extCmds := []*extensions.ResolvedCommand{
		{InvocationName: "/ext", RegisteredCommand: extensions.RegisteredCommand{Description: "ext cmd", SourceInfo: extSrc}},
	}
	templates := []core.PromptTemplate{
		{Name: "plan", Description: "Plan mode", SourceInfo: promptSrc},
	}
	skills := []core.Skill{
		{Name: "summarize", Description: "Summarize code", SourceInfo: skillSrc},
	}

	got := buildCommandsList(extCmds, templates, skills)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	// Extension command.
	if got[0].Name != "/ext" || got[0].Source != SourceExtension || got[0].Description != "ext cmd" {
		t.Errorf("ext cmd = %+v", got[0])
	}
	if si, ok := got[0].SourceInfo.(core.SourceInfo); !ok || si.Path != "/ext" {
		t.Errorf("ext sourceInfo passthrough = %+v", got[0].SourceInfo)
	}

	// Prompt command.
	if got[1].Name != "plan" || got[1].Source != SourcePrompt || got[1].Description != "Plan mode" {
		t.Errorf("prompt cmd = %+v", got[1])
	}

	// Skill command — prefixed.
	if got[2].Name != "skill:summarize" || got[2].Source != SourceSkill || got[2].Description != "Summarize code" {
		t.Errorf("skill cmd = %+v", got[2])
	}
}

// TestBuildCommandsList_Empty verifies all-empty inputs yield an empty (non-nil) slice.
func TestBuildCommandsList_Empty(t *testing.T) {
	got := buildCommandsList(nil, nil, nil)
	if len(got) != 0 {
		t.Errorf("empty inputs → %d commands, want 0", len(got))
	}
}

// ============================================================================
// lastAssistantText
// ============================================================================

// TestLastAssistantText_Empty verifies no messages → "".
func TestLastAssistantText_Empty(t *testing.T) {
	if got := lastAssistantText(nil); got != "" {
		t.Errorf("empty → %q, want empty", got)
	}
}

// TestLastAssistantText_ScansBackward verifies the LAST assistant message is
// used even when an earlier assistant message exists after a user message.
func TestLastAssistantText_ScansBackward(t *testing.T) {
	msgs := []llm.Message{
		assistantMsg(llm.StopReasonStop, "", []string{"old"}),
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: "q"}}},
		assistantMsg(llm.StopReasonStop, "", []string{"new ", "answer"}),
	}
	if got := lastAssistantText(msgs); got != "new answer" {
		t.Errorf("→ %q, want 'new answer'", got)
	}
}

// TestLastAssistantText_NoAssistant verifies a user-only history → "".
func TestLastAssistantText_NoAssistant(t *testing.T) {
	msgs := []llm.Message{
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: "hi"}}},
	}
	if got := lastAssistantText(msgs); got != "" {
		t.Errorf("→ %q, want empty (no assistant)", got)
	}
}

// TestLastAssistantText_SkipsNonText verifies non-text blocks are not joined.
func TestLastAssistantText_SkipsNonText(t *testing.T) {
	msgs := []llm.Message{
		assistantMsg(llm.StopReasonStop, "", []string{"keep"}, llm.ToolCall{Type: "tool_use"}),
	}
	if got := lastAssistantText(msgs); got != "keep" {
		t.Errorf("→ %q, want 'keep'", got)
	}
}

// ============================================================================
// dialog resolvers (select / confirm / input)
// ============================================================================

// TestDialogSelectIndex covers found / not-found / cancelled / timeout.
func TestDialogSelectIndex(t *testing.T) {
	options := []string{"a", "b", "c"}
	cases := []struct {
		name string
		resp RpcExtensionUIResponse
		ok   bool
		want int
	}{
		{"found-second", RpcExtensionUIResponse{Value: "b"}, true, 1},
		{"found-first", RpcExtensionUIResponse{Value: "a"}, true, 0},
		{"not-in-options", RpcExtensionUIResponse{Value: "z"}, true, -1},
		{"cancelled", RpcExtensionUIResponse{Cancelled: true, Value: "b"}, true, -1},
		{"timeout", RpcExtensionUIResponse{}, false, -1},
	}
	for _, c := range cases {
		if got := dialogSelectIndex(c.resp, options, c.ok); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

// TestDialogConfirmValue covers confirmed true/false (via *bool), cancelled, timeout.
func TestDialogConfirmValue(t *testing.T) {
	no := false
	yes := true
	cases := []struct {
		name string
		resp RpcExtensionUIResponse
		ok   bool
		want bool
	}{
		{"confirmed-true", RpcExtensionUIResponse{Confirmed: &yes}, true, true},
		{"confirmed-false", RpcExtensionUIResponse{Confirmed: &no}, true, false}, // *false is meaningful
		{"cancelled", RpcExtensionUIResponse{Cancelled: true, Confirmed: &yes}, true, false},
		{"timeout", RpcExtensionUIResponse{}, false, false},
		{"nil-confirmed", RpcExtensionUIResponse{}, true, false},
	}
	for _, c := range cases {
		if got := dialogConfirmValue(c.resp, c.ok); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDialogInputValue covers value / cancelled / timeout.
func TestDialogInputValue(t *testing.T) {
	cases := []struct {
		name string
		resp RpcExtensionUIResponse
		ok   bool
		want string
	}{
		{"value", RpcExtensionUIResponse{Value: "typed"}, true, "typed"},
		{"cancelled", RpcExtensionUIResponse{Cancelled: true, Value: "typed"}, true, ""},
		{"timeout", RpcExtensionUIResponse{}, false, ""},
	}
	for _, c := range cases {
		if got := dialogInputValue(c.resp, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// ============================================================================
// imagesToAny
// ============================================================================

// TestImagesToAny verifies nil/empty → nil and a populated slice is converted.
func TestImagesToAny(t *testing.T) {
	if got := imagesToAny(nil); got != nil {
		t.Errorf("nil → %+v, want nil", got)
	}
	if got := imagesToAny([]*llm.ImageContent{}); got != nil {
		t.Errorf("empty → %+v, want nil", got)
	}
	imgs := []*llm.ImageContent{{Type: "image", Data: "abc", MimeType: "image/png"}}
	got := imagesToAny(imgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if _, ok := got[0].(*llm.ImageContent); !ok {
		t.Errorf("element type = %T, want *llm.ImageContent", got[0])
	}
}
