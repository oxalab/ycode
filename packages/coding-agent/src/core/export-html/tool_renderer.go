package exporthtml

import (
	"encoding/json"
	"fmt"

	"ycode/packages/llm/src"
)

// ToolRenderer renders tool call/result pairs as styled HTML blocks.
// Mirrors TS ToolHtmlRenderer (tool-renderer.ts:25-27).
type ToolRenderer interface {
	RenderCall(toolName string, args any) string
	RenderResult(toolName string, result any, isError bool) string
}

// ToolRendererDeps provides dependencies for creating a ToolRenderer.
// Mirrors TS ToolHtmlRendererDeps (tool-renderer.ts:14-17).
type ToolRendererDeps struct {
	GetToolDefinition func(name string) *ToolDef
	Cwd               string
	Width             int
}

// ToolDef is a minimal tool definition for rendering purposes.
type ToolDef struct {
	Name        string
	Label       string
	Description string
}

// CreateToolRenderer creates a new ToolRenderer from the given deps.
// Mirrors TS createToolRenderer() (tool-renderer.ts:58).
func CreateToolRenderer(deps ToolRendererDeps) ToolRenderer {
	return &defaultToolRenderer{deps: deps}
}

type defaultToolRenderer struct {
	deps ToolRendererDeps
}

func (r *defaultToolRenderer) RenderCall(toolName string, args any) string {
	label := toolName
	if def := r.deps.GetToolDefinition(toolName); def != nil && def.Label != "" {
		label = def.Label
	}

	argsJSON, _ := json.MarshalIndent(args, "", "  ")

	return fmt.Sprintf(`<div class="tool-call">
<div class="tool-call-header"><span class="tool-name">%s</span></div>
<pre class="tool-call-args"><code>%s</code></pre>
</div>`, escapeHtml(label), escapeHtml(string(argsJSON)))
}

func (r *defaultToolRenderer) RenderResult(toolName string, result any, isError bool) string {
	errorClass := ""
	if isError {
		errorClass = " tool-result-error"
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	if len(resultJSON) > 10000 {
		resultJSON = resultJSON[:10000]
		resultJSON = append(resultJSON, []byte("\n... (truncated)")...)
	}

	// Check if result contains ANSI codes and render them
	resultStr := string(resultJSON)
	if containsAnsi(resultStr) {
		resultStr = AnsiToHtml(resultStr)
	} else {
		resultStr = escapeHtml(resultStr)
	}

	return fmt.Sprintf(`<div class="tool-result%s">
<pre><code>%s</code></pre>
</div>`, errorClass, resultStr)
}

// containsAnsi checks if a string contains ANSI escape sequences.
func containsAnsi(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '\x1b' && s[i+1] == '[' {
			return true
		}
	}
	return false
}

// NoOpRenderer is a ToolRenderer that produces minimal output.
var NoOpRenderer ToolRenderer = &noOpRenderer{}

type noOpRenderer struct{}

func (n *noOpRenderer) RenderCall(toolName string, args any) string {
	return fmt.Sprintf(`<div class="tool-call"><span class="tool-name">%s</span></div>`, escapeHtml(toolName))
}

func (n *noOpRenderer) RenderResult(toolName string, result any, isError bool) string {
	return ""
}

// Ensure llm import is used (for potential future ContentBlock rendering)
var _ llm.ContentBlock = llm.TextContent{}
