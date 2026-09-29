package interactive

// events.go is the imperative event bridge — the pi-tui equivalent of reference
// interactive-mode's session subscription, with NO tea.Msg / program.Send. The
// SDK agent's subscribe callback runs on the agent's emitter goroutine; each
// event mutates the chat tree via TUI.Mutate (render-lock-serialized) and the
// engine re-renders. P3a covers assistant-message streaming; tool/bash/diff
// events arrive in P3b.

import (
	"context"
	"strings"

	"ycode/packages/tui/components"
	sdk "ycode/packages/agent-sdk/src"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tools"
	llm "ycode/packages/llm/src"
)

// subscribeAgent subscribes to the SDK agent's streaming/tool events. Returns an
// unsubscribe func (called from Stop).
func (o *Orchestrator) subscribeAgent() func() {
	if o.session == nil || o.session.Agent == nil {
		return func() {}
	}
	return o.session.Agent.Subscribe(func(event sdk.AgentEvent, ctx context.Context) error {
		o.handleAgentEvent(event)
		return nil
	})
}

// handleAgentEvent maps SDK agent events to chat-container mutations.
func (o *Orchestrator) handleAgentEvent(event sdk.AgentEvent) {
	switch e := event.(type) {
	case sdk.EventAgentStart:
		// The working loader spans the whole agent run (TS stops any retry
		// chrome first, interactive-mode.ts:2643-2657 — auto_retry_end may
		// not have arrived when the retried attempt starts).
		o.stopRetryChrome()
		o.showStatusLoader("Working...") // P4b working indicator (setWorkingVisible)
	case sdk.EventAgentEnd:
		o.hideStatusLoader() // P4b working indicator
	case sdk.EventMessageStart:
		if am, ok := asAssistant(e.Message); ok {
			comp := NewAssistantMessage(am, o.th, o.mdTheme(), false, "Thinking...")
			o.engine.Mutate(func() {
				o.chat.AddChild(comp)
				o.streamingComp = comp
			})
		}
	case sdk.EventMessageUpdate:
		if am, ok := asAssistant(e.Message); ok {
			o.engine.Mutate(func() {
				if o.streamingComp != nil {
					o.streamingComp.UpdateContent(am)
				}
			})
		}
	case sdk.EventMessageEnd:
		if am, ok := asAssistant(e.Message); ok {
			o.engine.Mutate(func() {
				if o.streamingComp != nil {
					o.streamingComp.UpdateContent(am)
				}
				o.streamingComp = nil
			})
		}
	case sdk.EventToolExecutionStart:
		o.handleToolStart(e.ToolCallID, e.ToolName, e.Args)
	case sdk.EventToolExecutionUpdate:
		o.handleToolUpdate(e.ToolCallID, e.PartialResult)
	case sdk.EventToolExecutionEnd:
		o.handleToolEnd(e.ToolCallID, e.Result, e.IsError)

	// A failed turn carries the error message on EventTurnEnd with no preceding
	// MessageStart/MessageEnd (agent_loop.go:173-174), so it would otherwise be
	// invisible. Surface it as a red notice (reference shows provider errors too).
	case sdk.EventTurnEnd:
		if am, ok := asAssistant(e.Message); ok && am.ErrorMessage != "" && am.ErrorMessage != "Request was aborted" {
			msg := am.ErrorMessage
			o.engine.Mutate(func() {
				o.chat.AddChild(components.NewSpacer(1))
				o.chat.AddChild(components.NewText(o.th.Fg("error", "⚠ "+msg), 0, 0, nil))
			})
		}
	}
}

// handleToolStart adds a tool (or bash) component to the chat for a new call.
func (o *Orchestrator) handleToolStart(id, name string, args any) {
	o.engine.Mutate(func() {
		if strings.EqualFold(name, "bash") {
			be := NewBashExecution(o.engine, bashCommandFromArgs(args), false, o.th)
			o.chat.AddChild(be)
			o.bashComps[id] = be
			return
		}
		tc := NewToolExecution(name, args, o.th)
		tc.MarkStarted()
		o.chat.AddChild(tc)
		o.toolComps[id] = tc
	})
}

// handleToolUpdate applies a partial result (for bash, streams output).
func (o *Orchestrator) handleToolUpdate(id string, partial any) {
	text := extractResultText(partial)
	o.engine.Mutate(func() {
		if be, ok := o.bashComps[id]; ok {
			if text != "" {
				be.AppendOutput(text)
			}
			return
		}
		if tc, ok := o.toolComps[id]; ok {
			tc.UpdateResult(text, false, true)
		}
	})
}

// handleToolEnd finalizes a tool/bash component with its result.
func (o *Orchestrator) handleToolEnd(id string, result any, isError bool) {
	text := extractResultText(result)
	exitCode := bashExitCodeFromResult(result) // best-effort for the agent-tool path
	o.engine.Mutate(func() {
		if be, ok := o.bashComps[id]; ok {
			if text != "" {
				be.AppendOutput(text)
			}
			be.SetComplete(exitCode, false, isError)
			delete(o.bashComps, id)
			return
		}
		if tc, ok := o.toolComps[id]; ok {
			tc.UpdateResult(text, isError, false)
			delete(o.toolComps, id)
		}
	})
}

