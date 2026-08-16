package tui

import (
	"strings"

	"ycode/packages/coding-agent/src/core/tui/components"

	tea "github.com/charmbracelet/bubbletea"
)

// chatEntry represents one item rendered in the chat viewport.
// An entry is either a live component (item != nil) that can be updated
// during streaming, or a pre-rendered static string.
type chatEntry struct {
	// item is the live component (assistant message, tool, bash, etc.).
	// nil for static entries that only use staticText.
	item components.ChatItem

	// staticText is the pre-rendered string used when item is nil
	// (e.g., user messages, which never change once rendered).
	staticText string

	// toolCallID is set for tool/bash entries so they can be looked up
	// by ID during tool-execution events. Empty for other entries.
	toolCallID string

	// entryKind tags the entry type for rendering logic.
	entryKind chatEntryKind
}

// chatEntryKind enumerates the kinds of chat entries.
type chatEntryKind int

const (
	chatEntryStatic chatEntryKind = iota // staticText only
	chatEntryAssistant                   // streaming/final assistant message
	chatEntryTool                        // tool execution
	chatEntryBash                        // bash execution
)

// ============================================================================
// Chat state mutation helpers
//
// These mutate the App's chat log and rebuild the Chat viewport content.
// They run inside the single-threaded Bubble Tea Update loop.
// ============================================================================

// addStaticEntry appends a pre-rendered string to the chat.
func (a *App) addStaticEntry(text string) {
	a.entries = append(a.entries, chatEntry{
		staticText: text,
		entryKind:  chatEntryStatic,
	})
	a.rebuildChat()
}

// addAssistantEntry appends a live assistant message component and records its
// index as the streaming target. Returns the index.
func (a *App) addAssistantEntry(item *components.AssistantMessageComponent) int {
	a.entries = append(a.entries, chatEntry{
		item:      item,
		entryKind: chatEntryAssistant,
	})
	a.streamingIdx = len(a.entries) - 1
	a.rebuildChat()
	return a.streamingIdx
}

// addToolEntry appends a tool execution component, indexed by toolCallID.
// If an entry for the ID already exists, it returns that index instead.
func (a *App) addToolEntry(toolCallID string, item *components.ToolExecutionComponent) int {
	if idx, ok := a.toolComponents[toolCallID]; ok {
		return idx
	}
	a.entries = append(a.entries, chatEntry{
		item:       item,
		toolCallID: toolCallID,
		entryKind:  chatEntryTool,
	})
	idx := len(a.entries) - 1
	a.toolComponents[toolCallID] = idx
	a.rebuildChat()
	return idx
}

// toolEntry returns the ToolExecutionComponent for a toolCallID, or nil.
func (a *App) toolEntry(toolCallID string) *components.ToolExecutionComponent {
	idx, ok := a.toolComponents[toolCallID]
	if !ok {
		return nil
	}
	if idx < 0 || idx >= len(a.entries) {
		return nil
	}
	item, ok := a.entries[idx].item.(*components.ToolExecutionComponent)
	if !ok {
		return nil
	}
	return item
}

// removeToolEntry drops the tool-call index mapping (called when a tool finishes).
func (a *App) removeToolEntry(toolCallID string) {
	delete(a.toolComponents, toolCallID)
}

// finishStreaming clears the streaming-assistant tracking.
func (a *App) finishStreaming() {
	a.streamingIdx = -1
	a.isStreaming = false
	a.workingVisible = false
}

// rebuildChat re-renders every entry into the Chat viewport.
// Called after any mutation to the entries slice.
func (a *App) rebuildChat() {
	// Allocate content width (chat spans full terminal width).
	width := a.width
	if width <= 0 {
		width = 80
	}

	rendered := make([]string, 0, len(a.entries))
	for i := range a.entries {
		e := &a.entries[i]
		if e.item != nil {
			// Propagate width to sizable items before rendering.
			if s, ok := e.item.(components.SizableChatItem); ok {
				s.SetWidth(width)
			}
			view := e.item.View()
			if strings.TrimSpace(view) != "" {
				rendered = append(rendered, view)
			}
		} else if e.staticText != "" {
			rendered = append(rendered, e.staticText)
		}
	}
	a.chat.SetMessages(rendered)
}

// clearChat empties the chat log (used after compaction rebuilds).
func (a *App) clearChat() {
	a.entries = nil
	a.streamingIdx = -1
	a.toolComponents = make(map[string]int)
	a.bashComponents = make(map[string]*components.BashExecutionComponent)
	a.chat.SetMessages(nil)
}

// ============================================================================
// User message rendering
// ============================================================================

// addUserText appends a user message bubble to the chat.
func (a *App) addUserText(text string) {
	msg := components.NewUserMessageComponent(text, a.theme)
	(&msg).SetWidth(a.width)
	a.addStaticEntry(msg.View())
}

// ============================================================================
// Tool execution component factory
// ============================================================================

// newToolComponent builds a ToolExecutionComponent for the given tool call.
// For bash tool calls, it also creates a live BashExecutionComponent for
// streaming output and registers it under the tool call ID.
func (a *App) newToolComponent(toolName, toolCallID string, args any) *components.ToolExecutionComponent {
	// Bash tool calls get a streaming bash component registered for live output.
	if strings.EqualFold(toolName, "bash") {
		command := bashCommandFromArgs(args)
		exclude := false
		be := components.NewBashExecutionComponent(command, a.theme, exclude)
		be.SetWidth(a.width)
		a.bashComponents[toolCallID] = be
	}

	return components.NewToolExecutionComponent(
		toolName, toolCallID, args,
		components.ToolExecutionOptions{ShowImages: true, ImageWidthCells: 60},
		nil, // custom tool definition lookup deferred
		a.theme,
		a.session.SessionManager.GetCwd(),
	)
}

// bashCommandFromArgs extracts the command string from a bash tool's args.
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

// bashComponent returns the live BashExecutionComponent for a tool call ID.
func (a *App) bashComponent(toolCallID string) *components.BashExecutionComponent {
	return a.bashComponents[toolCallID]
}

// ============================================================================
// Submit handling — sends a prompt to the agent
// ============================================================================

// handleSubmitPrompt renders the user's message in the chat and dispatches it
// to the agent session. Returns a tea.Cmd that re-subscribes to events.
func (a *App) handleSubmitPrompt(text string) tea.Cmd {
	a.addUserText(text)
	a.isStreaming = true
	a.workingMessage = "Thinking..."
	a.workingVisible = true

	// Send the prompt to the agent session (fire and forget on a goroutine).
	go func() {
		_ = a.session.Prompt(text, nil)
	}()

	return nil
}
