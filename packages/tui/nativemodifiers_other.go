//go:build !darwin

package tui

// On non-darwin platforms there is no native modifier-state polling, so
// Apple-Terminal shift+enter cannot be detected (see CAVEATS). Returns false.

// IsNativeModifierPressed reports whether a macOS modifier (shift/command/control/
// option) is currently held. Only meaningfully implemented on darwin.
func IsNativeModifierPressed(modifier string) bool { return false }
