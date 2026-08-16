package components

import (
	"encoding/json"
	"fmt"

	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/coding-agent/src/core/tools"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// ToolExecutionOptions configures tool execution rendering.
type ToolExecutionOptions struct {
	ShowImages      bool
	ImageWidthCells int
}

// ToolExecutionComponent renders a tool call and its result with:
//   - Pending state: toolPendingBg + spinner
//   - Success state: toolSuccessBg + output
//   - Error state: toolErrorBg + error message
//   - Custom renderers from ToolDefinition (renderCall/renderResult)
//   - Expand/collapse for long output
//
// Port of reference/coding-agent/src/modes/interactive/components/tool-execution.ts
type ToolExecutionComponent struct {
	theme       *theme.Theme
	toolName    string
	toolCallID  string
	args        any
	cwd         string
	width       int

	// Configuration
	showImages      bool
	imageWidthCells int

	// State
	expanded         bool
	isPartial        bool
	executionStarted bool
	argsComplete     bool
	result           *toolResult

	// Tool definitions (custom + built-in)
	toolDefinition       *extensions.ToolDefinition
	builtInToolDefinition *extensions.ToolDefinition

	// Visibility
	hideComponent bool
}

// toolResult holds the rendered tool result.
type toolResult struct {
	content []any
	isError bool
	details any
}

// NewToolExecutionComponent creates a new tool execution component.
func NewToolExecutionComponent(
	toolName, toolCallID string,
	args any,
	options ToolExecutionOptions,
	toolDefinition *extensions.ToolDefinition,
	t *theme.Theme,
	cwd string,
) *ToolExecutionComponent {
	showImages := true
	imageWidth := 60
	if options.ShowImages == false {
		showImages = false
	}
	if options.ImageWidthCells > 0 {
		imageWidth = options.ImageWidthCells
	}

	c := &ToolExecutionComponent{
		theme:           t,
		toolName:        toolName,
		toolCallID:      toolCallID,
		args:            args,
		cwd:             cwd,
		showImages:      showImages,
		imageWidthCells: imageWidth,
		isPartial:       true,
		toolDefinition:  toolDefinition,
	}

	// Look up built-in tool definition
	defs := tools.CreateAllToolDefinitions(cwd, nil)
	if def, ok := defs[tools.ToolName(toolName)]; ok {
		c.builtInToolDefinition = def
	}

	return c
}

// SetWidth updates the available width.
func (c *ToolExecutionComponent) SetWidth(width int) {
	c.width = width
}

// UpdateArgs updates the tool call arguments during streaming.
func (c *ToolExecutionComponent) UpdateArgs(args any) {
	c.args = args
}

// MarkExecutionStarted marks that the tool has begun executing.
func (c *ToolExecutionComponent) MarkExecutionStarted() {
	c.executionStarted = true
}

// SetArgsComplete marks that all arguments have been received.
func (c *ToolExecutionComponent) SetArgsComplete() {
	c.argsComplete = true
}

// UpdateResult sets the tool result and marks it complete (or partial).
func (c *ToolExecutionComponent) UpdateResult(content []any, details any, isError bool, isPartial bool) {
	c.result = &toolResult{
		content: content,
		isError: isError,
		details: details,
	}
	c.isPartial = isPartial
}

// SetExpanded toggles the expanded state.
func (c *ToolExecutionComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
}

// SetShowImages toggles image display.
func (c *ToolExecutionComponent) SetShowImages(show bool) {
	c.showImages = show
}

// SetImageWidthCells updates the image display width.
func (c *ToolExecutionComponent) SetImageWidthCells(width int) {
	if width < 1 {
		width = 1
	}
	c.imageWidthCells = width
}

// View renders the tool execution component.
func (c ToolExecutionComponent) View() string {
	width := c.width
	if width <= 0 {
		width = 80
	}

	// Determine background based on state
	bgKey := "toolPendingBg"
	if !c.isPartial {
		if c.result != nil && c.result.isError {
			bgKey = "toolErrorBg"
		} else {
			bgKey = "toolSuccessBg"
		}
	}

	// Try custom renderer, fall back to default formatting
	rendered := c.tryCustomRenderers()
	if rendered == "" {
		rendered = c.defaultFormat()
	}

	if rendered == "" {
		c.hideComponent = true
		return ""
	}

	// Apply background
	rendered = c.theme.Bg(theme.ThemeBg(bgKey), rendered)

	return rendered
}

// --- Internal ---

// hasRendererDefinition checks if either a built-in or custom tool definition exists.
func (c *ToolExecutionComponent) hasRendererDefinition() bool {
	return c.builtInToolDefinition != nil || c.toolDefinition != nil
}

// getCallRenderer returns the call renderer from the most specific definition.
func (c *ToolExecutionComponent) getCallRenderer() func(args any) any {
	if c.builtInToolDefinition != nil && c.builtInToolDefinition.RenderCall != nil {
		return c.builtInToolDefinition.RenderCall
	}
	if c.toolDefinition != nil && c.toolDefinition.RenderCall != nil {
		return c.toolDefinition.RenderCall
	}
	return nil
}

// getResultRenderer returns the result renderer from the most specific definition.
func (c *ToolExecutionComponent) getResultRenderer() func(args any, result any, options any) any {
	if c.builtInToolDefinition != nil && c.builtInToolDefinition.RenderResult != nil {
		return c.builtInToolDefinition.RenderResult
	}
	if c.toolDefinition != nil && c.toolDefinition.RenderResult != nil {
		return c.toolDefinition.RenderResult
	}
	return nil
}

// tryCustomRenderers attempts to render using the tool's custom renderers.
// Returns empty string if no renderer or on error.
func (c ToolExecutionComponent) tryCustomRenderers() string {
	if !c.hasRendererDefinition() {
		return ""
	}

	var parts []string

	// Call renderer
	callRenderer := c.getCallRenderer()
	if callRenderer != nil {
		defer func() { _ = recover() }()
		result := callRenderer(c.args)
		if s, ok := result.(string); ok && s != "" {
			parts = append(parts, s)
		}
	} else {
		// Fallback: bold tool name
		parts = append(parts, c.theme.Fg("toolTitle", c.theme.Bold(c.toolName)))
	}

	// Result renderer
	if c.result != nil {
		resultRenderer := c.getResultRenderer()
		if resultRenderer != nil {
			defer func() { _ = recover() }()
			result := resultRenderer(
				c.args,
				map[string]any{"content": c.result.content, "details": c.result.details},
				map[string]any{"expanded": c.expanded, "isPartial": c.isPartial},
			)
			if s, ok := result.(string); ok && s != "" {
				parts = append(parts, s)
			}
		} else {
			// Fallback: text output
			output := c.getTextOutput()
			if output != "" {
				parts = append(parts, c.theme.Fg("toolOutput", output))
			}
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// defaultFormat renders the tool execution without custom renderers.
func (c ToolExecutionComponent) defaultFormat() string {
	text := c.theme.Fg("toolTitle", c.theme.Bold(c.toolName))

	// Args preview (pretty-printed JSON)
	if c.args != nil {
		if content, err := json.MarshalIndent(c.args, "", "  "); err == nil && len(content) > 0 && string(content) != "null" {
			text += "\n\n" + string(content)
		}
	}

	// Result output
	output := c.getTextOutput()
	if output != "" {
		text += "\n" + c.theme.Fg("toolOutput", output)
	}

	return text
}

// getTextOutput extracts text output from the result using the tools package.
func (c ToolExecutionComponent) getTextOutput() string {
	if c.result == nil {
		return ""
	}
	result := &tools.ToolResult{Content: c.result.content}
	return tools.GetTextOutput(result, c.showImages)
}

// String returns a debug representation.
func (c ToolExecutionComponent) String() string {
	return fmt.Sprintf("ToolExecution{name: %s, partial: %v, hasResult: %v}",
		c.toolName, c.isPartial, c.result != nil)
}
