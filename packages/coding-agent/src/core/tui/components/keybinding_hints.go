package components

import (
	"runtime"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

// KeyTextFormatOptions controls formatting of keybinding text.
type KeyTextFormatOptions struct {
	Capitalize bool
}

// formatKeyPart formats a single key part (e.g., "ctrl", "alt", "enter").
// On macOS, "alt" is displayed as "option".
func formatKeyPart(part string, options KeyTextFormatOptions) string {
	display := part
	if runtime.GOOS == "darwin" && strings.EqualFold(part, "alt") {
		display = "option"
	}
	if options.Capitalize && len(display) > 0 {
		display = strings.ToUpper(display[:1]) + display[1:]
	}
	return display
}

// FormatKeyText formats a keybinding string for display.
// Keys are split by "/" (alternatives) and "+" (chords).
// Example: "ctrl+c/q" → "ctrl+c/q" or "Ctrl+C/Q" (with capitalize).
func FormatKeyText(key string, options KeyTextFormatOptions) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		chords := strings.Split(part, "+")
		for j, chord := range chords {
			chords[j] = formatKeyPart(chord, options)
		}
		parts[i] = strings.Join(chords, "+")
	}
	return strings.Join(parts, "/")
}

// KeyHint returns a formatted keybinding hint with theme styling.
// Example: KeyHint(t, "ctrl+c", "to cancel") → dim "ctrl+c" + muted " to cancel"
func KeyHint(t *theme.Theme, key string, description string) string {
	return t.Fg("dim", FormatKeyText(key, KeyTextFormatOptions{})) +
		t.Fg("muted", " "+description)
}

// RawKeyHint returns a formatted keybinding hint using a raw key string.
// Same as KeyHint but takes a pre-formatted key string.
func RawKeyHint(t *theme.Theme, key string, description string) string {
	return t.Fg("dim", FormatKeyText(key, KeyTextFormatOptions{})) +
		t.Fg("muted", " "+description)
}

// KeyDisplayText formats a keybinding with capitalized parts.
// Example: "ctrl+c" → "Ctrl+C"
func KeyDisplayText(key string) string {
	return FormatKeyText(key, KeyTextFormatOptions{Capitalize: true})
}
