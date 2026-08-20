package main

// This file holds the pure/testable helpers extracted from the main() flow: the
// app-mode decision, env-flag truthiness, fork-flag conflict detection, CLI path
// resolution, settings-diagnostic collection, diagnostic reporting, stdin piping,
// and extension-flag gathering. Each is lifted out of the (untestable) run()
// orchestrator so it can be covered by helpers_test.go, following the 19.x
// "extract a pure seam, ship its test" precedent.
//
// It is the Go port of the helper functions scattered through
// reference/coding-agent/src/main.ts (collectSettingsDiagnostics,
// reportDiagnostics, isTruthyEnvFlag, resolveAppMode, toPrintOutputMode,
// resolveCliPaths, readPipedStdin, validateForkFlags).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
	cli "ycode/packages/coding-agent/src/cli"
)

// ============================================================================
// ANSI color constants (raw SGR codes — diagnostics print before any TUI/theme)
// ============================================================================

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGray   = "\x1b[90m"
	ansiDim    = "\x1b[2m"
)

// ============================================================================
// App mode — TS AppMode + resolveAppMode + toPrintOutputMode
// ============================================================================

// appMode mirrors the TS AppMode union ("interactive" | "print" | "json" | "rpc").
type appMode string

const (
	appInteractive appMode = "interactive"
	appPrint       appMode = "print"
	appJSON        appMode = "json"
	appRPC         appMode = "rpc"
)

// resolveAppMode decides the run mode from parsed args and whether stdin is a TTY
// (TS resolveAppMode). RPC/JSON are driven by --mode; --print or a non-TTY stdin
// selects non-interactive print mode; otherwise interactive.
func resolveAppMode(parsed cli.Args, stdinIsTTY bool) appMode {
	if parsed.Mode == cli.ModeRPC {
		return appRPC
	}
	if parsed.Mode == cli.ModeJSON {
		return appJSON
	}
	if parsed.Print || !stdinIsTTY {
		return appPrint
	}
	return appInteractive
}

// toPrintOutputMode maps the resolved app mode onto the print-mode output Mode
// (TS toPrintOutputMode). JSON → json, everything else → text.
func toPrintOutputMode(m appMode) cli.Mode {
	if m == appJSON {
		return cli.ModeJSON
	}
	return cli.ModeText
}

// ============================================================================
// Env flags — TS isTruthyEnvFlag
// ============================================================================

// isTruthyEnvFlag reports whether an env-var value is truthy (TS
// isTruthyEnvFlag): "1", "true", or "yes" (case-insensitive).
func isTruthyEnvFlag(v string) bool {
	if v == "" {
		return false
	}
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// ============================================================================
// Fork-flag validation — TS validateForkFlags
// ============================================================================

// forkFlagConflicts returns the session-selection flags that conflict with
// --fork (TS validateForkFlags body). It is pure so the conflict list is
// unit-testable; validateForkFlags is the exiting wrapper.
func forkFlagConflicts(parsed cli.Args) []string {
	if parsed.Fork == "" {
		return nil
	}
	var conflicts []string
	if parsed.Session != "" {
		conflicts = append(conflicts, "--session")
	}
	if parsed.Continue {
		conflicts = append(conflicts, "--continue")
	}
	if parsed.Resume {
		conflicts = append(conflicts, "--resume")
	}
	if parsed.NoSession {
		conflicts = append(conflicts, "--no-session")
	}
	return conflicts
}

// validateForkFlags errors out (red stderr + exit 1) when --fork is combined
// with any session-selection flag (TS validateForkFlags).
func validateForkFlags(parsed cli.Args) {
	conflicts := forkFlagConflicts(parsed)
	if len(conflicts) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, ansiRed+"Error: --fork cannot be combined with "+
		strings.Join(conflicts, ", ")+ansiReset)
	os.Exit(1)
}

// ============================================================================
// CLI path resolution — TS resolveCliPaths (+ utils isLocalPath)
// ============================================================================

// resolveCliPaths resolves relative, local paths against cwd, leaving absolute
// paths and URL-like values untouched (TS resolveCliPaths over utils.isLocalPath).
// A nil input yields nil.
func resolveCliPaths(cwd string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = resolveOneCliPath(cwd, p)
	}
	return out
}

// resolveOneCliPath is the per-element helper extracted for testability: relative
// non-URL paths are joined to cwd (TS isLocalPath: not absolute and has no "://").
func resolveOneCliPath(cwd, p string) string {
	if filepath.IsAbs(p) || strings.Contains(p, "://") {
		return p
	}
	return filepath.Join(cwd, p)
}

// ============================================================================
// Settings + runtime diagnostics — TS collectSettingsDiagnostics / reportDiagnostics
// ============================================================================

