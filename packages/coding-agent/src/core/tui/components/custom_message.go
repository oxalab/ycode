package components

import (
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
	llm "ycode/packages/llm/src"
)

// CustomMessageComponent renders a custom message entry from extensions.
// Uses distinct styling (customMessageBg, customMessageLabel) to differentiate
// from user messages. If the extension provides a custom renderer, it is used
// instead of the default label+content layout.
//
// Port of reference/coding-agent/src/modes/interactive/components/custom-message.ts
type CustomMessageComponent struct {
	theme          *theme.Theme
	message        core.CustomMessage
	customRenderer extensions.MessageRenderer
	width          int
	expanded       bool
}

// NewCustomMessageComponent creates a new custom message component.
// customRenderer may be nil — in that case the default label+content layout is used.
func NewCustomMessageComponent(
	msg core.CustomMessage,
	customRenderer extensions.MessageRenderer,
	t *theme.Theme,
) *CustomMessageComponent {
	return &CustomMessageComponent{
		theme:          t,
		message:        msg,
		customRenderer: customRenderer,
	}
}

// SetWidth updates the available width for rendering.
func (c *CustomMessageComponent) SetWidth(width int) {
	c.width = width
}

// SetExpanded toggles the expanded state (used by custom renderers).
func (c *CustomMessageComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
}

// View renders the custom message.
func (c CustomMessageComponent) View() string {
	width := c.width
	if width <= 0 {
		width = 80
	}

	// Try custom renderer first — it handles its own styling
	if c.customRenderer != nil {
		defer func() {
			// Swallow panics from extension-provided renderers, fall through to default
			_ = recover()
		}()
		rendered, err := callCustomRenderer(c.customRenderer, c.message, c.expanded)
		if err == nil && rendered != "" {
			return rendered
		}
	}

	// Default rendering: label + content in a customMessageBg box
	style := lipgloss.NewStyle().
		Padding(0, 1).
		Width(width - 2)

	// Build the label: "[customType]" in customMessageLabel color, bold
	label := c.theme.Fg("customMessageLabel", "\x1b[1m["+c.message.CustomType+"]\x1b[22m")

	// Extract text content
	text := extractCustomText(c.message.Content)
	contentMd := RenderMarkdown(text, c.theme)
	contentStyled := c.theme.Fg("customMessageText", contentMd)

	body := label + "\n" + contentStyled
	rendered := style.Render(body)

	// Apply customMessageBg background
	rendered = c.theme.Bg("customMessageBg", rendered)

	return rendered
}

// extractCustomText extracts text from a CustomMessage content field,
// which may be a plain string or a []llm.ContentBlock.
func extractCustomText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []llm.ContentBlock:
		var parts []string
		for _, block := range c {
			if tc, ok := block.(llm.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		var parts []string
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// callCustomRenderer safely invokes the extension-provided custom renderer.
// The MessageRenderer signature uses `any` types, so we adapt here.
func callCustomRenderer(
	renderer extensions.MessageRenderer,
	msg core.CustomMessage,
	expanded bool,
) (string, error) {
	opts := extensions.MessageRenderOptions{Expanded: expanded}
	result := renderer(msg, opts, nil)
	if result == nil {
		return "", nil
	}
	switch r := result.(type) {
	case string:
		return r, nil
	default:
		return "", nil
	}
}
