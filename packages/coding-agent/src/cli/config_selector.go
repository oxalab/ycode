package cli

// This file runs the interactive resource-configuration picker for the `config`
// subcommand: a standalone bubbletea program wrapping the
// ConfigSelectorComponent. It is the Go port of
// reference/coding-agent/src/cli/config-selector.ts.
//
// ============================================================================
// Deviations from the plan sketch + honest deferrals
// ============================================================================
//
// Plan sketch: SelectConfig() error.
// The reference selectConfig takes a ConfigSelectorOptions { resolvedPaths,
// settingsManager, cwd, agentDir }. The Go ConfigSelectorComponent constructor
// consumes only resolvedPaths + callbacks (settingsManager/cwd/agentDir were
// TS-internal and are not part of the ported component), so the faithful port
// takes a ConfigSelectorOptions carrying the resolved paths. Like 19.6's
// "sessionID" mislabel, the no-arg sketch was incomplete.
//
// Theme: the reference inherits the user's configured theme (initTheme).
// Settings→theme wiring isn't this phase, so the picker uses the built-in dark
// theme via defaultTheme() (shared with session_picker.go — single source of
// truth). A later overload can accept the configured theme (19.8).
//
// Persistence: the reference persists toggles via the SettingsManager handed to
// the component. The Go App's openConfigSelector currently uses a no-op OnToggle
// and an empty &core.ResolvedPaths{} because the per-resource SettingsManager
// setters and the PackageManager port are still pending (16.18). This port
// matches that state: OnToggle is a no-op and resolvedPaths may be empty, so the
// selector renders but does not yet persist. Wiring real persistence + real
// resolved paths is deferred to when those land.
//
// OnExit semantics: the TS exit callback calls process.exit(0) (hard exit). A
// library function should not call os.Exit, so SelectConfig instead returns the
// sentinel ErrConfigExit when the user hits ctrl+c; the entry point (19.8) maps
// it to os.Exit(0). OnClose (escape) completes normally (nil error), matching the
// TS resolve().
//
// Testability: SelectConfig drives a real tea.Program (needs a TTY) and is not
// unit-tested here. The pure seams — configSelectorCallbacks (close/exit wiring)
// and newConfigSelectorModel — are extracted and covered by
// config_selector_test.go, following the 19.6 precedent.

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// ErrConfigExit signals that the user requested a hard exit (ctrl+c) from the
// config selector. The TS exit callback calls process.exit(0); a library must
// not, so SelectConfig returns this sentinel and the entry point maps it to
// os.Exit(0).
var ErrConfigExit = errors.New("config: exit requested")

// ConfigSelectorOptions carries the inputs the standalone picker needs. It
// mirrors the TS ConfigSelectorOptions, reduced to what the Go
// ConfigSelectorComponent consumes (resolved paths). settingsManager/cwd/agentDir
// from the TS shape are not part of the ported component's constructor.
type ConfigSelectorOptions struct {
	// ResolvedPaths is the package-resource tree the selector renders. May be
	// empty (the App opens it empty today, pending the PackageManager port).
	ResolvedPaths *core.ResolvedPaths
}

// SelectConfig shows the interactive resource-configuration picker and returns
// when the user closes it (TS selectConfig → void). It returns ErrConfigExit if
// the user hit ctrl+c (the TS exit path calls process.exit(0); the entry point
// maps this sentinel to os.Exit(0)).
func SelectConfig(options ConfigSelectorOptions) error {
	t, err := defaultTheme()
	if err != nil {
		return err
	}

	var exitKind configExitKind
	model := newConfigSelectorModel(t, options.ResolvedPaths, &exitKind)

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return err
	}
	return exitKindToError(exitKind)
}

// exitKindToError maps the recorded termination kind to SelectConfig's error
// contract: hard exit → ErrConfigExit (entry point maps it to os.Exit(0)); close
// or none → nil. Extracted so the mapping is unit-testable without a tea.Program.
func exitKindToError(exitKind configExitKind) error {
	if exitKind == configExitHard {
		return ErrConfigExit
	}
	return nil
}

// configExitKind records how the selector terminated.
type configExitKind int

const (
	configExitNone configExitKind = iota // still running / never closed
	configExitClose                      // escape → close (normal completion)
	configExitHard                       // ctrl+c → hard exit (TS process.exit)
)

// configSelectorCallbacks builds the close/exit callbacks that record the
// termination kind and quit the program (the TS selector's close/exit callbacks).
// OnToggle is a no-op: persistence is deferred until the per-resource
// SettingsManager setters + PackageManager port land (the App uses a no-op today).
func configSelectorCallbacks(exitKind *configExitKind) components.ConfigSelectorCallbacks {
	return components.ConfigSelectorCallbacks{
		OnToggle: func(_ components.ConfigToggleReport) tea.Cmd { return nil },
		OnClose: func() tea.Cmd {
			if exitKind != nil {
				*exitKind = configExitClose
			}
			return tea.Quit
		},
		OnExit: func() tea.Cmd {
			if exitKind != nil {
				*exitKind = configExitHard
			}
			return tea.Quit
		},
	}
}

// newConfigSelectorModel wraps the ConfigSelectorComponent in a minimal tea.Model
// that forwards messages and resizes on WindowSizeMsg. The close/exit callbacks
// record the termination kind into exitKind (shared with SelectConfig).
func newConfigSelectorModel(t *theme.Theme, resolved *core.ResolvedPaths, exitKind *configExitKind) configSelectorModel {
	if resolved == nil {
		resolved = &core.ResolvedPaths{}
	}
	comp := components.NewConfigSelectorComponent(t, resolved, configSelectorCallbacks(exitKind))
	comp.SetWidth(80) // sane default until the first WindowSizeMsg arrives
	return configSelectorModel{comp: comp}
}

// configSelectorModel is the standalone tea.Model driving the picker.
type configSelectorModel struct {
	comp *components.ConfigSelectorComponent
}

// Init satisfies tea.Model.
func (m configSelectorModel) Init() tea.Cmd { return nil }

// Update resizes on WindowSizeMsg and forwards every other message to the
// embedded ConfigSelectorComponent (which owns the key handling and emits the
// close/exit cmds).
func (m configSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.comp.SetWidth(wsm.Width)
		return m, nil
	}
	model, cmd := m.comp.Update(msg)
	// The component returns itself (a *ConfigSelectorComponent); keep the
	// up-to-date pointer so View reflects any mutations.
	if c, ok := model.(*components.ConfigSelectorComponent); ok {
		m.comp = c
	}
	return m, cmd
}

// View delegates to the embedded component (header + search + grouped list).
func (m configSelectorModel) View() string {
	return m.comp.View()
}