// collectSettingsDiagnostics drains accumulated settings errors and renders them
// as warnings with a "(<context>, <scope> settings) <message>" prefix (TS
// collectSettingsDiagnostics). A nil settings manager yields nothing.
func collectSettingsDiagnostics(sm *core.SettingsManager, context string) []core.AgentSessionRuntimeDiagnostic {
	if sm == nil {
		return nil
	}
	errors := sm.DrainError()
	if len(errors) == 0 {
		return nil
	}
	diags := make([]core.AgentSessionRuntimeDiagnostic, 0, len(errors))
	for _, e := range errors {
		msg := ""
		if e.Error != nil {
			msg = e.Error.Error()
		}
		diags = append(diags, core.AgentSessionRuntimeDiagnostic{
			Type:    core.DiagnosticWarning,
			Message: fmt.Sprintf("(%s, %s settings) %s", context, e.Scope, msg),
		})
	}
	return diags
}

// reportDiagnostics prints each diagnostic to w, color-coded by type with an
// "Error: "/"Warning: " prefix (TS reportDiagnostics). The "info" type — which
// the core package renders as the DiagnosticInfo const, currently the bug value
// "string" — falls through to dim + no prefix, matching the TS else-branch.
func reportDiagnostics(w io.Writer, diags []core.AgentSessionRuntimeDiagnostic) {
	for _, d := range diags {
		color, prefix := diagnosticStyle(d.Type)
		fmt.Fprintln(w, color+prefix+d.Message+ansiReset)
	}
}

// diagnosticStyle maps a runtime diagnostic type to its ANSI color and prefix
// (TS reportDiagnostics ternary). Extracted so the mapping — including the
// DiagnosticInfo/"string"-bug → dim handling — is unit-testable.
func diagnosticStyle(t core.AgentSessionRuntimeDiagnosticType) (color, prefix string) {
	switch t {
	case core.DiagnosticError:
		return ansiRed, "Error: "
	case core.DiagnosticWarning:
		return ansiYellow, "Warning: "
	default:
		// Covers DiagnosticInfo, whose const value is "string" (a core bug); the
		// TS else-branch renders info as dim with no prefix, so this matches.
		return ansiDim, ""
	}
}

// ============================================================================
// Parse-time diagnostics — TS main()'s parsed.diagnostics loop
// ============================================================================

// reportParseDiagnostics prints parse warnings/errors to stderr and reports
// whether any error was present (caller exits 1 when true). Mirrors the
// parsed.diagnostics block in the TS main().
func reportParseDiagnostics(diags []cli.Diagnostic) (hasError bool) {
	for _, d := range diags {
		color, label := parseDiagnosticStyle(d.Type)
		fmt.Fprintln(os.Stderr, color+label+": "+d.Message+ansiReset)
		if d.Type == "error" {
			hasError = true
		}
	}
	return hasError
}

// parseDiagnosticStyle maps a parse-time diagnostic type string to its color and
// label (TS "Error"/"Warning"). Parse diagnostics carry their type as a plain
// string (cli.Diagnostic.Type), distinct from the runtime diagnostic type.
func parseDiagnosticStyle(t string) (color, label string) {
	if t == "error" {
		return ansiRed, "Error"
	}
	return ansiYellow, "Warning"
}

// ============================================================================
// Stdin — TS readPipedStdin
// ============================================================================

// stdinIsTerminal reports whether stdin is connected to a TTY (TS
// process.stdin.isTTY). On stat error it assumes a TTY so we never block reading.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// readPipedStdin reads all of stdin when it is piped (not a TTY) and returns the
// trimmed content plus whether any non-empty content was present (TS
// readPipedStdin, which resolves undefined for empty/whitespace-only input).
// When stdin is a TTY it returns "", false.
func readPipedStdin() (content string, hasContent bool) {
	if stdinIsTerminal() {
		return "", false
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", false
	}
	content = strings.TrimSpace(string(data))
	return content, content != ""
}

// ============================================================================
// Extension flags — TS main()'s help-path extension-flag gathering
// ============================================================================

// collectExtensionFlags gathers every extension flag (as values) across all
// loaded extensions for the help text (TS main() help path). The extensions store
// flags as map[string]*ExtensionFlag; PrintHelp wants []ExtensionFlag values.
func collectExtensionFlags(loader core.ResourceLoader) []extensions.ExtensionFlag {
	if loader == nil {
		return nil
	}
	result := loader.GetExtensions()
	if result == nil {
		return nil
	}
	var flags []extensions.ExtensionFlag
	for _, ext := range result.Extensions {
		for _, f := range ext.Flags {
			if f != nil {
				flags = append(flags, *f)
			}
		}
	}
	return flags
}

// ============================================================================
// Small generic helpers
// ============================================================================

// contains reports whether args contains s (used for the --offline scan).
func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}
