package tui

// This file ports reference/tui/src/editor-component.ts — the interface that lets
// extensions swap the editor implementation (vim mode, etc.) while staying
// compatible with the app. See mapping/01-component-core.md.
//
// The reference's "optional" methods are split into separate optional interfaces
// (type-asserted by callers) so the core EditorComponent interface is fully
// required. SetAutocompleteProvider lives in Phase 9 (autocomplete.go) because it
// depends on AutocompleteProvider, which isn't defined until then.

// EditorComponent is the required surface a custom editor must provide.
type EditorComponent interface {
	Component
	InputHandler

	GetText() string
	SetText(text string)

	SetOnSubmit(fn func(string))
	SetOnChange(fn func(string))
}

// HistoryEditor opt-in: support up/down history navigation.
type HistoryEditor interface {
	AddToHistory(text string)
}

// TextInsertEditor opt-in: programmatic insertion at the cursor (e.g. clipboard
// image paths). Atomic for undo.
type TextInsertEditor interface {
	InsertTextAtCursor(text string)
}

// ExpandedTextEditor opt-in: text with paste markers expanded to their content.
type ExpandedTextEditor interface {
	GetExpandedText() string
}

// ThemedEditor opt-in: dynamic appearance.
type ThemedEditor interface {
	BorderColor() StyleFunc
	SetBorderColor(StyleFunc)
	SetPaddingX(padding int)
	SetAutocompleteMaxVisible(maxVisible int)
}

// AutocompleteEditor (SetAutocompleteProvider) is defined alongside
// AutocompleteProvider in Phase 9 (autocomplete.go).
