package main

// This file resolves the session-selection CLI flags (--session / --resume /
// --continue / --fork / --no-session) into a *core.SessionManager, and owns the
// missing-session-cwd adapter. It is the Go port of the session-resolution half
// of reference/coding-agent/src/main.ts (resolveSessionPath, promptConfirm,
// validateForkFlags's fork helpers, forkSessionOrExit, createSessionManager).
//
// ============================================================================
// Deviations from the plan sketch + honest deferrals
// ============================================================================
//
// sessionCwdAdapter: core.SessionCwdSource requires GetCwd() and — note the
// typo — GetSesionFile() (missing 's'), which *core.SessionManager does not
// satisfy (it has GetSessionFile()). Rather than wait for a core rename, we
// adapt the manager locally (sessionCwdAdapter). The typo is recorded as a
// follow-up core fix; the adapter is the faithful, non-invasive bridge.
//
// promptForMissingSessionCwd: the TS interactive path pops a TUI prompt to
// continue/cancel. That TUI prompt is deferred (it needs the interactive App
// bootstrapped separately from Run); we fall back to the non-interactive error
// + exit 1 path, which is correct for fresh sessions and rarely fires.
//
// initTheme/stopThemeWatcher around --resume: theme wiring is not part of this
// phase, so the resume picker runs on the default theme (cf. 19.6).

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	core "ycode/packages/coding-agent/src/core"
	cli "ycode/packages/coding-agent/src/cli"
)

// ============================================================================
// resolvedSession — TS ResolvedSession discriminated union
// ============================================================================

type resolvedSessionKind int

const (
	rsPath     resolvedSessionKind = iota // Direct file path
	rsLocal                               // Found in current project
	rsGlobal                              // Found in a different project (carries cwd)
	rsNotFound                            // Not found anywhere (carries arg)
)

// resolvedSession mirrors the TS ResolvedSession union. kind discriminates which
// of path/cwd/arg is meaningful.
type resolvedSession struct {
	kind resolvedSessionKind
	path string // rsPath / rsLocal / rsGlobal
	cwd  string // rsGlobal
	arg  string // rsNotFound
}

// resolveSessionPath resolves a session argument to a file path (TS
// resolveSessionPath). If it looks like a path (contains a separator or ends in
// .jsonl) it is used as-is; otherwise it is matched as a session-ID prefix,
// first in the current project, then globally.
func resolveSessionPath(sessionArg, cwd, sessionDir string) resolvedSession {
	// Looks like a file path → use as-is.
	if strings.ContainsAny(sessionArg, "/\\") || strings.HasSuffix(sessionArg, ".jsonl") {
		return resolvedSession{kind: rsPath, path: sessionArg}
	}

	// Match as a session-ID prefix in the current project first.
	for _, s := range core.ListSessions(cwd, sessionDir) {
		if strings.HasPrefix(s.ID, sessionArg) {
			return resolvedSession{kind: rsLocal, path: s.Path}
		}
	}

	// Then a global search across all projects.
	for _, s := range core.ListAllSessions() {
		if strings.HasPrefix(s.ID, sessionArg) {
			return resolvedSession{kind: rsGlobal, path: s.Path, cwd: s.Cwd}
		}
	}

	return resolvedSession{kind: rsNotFound, arg: sessionArg}
}

// ============================================================================
// promptConfirm — TS promptConfirm (readline [y/N])
// ============================================================================

// promptConfirm prompts for a yes/no answer on stdin (TS promptConfirm). "y" or
// "yes" (case-insensitive) → true; anything else (including EOF) → false.
func promptConfirm(message string) bool {
	fmt.Fprint(os.Stdout, message+" [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// ============================================================================
// forkSessionOrExit — TS forkSessionOrExit
// ============================================================================

// forkSessionOrExit forks sourcePath into a new session for cwd, or prints a red
// error and exits 1 on failure (TS forkSessionOrExit).
func forkSessionOrExit(sourcePath, cwd, sessionDir string) *core.SessionManager {
	sm, err := core.ForkSession(sourcePath, cwd, sessionDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, ansiRed+"Error: "+err.Error()+ansiReset)
		os.Exit(1)
	}
	return sm
}

// ============================================================================
// sessionCwdAdapter — bridges *SessionManager to core.SessionCwdSource
// ============================================================================

// sessionCwdAdapter adapts a *core.SessionManager to the core.SessionCwdSource
// interface. The interface spells its file accessor GetSesionFile() (typo,
// missing 's'); SessionManager.GetSessionFile() is spelled correctly, so a
// direct assignment is impossible. This adapter is the faithful bridge.
type sessionCwdAdapter struct {
	sm *core.SessionManager
}

func (a sessionCwdAdapter) GetCwd() string {
	return a.sm.GetCwd()
}

// GetSesionFile matches the (typo'd) SessionCwdSource method name.
func (a sessionCwdAdapter) GetSesionFile() string {
	return a.sm.GetSessionFile()
}

// ============================================================================
// createSessionManager — TS createSessionManager
// ============================================================================

// createSessionManager builds the session manager implied by the parsed
// session-selection flags (TS createSessionManager). Precedence mirrors the
// reference: --no-session → in-memory; --fork → resolve + fork; --session →
// resolve + open (global matches prompt to fork); --resume → interactive
// picker; --continue → most recent; else a fresh session.
func createSessionManager(parsed cli.Args, cwd, sessionDir string) *core.SessionManager {
	if parsed.NoSession {
		return core.NewInMemorySession(cwd)
	}

	if parsed.Fork != "" {
		resolved := resolveSessionPath(parsed.Fork, cwd, sessionDir)
		switch resolved.kind {
		case rsPath, rsLocal, rsGlobal:
			return forkSessionOrExit(resolved.path, cwd, sessionDir)
		case rsNotFound:
			fmt.Fprintln(os.Stderr, ansiRed+
				"No session found matching '"+resolved.arg+"'"+ansiReset)
			os.Exit(1)
		}
	}

	if parsed.Session != "" {
		resolved := resolveSessionPath(parsed.Session, cwd, sessionDir)
		switch resolved.kind {
		case rsPath, rsLocal:
			return core.OpenSessionManager(resolved.path, sessionDir, "")
		case rsGlobal:
			fmt.Fprintln(os.Stdout, ansiYellow+
				"Session found in different project: "+resolved.cwd+ansiReset)
			if !promptConfirm("Fork this session into current directory?") {
				fmt.Fprintln(os.Stdout, ansiDim+"Aborted."+ansiReset)
				os.Exit(0)
			}
			return forkSessionOrExit(resolved.path, cwd, sessionDir)
		case rsNotFound:
			fmt.Fprintln(os.Stderr, ansiRed+
				"No session found matching '"+resolved.arg+"'"+ansiReset)
			os.Exit(1)
		}
	}

	if parsed.Resume {
		// The session picker derives both pools from the manager; pass one scoped
		// to the current cwd + session dir. initTheme/stopThemeWatcher are
		// deferred (theme wiring is not in this phase).
		selectedPath, err := cli.SelectSession(core.NewSessionManager(cwd, sessionDir))
		if err != nil {
			fmt.Fprintln(os.Stderr, ansiRed+"Error: "+err.Error()+ansiReset)
			os.Exit(1)
		}
		if selectedPath == "" {
			fmt.Fprintln(os.Stdout, ansiDim+"No session selected"+ansiReset)
			os.Exit(0)
		}
		return core.OpenSessionManager(selectedPath, sessionDir, "")
	}

	if parsed.Continue {
		return core.ContinueRecentSession(cwd, sessionDir)
	}

	return core.NewSessionManager(cwd, sessionDir)
}