// bashExitCodeFromResult extracts an exit code from a bash tool result if present
// (the result may arrive as a map from JSON, or a typed struct). Returns nil if
// not found — the caller falls back to the isError flag.
func bashExitCodeFromResult(result any) *int {
	if result == nil {
		return nil
	}
	switch r := result.(type) {
	case map[string]any:
		for _, k := range []string{"exitCode", "exit_code", "code"} {
			if v, ok := r[k]; ok {
				if code := toIntPtr(v); code != nil {
					return code
				}
			}
		}
	}
	return nil
}

// toIntPtr coerces a numeric (float64 from JSON, or int) to *int.
func toIntPtr(v any) *int {
	switch n := v.(type) {
	case int:
		return &n
	case int64:
		i := int(n)
		return &i
	case float64:
		i := int(n)
		return &i
	}
	return nil
}

// bashCommandFromArgs extracts the command string from a bash tool's args
// (reference app_chat.go:189-199).
func bashCommandFromArgs(args any) string {
	switch a := args.(type) {
	case string:
		return a
	case map[string]any:
		if cmd, ok := a["command"].(string); ok {
			return cmd
		}
	}
	return ""
}

// extractResultText pulls the concatenated text output from a tool result, and
// represents any image content blocks as a "[image: type]" marker (reference
// tool-execution.ts:326-329 fallback) so they aren't silently dropped. The result
// arrives as any (often a map[string]any / []any from JSON, or a typed
// ContentBlock slice); this mirrors the BT extractResultContent approach.
func extractResultText(result any) string {
	var parts []string
	for _, b := range extractContentAny(result) {
		if t := textOfAny(b); t != "" {
			parts = append(parts, t)
			continue
		}
		if img := imageMarkerOfAny(b); img != "" {
			parts = append(parts, img)
		}
	}
	return strings.Join(parts, "\n")
}

// imageMarkerOfAny returns a "[image: mime]" placeholder for image content
// blocks (full inline rendering via components.Image is deferred — rare in tool
// output, and needs terminal image-protocol support + a mixed-content layout).
func imageMarkerOfAny(b any) string {
	switch v := b.(type) {
	case llm.ImageContent:
		return "[image: " + v.MimeType + "]"
	case *llm.ImageContent:
		if v != nil {
			return "[image: " + v.MimeType + "]"
		}
	case map[string]any:
		if _, ok := v["data"].(string); ok {
			if mt, ok := v["mimeType"].(string); ok {
				return "[image: " + mt + "]"
			}
			if mt, ok := v["mime_type"].(string); ok {
				return "[image: " + mt + "]"
			}
			return "[image]"
		}
	}
	return ""
}

func extractContentAny(result any) []any {
	if result == nil {
		return nil
	}
	switch r := result.(type) {
	case []any:
		return r
	case map[string]any:
		if content, ok := r["content"].([]any); ok {
			return content
		}
		return []any{r}
	case []llm.ContentBlock:
		out := make([]any, len(r))
		for i, b := range r {
			out[i] = b
		}
		return out
	case *tools.ToolExecuteResult:
		// Tools (bash/read/grep/etc.) return *ToolExecuteResult{Content: [...]}.
		out := make([]any, len(r.Content))
		for i, b := range r.Content {
			out[i] = b
		}
		return out
	case tools.ToolExecuteResult:
		out := make([]any, len(r.Content))
		for i, b := range r.Content {
			out[i] = b
		}
		return out
	}
	return nil
}

func textOfAny(b any) string {
	switch v := b.(type) {
	case llm.TextContent:
		return v.Text
	case *llm.TextContent:
		return v.Text
	case map[string]any:
		if t, ok := v["text"].(string); ok {
			return t
		}
	}
	return ""
}

// asAssistant extracts an *llm.AssistantMessage from a Message interface
// (AgentMessage = llm.Message), handling both value and pointer messages.
func asAssistant(m llm.Message) (*llm.AssistantMessage, bool) {
	switch v := m.(type) {
	case *llm.AssistantMessage:
		return v, true
	case llm.AssistantMessage:
		return &v, true
	}
	return nil, false
}

// subscribeSession subscribes to AgentSession-level events (compaction, queue,
// retry, …). Returns an unsubscribe func. P3c wires compaction_end; the rest are
// handled in later phases (P4 status/loader, P6 queue).
func (o *Orchestrator) subscribeSession() func() {
	if o.session == nil {
		return func() {}
	}
	return o.session.Subscribe(o.handleSessionEvent)
}

// handleSessionEvent maps AgentSessionEvents to chat/status mutations.
func (o *Orchestrator) handleSessionEvent(event core.AgentSessionEvent) {
	switch event.EventType {
	case "queue_update":
		// P4b pending-message queue preview (updatePendingMessagesDisplay).
		o.renderPending(event.Steering, event.FollowUp)
	case "compaction_start":
		o.showStatusLoader("Compacting context… (esc to cancel)") // P4b working indicator
	case "compaction_end":
		o.hideStatusLoader()
		if event.CompactionResult != nil {
			comp := NewCompactionSummary(event.CompactionResult, o.th, o.mdTheme())
			o.engine.Mutate(func() { o.chat.AddChild(comp) })
		}
	case "auto_retry_start":
		o.handleRetryStart(event)
	case "auto_retry_end":
		o.handleRetryEnd(event)
	}
}
