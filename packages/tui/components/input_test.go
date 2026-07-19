package components

// input_test.go covers the Input component's bracketed-paste handling — the path
// the engine feeds once terminal.go forwards OnPaste (wrapped in markers) to
// handleInput. This guards the form fields (/login API key, /add-provider,
// model-selector search) that the user pastes into.

import "testing"

func TestInputBracketedPaste(t *testing.T) {
	i := NewInput()

	// A complete bracketed paste in one chunk inserts the content verbatim.
	i.HandleInput("\x1b[200~sk-ant-test-key\x1b[201~")
	if got := i.GetValue(); got != "sk-ant-test-key" {
		t.Errorf("after paste, value = %q, want %q", got, "sk-ant-test-key")
	}

	// A paste split across two chunks (start marker + content+end) still assembles.
	i2 := NewInput()
	i2.HandleInput("\x1b[200~sk-")
	i2.HandleInput("split\x1b[201~")
	if got := i2.GetValue(); got != "sk-split" {
		t.Errorf("split paste value = %q, want %q", got, "sk-split")
	}

	// Newlines/tabs in pasted content are flattened (single-line input).
	i3 := NewInput()
	i3.HandleInput("\x1b[200~line1\nline2\tend\x1b[201~")
	if got := i3.GetValue(); got != "line1line2    end" {
		t.Errorf("flattened paste value = %q, want %q", got, "line1line2    end")
	}
}
