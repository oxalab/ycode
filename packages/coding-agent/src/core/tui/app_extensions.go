package tui

// Phase 18.4: Extension widget container state.
//
// This file owns the App-side state for extension-provided widget surfaces —
// string-line content rendered above and/or below the editor. It is the Go
// analogue of the TS widget system (interactive-mode.ts:1718-1837):
//
//   - setExtensionWidget / removeExtensionWidget / clearExtensionWidgets
//   - line truncation at MAX_WIDGET_LINES with a "... (widget truncated)" notice
//   - renderExtensionWidgets, called by the view layer (Phase 18.5)
//
// ============================================================================
// Scope + honest deferrals
// ============================================================================
//
// The TS reference supports widgets in three forms and several related surfaces
// that are NOT portable to this Go architecture:
//
//  1. String-array widgets          → PORTED (below).
//  2. Component-factory widgets     → NOT PORTABLE: the factory returns a pi-tui
//     `Component`, which has no Go equivalent an extension could construct.
//  3. Custom header/footer factories → NOT PORTABLE for the same reason, and they
//     would have to cross the extension plugin boundary as a Go tea.Model.
//  4. Extension terminal-input subscribers → DEFERRED: requires a new
//     `ExtensionUIContext` field (e.g. onTerminalInput) that does not exist yet.
//
// SDK GAP: the extension SDK's ExtensionUIContext (core/extensions/runner.go)
// currently exposes only Select/Confirm/Input/Notify/Status. It does NOT yet
// expose setWidget, so these containers have no data source today. Adding a
// SetWidget field to ExtensionUIContext (and feeding it from extensions) is the
// prerequisite SDK change that will turn this infrastructure live — tracked as a
// later-phase task. The infrastructure is built now so that wiring is a one-line
// callback, not a redesign.

import (
	"strings"

	"ycode/packages/coding-agent/src/core/extensions"
)

// maxExtensionWidgetLines caps the total rendered lines per widget, matching the
// TS InteractiveMode.MAX_WIDGET_LINES constant.
const maxExtensionWidgetLines = 10

// extensionWidget is a single registered widget: an ordered key (for replacement
// and removal across both placements) and its (truncated) rendered lines.
type extensionWidget struct {
	key   string
	lines []string
}

// setExtensionWidget installs (or, when lines is nil, removes) the widget under
// key at the given placement. A key collides across both placements: setting it
// first removes any prior instance in either, matching the TS removeExisting
// (above) + removeExisting (below) behaviour.
func (a *App) setExtensionWidget(key string, lines []string, placement extensions.WidgetPlacement) {
	a.removeExtensionWidget(key)
	if lines == nil {
		return // removal only
	}
	w := extensionWidget{key: key, lines: truncateWidgetLines(lines)}
	if placement == extensions.WidgetPlacementBelowEditor {
		a.widgetsBelow = append(a.widgetsBelow, w)
	} else {
		// Default placement is aboveEditor, matching the TS default.
		a.widgetsAbove = append(a.widgetsAbove, w)
	}
}

// removeExtensionWidget removes the widget with the given key from either
// placement (no-op if absent).
func (a *App) removeExtensionWidget(key string) {
	a.widgetsAbove = removeWidgetByKey(a.widgetsAbove, key)
	a.widgetsBelow = removeWidgetByKey(a.widgetsBelow, key)
}

// clearExtensionWidgets removes every widget from both placements.
func (a *App) clearExtensionWidgets() {
	a.widgetsAbove = nil
	a.widgetsBelow = nil
}

// removeWidgetByKey returns the slice with the keyed widget dropped. It builds a
// fresh slice to avoid aliasing the original backing array.
func removeWidgetByKey(ws []extensionWidget, key string) []extensionWidget {
	out := make([]extensionWidget, 0, len(ws))
	for _, w := range ws {
		if w.key != key {
			out = append(out, w)
		}
	}
	return out
}

// truncateWidgetLines caps the line count at maxExtensionWidgetLines and appends
// a truncation notice when content is longer — matching the TS
// content.slice(0, MAX) + "... (widget truncated)" behaviour.
func truncateWidgetLines(lines []string) []string {
	if len(lines) <= maxExtensionWidgetLines {
		return lines
	}
	out := make([]string, 0, maxExtensionWidgetLines+1)
	out = append(out, lines[:maxExtensionWidgetLines]...)
	out = append(out, "... (widget truncated)")
	return out
}

// renderExtensionWidgets renders the widgets for a placement as a single
// newline-joined block, preserving insertion order (the TS Map iteration order).
// Returns "" when there are none, so the view layer can omit the section.
func (a App) renderExtensionWidgets(placement extensions.WidgetPlacement) string {
	var widgets []extensionWidget
	if placement == extensions.WidgetPlacementBelowEditor {
		widgets = a.widgetsBelow
	} else {
		widgets = a.widgetsAbove
	}
	if len(widgets) == 0 {
		return ""
	}
	var b strings.Builder
	for _, w := range widgets {
		for _, line := range w.lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// hasExtensionWidgets reports whether any widget exists in the given placement,
// so the view layer can decide whether to reserve space for the section.
func (a App) hasExtensionWidgets(placement extensions.WidgetPlacement) bool {
	if placement == extensions.WidgetPlacementBelowEditor {
		return len(a.widgetsBelow) > 0
	}
	return len(a.widgetsAbove) > 0
}

// renderWidgetsAbove / renderWidgetsBelow are view-layer conveniences that pin
// the placement, keeping app_view.go free of an extensions package import.
func (a App) renderWidgetsAbove() string {
	return a.renderExtensionWidgets(extensions.WidgetPlacementAboveEditor)
}

func (a App) renderWidgetsBelow() string {
	return a.renderExtensionWidgets(extensions.WidgetPlacementBelowEditor)
}
