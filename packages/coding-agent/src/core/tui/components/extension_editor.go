package components

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ExtensionEditorCallbacks wires the editor's submit/cancel results back to the
// caller. Returning a tea.Cmd (rather than mutating state in the callback) keeps
// state changes in the App's Update loop — the same result-protocol pattern as
// the Phase 16 selectors.
type ExtensionEditorCallbacks struct {
	OnSubmit func(value string) tea.Cmd
	OnCancel func() tea.Cmd
}

// ExtensionEditor is a multi-line text editor shown when an extension requests
// multi-line input (e.g. via the extension Input UI). Enter submits, Shift+Enter
// (or Ctrl+Enter) inserts a newline, Escape cancels, and Ctrl+G opens the user's
// $VISUAL/$EDITOR on a temp file. Port of extension-editor.ts.
type ExtensionEditor struct {
	textarea  textarea.Model
	theme     *theme.Theme
	title     string
	width     int
	focused   bool
	callbacks ExtensionEditorCallbacks
}

// NewExtensionEditor creates an editor with the given title and optional prefill.
func NewExtensionEditor(t *theme.Theme, title, prefill string, cb ExtensionEditorCallbacks) ExtensionEditor {
	ta := textarea.New()
	ta.Prompt = " > "
	ta.Placeholder = "Enter text..."
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetWidth(60)
	ta.SetHeight(5)
	styleTextarea(&ta, t)
	ta.Focus()
	if strings.TrimSpace(prefill) != "" {
		ta.SetValue(prefill)
	}
	return ExtensionEditor{
		textarea:  ta,
		theme:     t,
		title:     title,
		width:     60,
		focused:   true,
		callbacks: cb,
	}
}

// SetWidth updates the editor width (satisfies the overlayComponent interface).
func (e *ExtensionEditor) SetWidth(w int) {
	e.width = w
	e.textarea.SetWidth(w)
}

// Init satisfies tea.Model.
func (e ExtensionEditor) Init() tea.Cmd {
	return e.textarea.Focus()
}

// externalEditorResultMsg carries the content written by the external editor
// back into the textarea once the program resumes.
type externalEditorResultMsg struct {
	content string
	ok      bool
}

// Update satisfies tea.Model.
func (e ExtensionEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case externalEditorResultMsg:
		if m.ok {
			e.textarea.SetValue(m.content)
		}
		return e, nil

	case tea.KeyMsg:
		switch m.String() {
		case "enter":
			value := strings.TrimSpace(e.textarea.Value())
			if value == "" {
				return e, nil // ignore empty submit
			}
			if e.callbacks.OnSubmit != nil {
				return e, e.callbacks.OnSubmit(value)
			}
			return e, nil
		case "shift+enter", "ctrl+enter":
			var cmd tea.Cmd
			e.textarea, cmd = e.textarea.Update(m)
			return e, cmd
		case "escape":
			if e.callbacks.OnCancel != nil {
				return e, e.callbacks.OnCancel()
			}
			return e, nil
		case "ctrl+g":
			return e, e.openExternalEditor()
		}
	}

	var cmd tea.Cmd
	e.textarea, cmd = e.textarea.Update(msg)
	return e, cmd
}

// View satisfies tea.Model.
func (e ExtensionEditor) View() string {
	var b strings.Builder
	if e.title != "" && e.theme != nil {
		b.WriteString(e.theme.Fg("accent", e.theme.Bold(e.title)) + "\n")
	}
	ta := e.textarea.View()
	if e.theme != nil {
		ta = lipgloss.NewStyle().
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(e.theme.ToLipglossColor("border")).
			Render(ta)
	}
	b.WriteString(ta)
	if e.theme != nil {
		if h := e.hint(); h != "" {
			b.WriteString("\n" + e.theme.Dim(h))
		}
	}
	return b.String()
}

// hint renders the keybind legend. The external-editor hint only appears when an
// editor is configured via $VISUAL or $EDITOR.
func (e ExtensionEditor) hint() string {
	parts := []string{"↵ submit", "shift+↵ newline", "esc cancel"}
	if os.Getenv("VISUAL") != "" || os.Getenv("EDITOR") != "" {
		parts = append(parts, "ctrl+g external")
	}
	return strings.Join(parts, "  ")
}

// openExternalEditor hands the current text to $VISUAL/$EDITOR via tea.ExecProcess,
// which suspends the program, runs the editor, and resumes. The callback reads the
// (possibly edited) file back and returns it as an externalEditorResultMsg.
func (e ExtensionEditor) openExternalEditor() tea.Cmd {
	editorCmd := os.Getenv("VISUAL")
	if editorCmd == "" {
		editorCmd = os.Getenv("EDITOR")
	}
	if editorCmd == "" {
		return nil
	}

	// Seed a temp file with the current content.
	tmp, err := os.CreateTemp("", "ycode-ext-editor-*.md")
	if err != nil {
		return nil
	}
	tmpName := tmp.Name()
	if _, werr := tmp.WriteString(e.textarea.Value()); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return nil
	}
	_ = tmp.Close()

	// Parse the editor command and append the temp file as the final argument.
	parts := strings.Fields(editorCmd)
	if len(parts) == 0 {
		_ = os.Remove(tmpName)
		return nil
	}
	args := make([]string, 0, len(parts))
	args = append(args, parts[1:]...)
	args = append(args, tmpName)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Shell wrapper so commands like "code -w" or bare .exe names resolve.
		cmd = exec.Command("cmd", append([]string{"/c", parts[0]}, args...)...)
	} else {
		cmd = exec.Command(parts[0], args...)
	}

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		content, readErr := os.ReadFile(tmpName)
		_ = os.Remove(tmpName)
		if err != nil || readErr != nil {
			return externalEditorResultMsg{ok: false}
		}
		return externalEditorResultMsg{content: strings.TrimRight(string(content), "\n"), ok: true}
	})
}
