# 14 — Platform-native code (Windows VT input, macOS modifiers)

The TS ships two Node-API native addons (C) in `native/`. Go replaces them with
stdlib/syscall + optional CGo.

---

## 14a. Windows — `ENABLE_VIRTUAL_TERMINAL_INPUT`

- **TS source:** [`native/win32/src/win32-console-mode.c`](../../reference/tui/native/win32/src/win32-console-mode.c) (53 LOC)
- **Go target:** `terminal_windows.go` (`//go:build windows`) inside `terminal.go`'s
  package.

**What it does:** on the console input handle, ORs `ENABLE_VIRTUAL_TERMINAL_INPUT`
(0x0200) into the current console mode via `SetConsoleMode`. Without it, Windows
ReadConsoleInput discards modifier state (Shift+Tab arrives as plain `\t`).

**Go port — pure stdlib, no CGo:**

```go
//go:build windows
package tui

import "golang.org/x/sys/windows"

func enableWindowsVTInput() bool {
    h := windows.Handle(os.Stdin.Fd())   // GetStdHandle(STD_INPUT_HANDLE)
    var mode uint32
    if err := windows.GetConsoleMode(windows.Handle(h), &mode); err != nil { return false }
    const ENABLE_VIRTUAL_TERMINAL_INPUT = 0x0200
    return windows.SetConsoleMode(windows.Handle(h), mode|ENABLE_VIRTUAL_TERMINAL_INPUT) == nil
}
```

(`golang.org/x/sys/windows` is transitively available; `x/sys` is in go.mod.)

> **If we back ProcessTerminal with Bubble Tea** ([03-terminal.md](03-terminal.md)),
  tea already sets this flag — the manual call becomes unnecessary. Keep the function
  as a thin shim for the direct-stdio path / tests.

Prebuilds exist for `win32-x64` and `win32-arm64` — both covered by `x/sys/windows`
(x64 and arm64). Add a `terminal_other.go` (`//go:build !windows`) no-op stub.

## 14b. macOS — CGEventFlags modifier polling

- **TS source:** [`native/darwin/src/darwin-modifiers.c`](../../reference/tarwin/native/darwin/src/darwin-modifiers.c) → `native/darwin/src/darwin-modifiers.c` (70 LOC)
- **Go target:** `nativemodifiers_darwin.go` (`//go:build darwin`), CGo.

**What it does:** `isModifierPressed(name)` reads
`CGEventSourceFlagsState(kCGEventSourceStateCombinedSessionState, ...)` and masks:
shift=`kCGEventFlagMaskShift`, command=`kCGEventFlagMaskCommand`,
control=`kCGEventFlagMaskControl`, option=`kCGEventFlagMaskAlternate`.

**Go port (CGo to CoreGraphics):**

```go
//go:build darwin
package tui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>

static int cgModifierPressed(CGEventFlags mask) {
    return (CGEventSourceFlagsState(kCGEventSourceStateCombinedSessionState) & mask) != 0;
}
*/
import "C"

const (
    maskShift   = C.CGEventFlags(C.kCGEventFlagMaskShift)
    maskCommand = C.CGEventFlags(C.kCGEventFlagMaskCommand)
    maskControl = C.CGEventFlags(C.kCGEventFlagMaskControl)
    maskOption  = C.CGEventFlags(C.kCGEventFlagMaskAlternate)
)

func IsNativeModifierPressed(key ModifierKey) bool {
    var m C.CGEventFlags
    switch key { /* map name → mask */ }
    return C.cgModifierPressed(m) != 0
}
```

Supported arches: darwin-x64 and darwin-arm64 — both build with the same CGo.

**Trade-off:** CGo adds a build dependency (Xcode/clang) for a macOS-only,
Apple-Terminal-only feature. **Lower priority.** Acceptable first-cut: ship
`nativemodifiers_other.go`-style stub returning false on darwin too, and document
that Apple-Terminal shift-enter detection degrades (shift+enter may not register on
that one terminal). Add CGo later if needed.

## Prebuilt binaries in reference (informational)
`native/{darwin,win32}/prebuilds/*/*.node` — Node-API prebuilds for
darwin-{arm64,x64} and win32-{arm64,x64}. **Not needed in Go** — delete from scope;
the Go ports above replace them entirely.

## Tests to port
- No standalone tests. The Windows 0x08-over-SSH behavior is covered by
  `keys.test.ts` (`isWindowsTerminalSession` env-mocked). macOS modifier behavior is
  not unit-tested in TS.
