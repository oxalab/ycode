package modes

import (
	"testing"

	llm "ycode/packages/llm/src"
)

// assistantMsg builds an AssistantMessage with the given stop reason, error
// message, and text-block contents (plus an optional non-text block).
func assistantMsg(stop llm.StopReason, errMsg string, texts []string, extra ...llm.ContentBlock) llm.AssistantMessage {
	content := make([]llm.ContentBlock, 0, len(texts)+len(extra))
	for _, t := range texts {
		content = append(content, llm.TextContent{Type: "text", Text: t})
	}
	content = append(content, extra...)
	return llm.AssistantMessage{
		Role:         llm.RoleAssistant,
		Content:      content,
		StopReason:   stop,
		ErrorMessage: errMsg,
	}
}

// TestExtractPrintResult_Empty verifies no messages yields no output / exit 0.
func TestExtractPrintResult_Empty(t *testing.T) {
	r := extractPrintResult(nil)
	if r.text != "" || r.exitCode != 0 || r.errMsg != "" {
		t.Errorf("empty → %+v, want zero", r)
	}
}

// TestExtractPrintResult_LastNotAssistant verifies a non-assistant last message
// produces no output (the TS only acts when the final message is assistant).
func TestExtractPrintResult_LastNotAssistant(t *testing.T) {
	msgs := []llm.Message{
		assistantMsg(llm.StopReasonStop, "", []string{"earlier assistant text"}),
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: "hi"}}},
	}
	r := extractPrintResult(msgs)
	if r.text != "" || r.exitCode != 0 {
		t.Errorf("non-assistant last → %+v, want no output (must not scan back to earlier assistant)", r)
	}
}

// TestExtractPrintResult_TextBlocks verifies text content blocks are concatenated.
func TestExtractPrintResult_TextBlocks(t *testing.T) {
	msgs := []llm.Message{assistantMsg(llm.StopReasonStop, "", []string{"Hello ", "world"})}
	r := extractPrintResult(msgs)
	if r.text != "Hello world" || r.exitCode != 0 {
		t.Errorf("text blocks → %+v, want text='Hello world' exit=0", r)
	}
}

// TestExtractPrintResult_SkipsNonTextContent verifies non-text content blocks
// (e.g. a tool call) are ignored and only text is emitted.
func TestExtractPrintResult_SkipsNonTextContent(t *testing.T) {
	msgs := []llm.Message{assistantMsg(llm.StopReasonStop, "", []string{"only this"},
		llm.ToolCall{Type: "tool_use"})}
	r := extractPrintResult(msgs)
	if r.text != "only this" {
		t.Errorf("non-text filtered → %q, want 'only this'", r.text)
	}
}

// TestExtractPrintResult_Error verifies an error stop reason yields exit 1 with
// the assistant's error message.
func TestExtractPrintResult_Error(t *testing.T) {
	msgs := []llm.Message{assistantMsg(llm.StopReasonError, "upstream blew up", nil)}
	r := extractPrintResult(msgs)
	if r.exitCode != 1 || r.errMsg != "upstream blew up" || r.text != "" {
		t.Errorf("error stop → %+v, want exit=1 errMsg='upstream blew up'", r)
	}
}

// TestExtractPrintResult_AbortedNoMessage verifies an aborted stop reason with no
// error message falls back to "Request aborted".
func TestExtractPrintResult_AbortedNoMessage(t *testing.T) {
	msgs := []llm.Message{assistantMsg(llm.StopReasonAborted, "", nil)}
	r := extractPrintResult(msgs)
	if r.exitCode != 1 || r.errMsg != "Request aborted" {
		t.Errorf("aborted → %+v, want exit=1 errMsg='Request aborted'", r)
	}
}
