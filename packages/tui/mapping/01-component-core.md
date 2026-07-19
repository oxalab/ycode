# 01 — Component core (interfaces)

Maps the **interface layer** that everything else builds on.

- **TS source:** [`tui.ts`](../../reference/tui/src/tui.ts) (lines 61–290: `Component`,
  `Focusable`, `isFocusable`, `CURSOR_MARKER`, `Container`) +
  [`editor-component.ts`](../../reference/tui/src/editor-component.ts)
- **Go target:** `component.go` + `editorcomponent.go`
- **Package:** `tui` (`ycode/packages/tui`)

> The `TUI` class itself (rendering engine) is documented in
> [02-engine.md](02-engine.md). This doc covers only the data model / interfaces.

---

## `Component` interface

TS `Component` is an object with required `render`+`invalidate` and optional
`handleInput`/`wantsKeyRelease`. Go needs an interface, but `invalidate()` with a
no-op default for components that don't cache argues for an optional method.

**Decision: split into a required core interface + an optional `Invalidater`.**

```go
// Component is the core renderable unit. Every TUI element implements it.
type Component interface {
    Render(width int) []string
}

// Invalidater is implemented by components that cache render output.
// The engine calls Invalidate() on theme change / forced re-render.
type Invalidater interface {
    Invalidate()
}

// InputHandler is implemented by components that accept keyboard input when focused.
type InputHandler interface {
    HandleInput(data string)
}

// KeyReleaseSubscriber opts a component into receiving Kitty key-release events
// (default: filtered out). TS field `wantsKeyRelease?: boolean`.
type KeyReleaseSubscriber interface {
    WantsKeyRelease() bool
}
```

| TS symbol | Go symbol | Notes |
|---|---|---|
| `interface Component { render(width): string[] }` | `Component.Render(width int) []string` | |
| `Component.handleInput?(data)` | `InputHandler.HandleInput(data string)` | optional via separate interface |
| `Component.wantsKeyRelease?` | `KeyReleaseSubscriber.WantsKeyRelease() bool` | |
| `Component.invalidate()` | `Invalidater.Invalidate()` | optional via separate interface |

Engine must type-assert these optional interfaces (`if h, ok := c.(InputHandler); ok {...}`).

## `Focusable` interface (IME cursor)

```go
// Focusable components emit CURSOR_MARKER when focused so the engine can
// position the hardware cursor for IME candidate windows.
type Focusable interface {
    Focused() bool
    SetFocused(bool)
}
```

TS uses a `focused: boolean` field; Go needs getter+setter so the engine can flip it.
Container components that embed an Input/Editor must implement `Focusable` and
forward `SetFocused` to the child (see README "Container components with embedded
inputs").

| TS | Go |
|---|---|
| `interface Focusable { focused: boolean }` | `Focusable` (Focused/SetFocused) |
| `isFocusable(component)` | `func IsFocusable(c Component) (*Focusable, bool)` — type assert |

## `CURSOR_MARKER` constant

```go
// APC (Application Program Command) zero-width marker. Engine scans rendered
// output for this, strips it, and positions the hardware cursor there.
const CursorMarker = "\x1b_pi:c\x07"
```

| TS | Go |
|---|---|
| `export const CURSOR_MARKER` | `CursorMarker` (exported const) |

## `Container`

```go
type Container struct {
    Children []Component
}
func (c *Container) AddChild(child Component)
func (c *Container) RemoveChild(child Component)
func (c *Container) Clear()
func (c *Container) Invalidate()        // calls Invalidate() on children that implement it
func (c *Container) Render(width int) []string  // concatenates child renders
```

| TS | Go |
|---|---|
| `class Container implements Component` | `struct Container` with same methods |
| `children: Component[]` | `Children []Component` |
| `addChild/removeChild/clear` | `AddChild/RemoveChild/Clear` |
| `invalidate()` | `Invalidate()` (also satisfies `Invalidater`) |
| `render(width)` | `Render(width)` |

`TUI` embeds `*Container` (engine doc).

## `EditorComponent` interface (extension point)

Allows extensions to supply a custom editor (vim mode etc.) while staying
compatible with the app.

```go
type EditorComponent interface {
    Component
    InputHandler
    GetText() string
    SetText(text string)
    OnSubmit() func(string)        // see note on callbacks below
    SetOnSubmit(fn func(string))
    OnChange() func(string)
    SetOnChange(fn func(string))
    AddToHistory(text string)             // optional
    InsertTextAtCursor(text string)       // optional
    GetExpandedText() string              // optional, defaults to GetText
    SetAutocompleteProvider(p AutocompleteProvider) // optional
    BorderColor() StyleFunc              // theme: func(string) string
    SetBorderColor(StyleFunc)
    SetPaddingX(int)
    SetAutocompleteMaxVisible(int)
}
```

**Callbacks:** TS uses optional fields (`onSubmit?`, `onChange?`). In Go, model as
settable fields or setter methods. Recommended: store `onSubmit func(string)` as an
unexported field with a setter; callers check nil. The "optional" methods
(`AddToHistory` etc.) should be **split into smaller interfaces** the engine
type-asserts against, so the base EditorComponent interface stays required-only.

| TS `EditorComponent` | Go |
|---|---|
| required: `getText/setText/handleInput/onSubmit?/onChange?` | core interface |
| optional: `addToHistory/insertTextAtCursor/getExpandedText/setAutocompleteProvider/borderColor/setPaddingX/setAutocompleteMaxVisible` | separate optional interfaces |

---

## Tests to port
- `terminal-colors.test.ts` uses a `TestTerminal` + `InputRecorder implements Component`
  — port that as the canonical test helpers (also used by engine/component tests).
- Component-interface behavior is exercised indirectly through every component
  test; no standalone interface test file exists.
