package modes

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

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

// ============================================================================
// buildUIContext — emitted request shape (audit #28)
// ============================================================================

// syncBuffer is a concurrency-safe bytes.Buffer: dialog closures emit from the
// caller's goroutine while routeUIResponse may run on another.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForRequest scans the captured stdout lines for the first request with the
// given method and returns it fully decoded.
func waitForRequest(t *testing.T, buf *syncBuffer, method string) RpcExtensionUIRequest {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(buf.String(), "\n") {
			var req RpcExtensionUIRequest
			if json.Unmarshal([]byte(line), &req) == nil && req.Method == method {
				return req
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %q request emitted within deadline; captured:\n%s", method, buf.String())
	return RpcExtensionUIRequest{}
}

// TestBuildUIContext_RequestShape verifies the dialog closures populate the
// reference's wire fields: title on every dialog, statusKey on setStatus, and
// ycode's defaultIdx on select. Input must NOT carry Message (the reference
// sends title + placeholder only; the old port stuffed the prompt there).
func TestBuildUIContext_RequestShape(t *testing.T) {
	var buf syncBuffer
	s := &rpcServer{out: &buf, pending: make(map[string]chan RpcExtensionUIResponse)}
	ui := s.buildUIContext()

	// Blocking dialogs: emit immediately, then time out (resolver behavior is
	// covered by the dialog*Value tests above).
	_, _ = ui.Select("Pick", []string{"a", "b"}, 1, 40)
	req := waitForRequest(t, &buf, MethodSelect)
	if req.Title != "Pick" || len(req.Options) != 2 || req.DefaultIdx != 1 || req.Timeout != 40 {
		t.Errorf("select request = %+v", req)
	}

	_, _ = ui.Confirm("Confirm", "Proceed?", 40)
	req = waitForRequest(t, &buf, MethodConfirm)
	if req.Title != "Confirm" || req.Message != "Proceed?" || req.Timeout != 40 {
		t.Errorf("confirm request = %+v", req)
	}

	_, _ = ui.Input("Name", "hint", 40)
	req = waitForRequest(t, &buf, MethodInput)
	if req.Title != "Name" || req.Placeholder != "hint" || req.Timeout != 40 {
		t.Errorf("input request = %+v", req)
	}
	if req.Message != "" {
		t.Errorf("input request carries Message %q; reference sends title+placeholder only", req.Message)
	}

	// Fire-and-forget.
	ui.Status("sync", "running")
	req = waitForRequest(t, &buf, MethodSetStatus)
	if req.StatusKey != "sync" || req.StatusText != "running" {
		t.Errorf("setStatus request = %+v", req)
	}

	// Editor blocks until the host responds (no timeout in the reference) —
	// emit, observe, then answer from the test goroutine.
	type editorResult struct {
		text string
		err  error
	}
	done := make(chan editorResult, 1)
	go func() {
		text, err := ui.Editor("Commit", "feat: ")
		done <- editorResult{text, err}
	}()
	editorReq := waitForRequest(t, &buf, MethodEditor)
	if editorReq.Title != "Commit" || editorReq.Prefill != "feat: " {
		t.Errorf("editor request = %+v", editorReq)
	}
	s.routeUIResponse(RpcExtensionUIResponse{Type: ExtUIResponseType, ID: editorReq.ID, Value: "body"})
	select {
	case r := <-done:
		if r.err != nil || r.text != "body" {
			t.Errorf("Editor = (%q, %v), want (\"body\", nil)", r.text, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Editor did not resolve after host response")
	}
}
