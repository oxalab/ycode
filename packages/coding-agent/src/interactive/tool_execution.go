package interactive

// tool_execution.go ports reference components/tool-execution.ts (verified — see
// mapping/12-tools.md), P3b slice: the background-color state machine
// (toolPendingBg → toolSuccessBg/toolErrorBg) + the generic fallback renderer
// (bold tool name + JSON args + text output). Per-tool custom renderers
// (renderCall/renderResult, e.g. the edit-tool diff) and image embedding land in
// P3c — this matches the current Bubble-Tea TUI, which also uses the fallback
// renderer (custom tool-definition lookup was deferred there too).

import (
	"encoding/json"
	"fmt"
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tools"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// toolResultMaxLines caps the generic fallback's result preview (reference
// core/tools/*.ts use ~15-20 line previews per tool). Expand/collapse (ctrl+o)
// will override this.
const toolResultMaxLines = 20

// ToolExecution renders a tool call + its result with a bg-color state machine.
type ToolExecution struct {
	*tui.Container
	box      *components.Box
	text     *components.Text
	th       *theme.Theme
	toolName string
	args     any
	result   string
	isError  bool
	isPartial bool
	hidden   bool // hide when complete with no args and no result (reference :222-224,331-333)
	expanded bool // ctrl+o (app.tools.expand) shows full output vs a preview
}

// NewToolExecution builds a pending tool component.
func NewToolExecution(toolName string, args any, th *theme.Theme) *ToolExecution {
	box := components.NewBox(1, 1, toolBg(th, true, false))
	t := &ToolExecution{
		th: th, toolName: toolName, args: args, isPartial: true, box: box,
	}
	txt := components.NewText(t.format(), 0, 0, nil)
	box.AddChild(txt)
	t.text = txt

	c := &tui.Container{}
	c.AddChild(components.NewSpacer(1))
	c.AddChild(box)
	t.Container = c
	return t
}

// MarkStarted marks the tool as executing (pending bg).
func (t *ToolExecution) MarkStarted() { t.isPartial = true; t.refresh() }

// SetExpanded toggles full output vs the capped preview (reference setExpanded :201).
func (t *ToolExecution) SetExpanded(e bool) { t.expanded = e; t.refresh() }

// UpdateResult finalizes (or partially updates) the tool with its text output.
func (t *ToolExecution) UpdateResult(text string, isError, isPartial bool) {
	t.result = text
	t.isError = isError
	t.isPartial = isPartial
	t.refresh()
}

func (t *ToolExecution) refresh() {
	t.box.SetBg(toolBg(t.th, t.isPartial, t.isError))
	// Hide a completed tool that produced no args and no result (reference
	// tool-execution.ts:222-224,331-333 hides when the renderer yields no content).
	t.hidden = !t.isPartial && t.result == "" && argsJSON(t.args) == ""
	t.text.SetText(t.format())
}

// Render hides the component entirely when it has nothing to show.
func (t *ToolExecution) Render(width int) []string {
	if t.hidden {
		return []string{}
	}
	return t.Container.Render(width)
}

// format is the fallback renderer (reference formatToolExecution :340-351).
// Results that look like a unified diff (e.g. the edit tool) are colored via
// renderDiff; otherwise plain toolOutput. Output is capped to a preview with a
// "… N more lines" hint (per-tool renderers will customize this later).
func (t *ToolExecution) format() string {
	s := t.th.Fg("toolTitle", t.th.Bold(t.toolName))
	// Compact per-tool call line for known tools (read/grep/edit/write/find/ls);
	// fall back to indented JSON for unknown/MCP tools (reference core/tools/*.ts
	// renderCall). bash is rendered by its own component.
	if call := formatToolCall(t.toolName, t.args, t.expanded, t.th); call != "" {
		s += " " + call
	} else if j := argsJSON(t.args); j != "" {
		s += "\n\n" + j
	}
	// Reference formatWriteResult: write success renders nothing — the call
	// preview already shows the content. Errors keep their text.
	if t.result != "" && !(strings.EqualFold(t.toolName, "write") && !t.isError) {
		// Cap to a preview (first toolResultMaxLines lines) when collapsed; full
		// output when expanded (ctrl+o). + "… N more lines (ctrl+o to expand)" hint.
		raw := t.result
		omitted := 0
		if !t.expanded {
			if lines := strings.Split(raw, "\n"); len(lines) > toolResultMaxLines {
				omitted = len(lines) - toolResultMaxLines
				raw = strings.Join(lines[:toolResultMaxLines], "\n")
			}
		}
		out := raw
		if isDiffLike(t.result) {
			out = renderDiff(out, t.th)
		}
		s += "\n" + t.th.Fg("toolOutput", out)
		if omitted > 0 {
			s += "\n" + t.th.Fg("muted", fmt.Sprintf("... %d more lines ", omitted)) +
				"(" + keyHint(t.th, "app.tools.expand", "to expand") + ")"
		}
	}
	return s
}

// toolBg returns the background StyleFunc for the current state
// (reference updateDisplay :229-233 + getEditHeaderBg — a preview or settled
// error tints the header red even while the tool is still running).
func toolBg(th *theme.Theme, isPartial, isError bool) tui.StyleFunc {
	switch {
	case isError:
		return func(s string) string { return th.Bg("toolErrorBg", s) }
	case isPartial:
		return func(s string) string { return th.Bg("toolPendingBg", s) }
	default:
		return func(s string) string { return th.Bg("toolSuccessBg", s) }
	}
}

// argsJSON renders the tool args as indented JSON (empty on failure/nil).
func argsJSON(args any) string {
	if args == nil {
		return ""
	}
	b, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// argsMap normalizes the tool args to a map (handles map[string]any from JSON and
// typed input structs via a JSON round-trip). Returns nil if not mappable.
func argsMap(args any) map[string]any {
	if args == nil {
		return nil
	}
	if m, ok := args.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// firstStr returns the first non-empty string value among the given keys.
func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// formatToolCall renders a compact, tool-specific call line (path in accent,
// pattern as /…/, edit count, etc.) for known tools. Returns "" for unknown/MCP
// tools so the caller falls back to indented JSON (reference core/tools/*.ts
// renderCall; bash has its own component). write additionally renders the
// content preview body, so it needs the expanded flag.
func formatToolCall(name string, args any, expanded bool, th *theme.Theme) string {
	m := argsMap(args)
	if m == nil {
		return ""
	}
	accent := func(s string) string { return th.Fg("accent", s) }
	path := func(keys ...string) string { return firstStr(m, keys...) }

	switch strings.ToLower(name) {
	case "read", "view":
		if p := path("path", "file_path", "filePath"); p != "" {
			return accent(p)
		}
	case "write", "create":
		if p := path("path", "file_path", "filePath"); p != "" {
			call := accent(p)
			if body := writePreviewBody(m, p, expanded, th); body != "" {
				call += "\n\n" + body
			}
			return call
		}
	case "edit":
		if p := path("path", "file_path", "filePath"); p != "" {
			if n := editCount(m); n > 0 {
				noun := "edit"
				if n > 1 {
					noun = "edits"
				}
				return accent(p) + " " + th.Fg("muted", fmt.Sprintf("(%d %s)", n, noun))
			}
			return accent(p)
		}
	case "grep":
		pat := path("pattern")
		if pat == "" {
			return ""
		}
		s := accent("/" + pat + "/")
		if p := path("path", "glob"); p != "" {
			s += " " + accent(p)
		}
		return s
	case "find":
		pat := path("pattern")
		if pat == "" {
			return ""
		}
		s := accent(pat)
		if p := path("path"); p != "" {
			s += " " + accent(p)
		}
		return s
	case "ls":
		if p := path("path"); p != "" {
			return accent(p)
		}
		return accent(".")
	}
	return ""
}

// editCount returns the number of edits in an edit tool's args (0 if absent).
func editCount(m map[string]any) int {
	if v, ok := m["edits"].([]any); ok {
		return len(v)
	}
	return 0
}

// writePreviewBody renders the write tool's content preview (reference
// formatWriteCall): trailing empty lines trimmed, interior blanks kept,
// collapsed to 10 lines with an expand hint unless expanded, syntax-highlighted
// via the file path when a lexer matches.
func writePreviewBody(m map[string]any, path string, expanded bool, th *theme.Theme) string {
	content, ok := m["content"].(string)
	if !ok {
		return th.Fg("error", "[invalid content arg - expected string]")
	}
	if content == "" {
		return ""
	}

	display := tools.ReplaceTabs(tools.NormalizeDisplayText(content))
	var lines []string
	if highlighted := highlightWritePreview(display, path, th); highlighted != nil {
		lines = trimTrailingEmptyLines(highlighted)
	} else {
		for _, l := range strings.Split(display, "\n") {
			lines = append(lines, th.Fg("toolOutput", l))
		}
		lines = trimTrailingEmptyLines(lines)
	}

	total := len(lines)
	maxLines := total
	if !expanded && maxLines > 10 {
		maxLines = 10
	}
	body := strings.Join(lines[:maxLines], "\n")
	if remaining := total - maxLines; remaining > 0 {
		body += "\n" + th.Fg("muted", fmt.Sprintf("... (%d more lines, %d total,", remaining, total)) +
			" " + keyHint(th, "app.tools.expand", "to expand") + ")"
	}
	return body
}

// trimTrailingEmptyLines drops trailing empty lines only (reference
// trimTrailingEmptyLines) — interior blank lines are part of the content.
// Highlighted lines carry ANSI, so emptiness is judged on the stripped text.
func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansiStripRe.ReplaceAllString(lines[end-1], "")) == "" {
		end--
	}
	return lines[:end]
}
