//go:build darwin

package tui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics

#include <CoreGraphics/CoreGraphics.h>

static int cg_modifier_pressed(unsigned long long mask) {
    return (CGEventSourceFlagsState(kCGEventSourceStateCombinedSessionState) & mask) != 0;
}
*/
import "C"

// macOS CGEventFlags masks for modifier polling (Apple-Terminal shift+enter).
const (
	cgShift   = uint64(C.kCGEventFlagMaskShift)
	cgCommand = uint64(C.kCGEventFlagMaskCommand)
	cgControl = uint64(C.kCGEventFlagMaskControl)
	cgOption  = uint64(C.kCGEventFlagMaskAlternate)
)

// IsNativeModifierPressed reports whether a macOS modifier is currently held,
// via CGEventSourceFlagsState. Returns false for unknown modifier names.
func IsNativeModifierPressed(modifier string) bool {
	var mask uint64
	switch modifier {
	case "shift":
		mask = cgShift
	case "command":
		mask = cgCommand
	case "control":
		mask = cgControl
	case "option":
		mask = cgOption
	default:
		return false
	}
	return C.cg_modifier_pressed(C.ulonglong(mask)) != 0
}
