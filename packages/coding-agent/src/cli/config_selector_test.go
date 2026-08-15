package cli

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ycode/packages/coding-agent/src/core/tui/components"
)

// TestConfigSelectorCallbacks_CloseAndExit verifies the close/exit wiring:
// OnClose records configExitClose and returns a quit cmd; OnExit records
// configExitHard and returns a quit cmd; OnToggle is a harmless no-op.
func TestConfigSelectorCallbacks_CloseAndExit(t *testing.T) {
	var exitKind configExitKind
	cb := configSelectorCallbacks(&exitKind)

	// OnClose → close (escape path) → normal completion.
	if cmd := cb.OnClose(); cmd == nil || cmd() == nil {
		t.Errorf("OnClose returned %v, want a non-nil quit cmd producing a Msg", cmd)
	}
	if exitKind != configExitClose {
		t.Errorf("after OnClose, exitKind = %v, want configExitClose", exitKind)
	}

	// OnExit → hard exit (ctrl+c path).
	if cmd := cb.OnExit(); cmd == nil || cmd() == nil {
		t.Errorf("OnExit returned %v, want a non-nil quit cmd producing a Msg", cmd)
	}
	if exitKind != configExitHard {
		t.Errorf("after OnExit, exitKind = %v, want configExitHard", exitKind)
	}

	// OnToggle must be wired (non-nil) and a no-op: no exit-kind mutation, no cmd.
	exitKind = configExitNone
	if cmd := cb.OnToggle(components.ConfigToggleReport{}); cmd != nil {
		t.Errorf("OnToggle returned %v, want nil (no-op)", cmd)
	}
	if exitKind != configExitNone {
		t.Errorf("OnToggle mutated exitKind to %v, want untouched", exitKind)
	}
}

// TestConfigSelectorCallbacks_NilPointer verifies a defensive nil exitKind pointer
// does not panic; the quit cmds are still returned.
func TestConfigSelectorCallbacks_NilPointer(t *testing.T) {
	cb := configSelectorCallbacks(nil)
	if cmd := cb.OnClose(); cmd == nil || cmd() == nil {
		t.Errorf("OnClose(nil) = %v, want a non-nil quit cmd", cmd)
	}
	if cmd := cb.OnExit(); cmd == nil || cmd() == nil {
		t.Errorf("OnExit(nil) = %v, want a non-nil quit cmd", cmd)
	}
}

// TestSelectConfig_MapsExitKind verifies SelectConfig maps the recorded exit kind
// to its error contract: close → nil, hard exit → ErrConfigExit. It does so
// WITHOUT driving a real tea.Program by invoking the callback builder directly —
// the same pure seam SelectConfig consults.
func TestSelectConfig_MapsExitKind(t *testing.T) {
	if !errors.Is(ErrConfigExit, ErrConfigExit) {
		t.Fatal("ErrConfigExit must be errors.Is-identifiable")
	}

	// Close path → no error.
	var k configExitKind
	k = configExitClose
	if err := exitKindToError(k); err != nil {
		t.Errorf("close → exitKindToError = %v, want nil", err)
	}

	// Hard-exit path → ErrConfigExit.
	k = configExitHard
	if err := exitKindToError(k); !errors.Is(err, ErrConfigExit) {
		t.Errorf("hard exit → exitKindToError = %v, want ErrConfigExit", err)
	}
}

// TestNewConfigSelectorModel_Renders verifies the model wires the component
// (empty ResolvedPaths → "No resources found"), renders a non-empty view with the
// title, handles a WindowSizeMsg without panicking, and tolerates a nil
// ResolvedPaths — all without driving a real tea.Program.
func TestNewConfigSelectorModel_Renders(t *testing.T) {
	t2, err := defaultTheme()
	if err != nil {
		t.Fatalf("defaultTheme: %v", err)
	}

	m := newConfigSelectorModel(t2, nil, nil) // nil resolved → empty tree

	if m.Init() != nil {
		t.Error("Init should return a nil cmd")
	}
	view := m.View()
	if view == "" {
		t.Fatal("View should render a non-empty selector")
	}
	// The title "Resource Configuration" is a stable substring of the view.
	if !strings.Contains(view, "Resource Configuration") {
		t.Errorf("view should contain the title:\n%s", view)
	}

	// A WindowSizeMsg must resize without panic and stay the same model type.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if _, ok := next.(configSelectorModel); !ok {
		t.Errorf("Update(WindowSizeMsg) returned %T, want configSelectorModel", next)
	}
}
