package components

// ChatItem is the interface satisfied by any component that can be rendered
// as a single entry in the chat viewport. Every message component
// (assistant, tool, bash, custom, branch/compaction/skill summary) implements
// View() string, so they all satisfy this interface.
type ChatItem interface {
	View() string
}

// SizableChatItem is implemented by chat items that adapt to a given width.
// The App calls SetWidth before rendering so wrapped layouts stay correct.
type SizableChatItem interface {
	SetWidth(width int)
}
