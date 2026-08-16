package components

import (
	"fmt"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
	llm "ycode/packages/llm/src"
)

// OSC 133 zone markers for terminal integration (shell integration).
const (
	osc133ZoneStart = "\x1b]133;A\x07"
	osc133ZoneEnd   = "\x1b]133;B\x07"
	osc133ZoneFinal = "\x1b]133;C\x07"
)

// AssistantMessageComponent renders a complete assistant message including:
//   - Text blocks rendered as markdown
//   - Thinking blocks (shown or hidden based on settings)
//   - Abort/error messages
//   - OSC 133 zone markers for terminal shell integration
//
// Port of reference/coding-agent/src/modes/interactive/components/assistant-message.ts
type AssistantMessageComponent struct {
	theme  *theme.Theme
	width  int

	// Configuration
	hideThinkingBlock    bool
	hiddenThinkingLabel  string

	// State — the message being rendered
	message    *llm.AssistantMessage
	hasToolCalls bool
}

// NewAssistantMessageComponent creates a new assistant message component.
func NewAssistantMessageComponent(
	t *theme.Theme,
	hideThinkingBlock bool,
	hiddenThinkingLabel string,
) *AssistantMessageComponent {
	if hiddenThinkingLabel == "" {
		hiddenThinkingLabel = "Thinking..."
	}
	return &AssistantMessageComponent{
		theme:               t,
		hideThinkingBlock:   hideThinkingBlock,
		hiddenThinkingLabel: hiddenThinkingLabel,
	}
}

// SetWidth updates the component's available width.
func (a *AssistantMessageComponent) SetWidth(width int) {
	a.width = width
}

// SetHideThinkingBlock updates whether thinking blocks are shown or hidden.
func (a *AssistantMessageComponent) SetHideThinkingBlock(hide bool) {
	a.hideThinkingBlock = hide
}

// SetHiddenThinkingLabel updates the label shown when thinking is hidden.
func (a *AssistantMessageComponent) SetHiddenThinkingLabel(label string) {
	a.hiddenThinkingLabel = label
}

// UpdateContent sets the message to render and rebuilds the view.
func (a *AssistantMessageComponent) UpdateContent(msg llm.AssistantMessage) {
	a.message = &msg
	a.hasToolCalls = hasToolCalls(msg.Content)
}

// View renders the assistant message as a string.
func (a AssistantMessageComponent) View() string {
	if a.message == nil || len(a.message.Content) == 0 {
		return ""
	}

	var sections []string

	// Render each content block in order
	for i, block := range a.message.Content {
		switch b := block.(type) {
		case llm.TextContent:
			text := strings.TrimSpace(b.Text)
			if text == "" {
				continue
			}
			rendered := RenderMarkdown(text, a.theme)
			sections = append(sections, rendered)

		case llm.ThinkingContent:
			thinking := strings.TrimSpace(b.Thinking)
			if thinking == "" {
				continue
			}

			// Check if there's visible content after this block
			hasVisibleAfter := hasVisibleContentAfter(a.message.Content, i+1)

			if a.hideThinkingBlock {
				// Show static label when thinking is hidden
				label := a.theme.Italic(a.theme.Fg("thinkingText", a.hiddenThinkingLabel))
				sections = append(sections, label)
				if hasVisibleAfter {
					sections = append(sections, "")
				}
			} else {
				// Show thinking content in thinkingText color, italic
				thinkingMd := RenderMarkdown(thinking, a.theme)
				// Wrap in thinkingText color + italic
				thinkingStyled := a.theme.Italic(a.theme.Fg("thinkingText", thinkingMd))
				sections = append(sections, thinkingStyled)
				if hasVisibleAfter {
					sections = append(sections, "")
				}
			}
		}
	}

	// Show abort/error if no tool calls (tool execution components handle errors with tool calls)
	if !a.hasToolCalls && a.message.StopReason != "" {
		switch a.message.StopReason {
		case llm.StopReasonAborted:
			abortMsg := "Operation aborted"
			if a.message.ErrorMessage != "" && a.message.ErrorMessage != "Request was aborted" {
				abortMsg = a.message.ErrorMessage
			}
			sections = append(sections, "")
			sections = append(sections, a.theme.Fg("error", abortMsg))
		case llm.StopReasonError:
			errMsg := "Unknown error"
			if a.message.ErrorMessage != "" {
				errMsg = a.message.ErrorMessage
			}
			sections = append(sections, "")
			sections = append(sections, a.theme.Fg("error", fmt.Sprintf("Error: %s", errMsg)))
		}
	}

	if len(sections) == 0 {
		return ""
	}

	result := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Add OSC 133 zone markers (only if no tool calls — tool executions manage their own markers)
	if !a.hasToolCalls {
		result = osc133ZoneStart + result + osc133ZoneEnd + osc133ZoneFinal
	}

	return result
}

// --- Internal helpers ---

// hasToolCalls checks if the message content contains any tool call blocks.
func hasToolCalls(content []llm.ContentBlock) bool {
	for _, block := range content {
		if _, ok := block.(llm.ToolCall); ok {
			return true
		}
	}
	return false
}

// hasVisibleContentAfter checks if there's visible text/thinking content after the given index.
func hasVisibleContentAfter(content []llm.ContentBlock, startIndex int) bool {
	for i := startIndex; i < len(content); i++ {
		switch b := content[i].(type) {
		case llm.TextContent:
			if strings.TrimSpace(b.Text) != "" {
				return true
			}
		case llm.ThinkingContent:
			if strings.TrimSpace(b.Thinking) != "" {
				return true
			}
		}
	}
	return false
}
