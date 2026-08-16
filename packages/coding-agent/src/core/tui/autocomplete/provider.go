// Package autocomplete defines the contract for the editor's inline
// autocomplete popup. A provider turns the token the user is currently typing
// (the "prefix") into a ranked list of suggestions.
//
// Phase 17.1 establishes only the contract — the data type and the interface.
// Concrete providers ship in the same package:
//
//   - CombinedProvider  (17.2) merges slash commands + templates + extensions + skills
//   - SlashCommandProvider (17.3) converts builtin slash commands, incl. /model args
//
// Simplification note. The TypeScript source (@earendil-works/pi-tui) models
// autocomplete as a cursor-aware async query — getSuggestions(lines, line, col,
// opts) → { items, prefix }, plus applyCompletion and shouldTriggerFileCompletion
// hooks. The Go port collapses this to a synchronous, prefix-based query
// (GetItems(prefix) → items). The editor is responsible for extracting the token
// to complete (e.g. "/mod" or "#12") and for applying a selection, which keeps
// the provider contract simple and testable. The richer cursor/async shape can
// grow back around this interface if a provider later needs it.
package autocomplete

// AutocompleteItem is a single suggestion in the autocomplete popup.
//
//   - Value is the full string inserted into the editor when the item is chosen
//     (e.g. "/model", "#123", "claude-opus-4-6"). It includes the trigger
//     character so the result is self-contained.
//   - Label is the primary text shown in the popup row (e.g. "/model", "#123",
//     the model id). May equal Value for simple commands.
//   - Description is the secondary, dimmed text shown alongside the Label
//     (e.g. a command's help text, a model's provider, an issue's title).
type AutocompleteItem struct {
	Value       string
	Label       string
	Description string
}

// NewItem is a compact constructor for AutocompleteItem.
func NewItem(value, label, description string) AutocompleteItem {
	return AutocompleteItem{Value: value, Label: label, Description: description}
}

// AutocompleteProvider produces suggestions for a token the user is typing.
//
// prefix is the token currently being completed — for slash commands this is the
// text from the most recent "/" to the cursor (e.g. "/mod"); for trigger-character
// completions like "#" it is the text from that character to the cursor (e.g.
// "#12"). The editor extracts and passes this prefix; the provider never sees the
// full buffer.
//
// Implementations should return only items that match prefix (typically via a
// case-folded subsequence/fuzzy match) in display order. An empty slice (not nil
// items) signals "no suggestions", which the editor renders as no popup.
type AutocompleteProvider interface {
	GetItems(prefix string) []AutocompleteItem
}
