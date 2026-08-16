package components

import (
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// BorderedLoaderCallbacks reports cancellation to the App. OnAbort fires when
// the user presses the cancel key (or Abort() is called) on a cancellable
// loader; the App wires it to context cancellation of the in-flight operation.
type BorderedLoaderCallbacks struct {
	OnAbort func()
}

// BorderedLoaderComponent renders a spinner (framed by top/bottom borders) used
// for in-flight extension operations. When cancellable, escape/ctrl+c aborts.
//
// Port of reference/coding-agent/src/modes/interactive/components/bordered-loader.ts
type BorderedLoaderComponent struct {
	theme   *theme.Theme
	width   int
	message string

	cancellable bool
	spinner     spinner.Model
	onAbort     func()
}

// NewBorderedLoaderComponent creates a new bordered loader. cancellable controls
// whether the user can abort (the TS reference defaults this to true).
func NewBorderedLoaderComponent(
	t *theme.Theme,
	message string,
	cancellable bool,
	callbacks BorderedLoaderCallbacks,
) *BorderedLoaderComponent {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	return &BorderedLoaderComponent{
		theme:       t,
		message:     message,
		cancellable: cancellable,
		spinner:     sp,
		onAbort:     callbacks.OnAbort,
	}
}

// SetWidth updates the available width (the borders span it).
func (b *BorderedLoaderComponent) SetWidth(width int) { b.width = width }

// IsCancellable reports whether the loader accepts cancellation.
func (b BorderedLoaderComponent) IsCancellable() bool { return b.cancellable }

// Init starts the spinner animation.
func (b *BorderedLoaderComponent) Init() tea.Cmd { return b.spinner.Tick }

// Update advances the spinner and, when cancellable, aborts on the cancel key.
func (b *BorderedLoaderComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if b.cancellable {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "escape", "ctrl+c":
				b.abort()
				return b, nil
			}
		}
	}

	var cmd tea.Cmd
	b.spinner, cmd = b.spinner.Update(msg)
	return b, cmd
}

// View renders the top border, spinner + message, optional cancel hint, and
// bottom border.
func (b BorderedLoaderComponent) View() string {
	width := b.width
	if width <= 0 {
		width = 40
	}
	border := b.theme.Fg("border", strings.Repeat("─", width))
	frame := b.theme.Fg("accent", b.spinner.View())

	var sb strings.Builder
	sb.WriteString(border + "\n")
	sb.WriteString(frame + " " + b.theme.Fg("muted", b.message) + "\n")
	if b.cancellable {
		sb.WriteString("\n")
		sb.WriteString(b.theme.Fg("dim", "(esc to cancel)") + "\n")
	}
	sb.WriteString(border)
	return sb.String()
}

// --- Internal ---

// Abort programmatically cancels the loader (fires OnAbort).
func (b *BorderedLoaderComponent) Abort() { b.abort() }

func (b *BorderedLoaderComponent) abort() {
	if b.onAbort != nil {
		b.onAbort()
	}
}
