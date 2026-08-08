package interactive

// keybinding_hints.go ports reference/coding-agent/src/modes/interactive/
// components/keybinding-hints.ts (verified — see mapping/10-chrome.md). Pure
// formatting helpers that turn keybinding actions into dim/muted hint strings,
// used by the header (and later the footer, bash-execution, selectors).

import (
	"runtime"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"
	"ycode/packages/tui"
)

// formatKeyPart lower-cases a single key part and maps "alt" → "option" on macOS
// (reference keybinding-hints.ts:12-15).
func formatKeyPart(part string) string {
	if runtime.GOOS == "darwin" && strings.EqualFold(part, "alt") {
		return "option"
	}
	return part
}

// formatKeyText splits a key spec on "/" then "+" and formats each part
// (reference formatKeyText). e.g. "ctrl+c/ctrl+d" → "ctrl+c/ctrl+d".
func formatKeyText(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		sub := strings.Split(p, "+")
		for j, s := range sub {
			sub[j] = formatKeyPart(s)
		}
		parts[i] = strings.Join(sub, "+")
	}
	return strings.Join(parts, "/")
}

// formatKeys formats the resolved keys for an action (reference formatKeys).
func formatKeys(keys []tui.KeyID) string {
	if len(keys) == 0 {
		return ""
	}
	ss := make([]string, len(keys))
	for i, k := range keys {
		ss[i] = string(k)
	}
	return formatKeyText(strings.Join(ss, "/"))
}

// keyText returns the formatted key chord for an action, resolved from the
// global (merged) KeybindingsManager (reference keyText).
func keyText(kb tui.Keybinding) string {
	return formatKeys(tui.GetKeybindings().GetKeys(kb))
}

// keyHint renders "‹dim key› ‹muted description›" (reference keyHint).
func keyHint(th *theme.Theme, kb tui.Keybinding, description string) string {
	return th.Fg("dim", keyText(kb)) + th.Fg("muted", " "+description)
}

// rawKeyHint renders a hint from a literal key string (reference rawKeyHint).
func rawKeyHint(th *theme.Theme, key, description string) string {
	return th.Fg("dim", formatKeyText(key)) + th.Fg("muted", " "+description)
}
