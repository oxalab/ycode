package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "ycode/packages/coding-agent/src/core"
	cli "ycode/packages/coding-agent/src/cli"
)

// TestResolveAppMode exercises the mode-decision matrix: --mode rpc/json drive
// those modes; --print or a non-TTY stdin selects print; otherwise interactive.
func TestResolveAppMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    cli.Mode
		print   bool
		stdinTTY bool
		want    appMode
	}{
		{"rpc mode", cli.ModeRPC, false, true, appRPC},
		{"json mode", cli.ModeJSON, false, true, appJSON},
		{"print flag", "", true, true, appPrint},
		{"piped stdin", "", false, false, appPrint},
		{"interactive default", "", false, true, appInteractive},
		{"print beats nothing", "", true, false, appPrint},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed := cli.Args{Mode: c.mode, Print: c.print}
			if got := resolveAppMode(parsed, c.stdinTTY); got != c.want {
				t.Errorf("resolveAppMode = %q, want %q", got, c.want)
			}
		})
	}
}

// TestToPrintOutputMode verifies json maps to json and everything else to text.
func TestToPrintOutputMode(t *testing.T) {
	if got := toPrintOutputMode(appJSON); got != cli.ModeJSON {
		t.Errorf("json → %q, want %q", got, cli.ModeJSON)
	}
	for _, m := range []appMode{appPrint, appInteractive, appRPC} {
		if got := toPrintOutputMode(m); got != cli.ModeText {
			t.Errorf("%v → %q, want %q", m, got, cli.ModeText)
		}
	}
}

// TestIsTruthyEnvFlag verifies the truthy set and that empty/false are falsy.
func TestIsTruthyEnvFlag(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "True", "yes", "YES", "Yes"}
	for _, v := range truthy {
		if !isTruthyEnvFlag(v) {
			t.Errorf("isTruthyEnvFlag(%q) = false, want true", v)
		}
	}
	falsy := []string{"", "0", "false", "no", "anything"}
	for _, v := range falsy {
		if isTruthyEnvFlag(v) {
			t.Errorf("isTruthyEnvFlag(%q) = true, want false", v)
		}
	}
}

// TestForkFlagConflicts verifies no conflicts without --fork, and that each
// session-selection flag conflicts when --fork is set.
func TestForkFlagConflicts(t *testing.T) {
	// No --fork → never conflicts.
	if c := forkFlagConflicts(cli.Args{Session: "abc"}); len(c) != 0 {
		t.Errorf("no --fork → %v, want empty", c)
	}
	// --fork alone → no conflicts.
	if c := forkFlagConflicts(cli.Args{Fork: "abc"}); len(c) != 0 {
		t.Errorf("--fork alone → %v, want empty", c)
	}
	// --fork with each session-selection flag.
	c := forkFlagConflicts(cli.Args{Fork: "x", Session: "s"})
	if len(c) != 1 || c[0] != "--session" {
		t.Errorf("--fork+--session → %v, want [--session]", c)
	}
	c = forkFlagConflicts(cli.Args{Fork: "x", Continue: true, Resume: true, NoSession: true})
	if len(c) != 3 {
		t.Fatalf("--fork+3 flags → %d conflicts, want 3: %v", len(c), c)
	}
	want := map[string]bool{"--continue": true, "--resume": true, "--no-session": true}
	for _, flag := range c {
		if !want[flag] {
			t.Errorf("unexpected conflict %q", flag)
		}
	}
}

// TestResolveCliPaths verifies nil-safety, relative-path joining, and that
// absolute paths and URLs are left untouched.
func TestResolveCliPaths(t *testing.T) {
	if got := resolveCliPaths("/work", nil); got != nil {
		t.Errorf("nil → %v, want nil", got)
	}
	// An OS-absolute path (so filepath.IsAbs is true on every platform).
	absPath := filepath.Join(os.TempDir(), "absfile")
	if !filepath.IsAbs(absPath) {
		t.Fatalf("sanity: %q is not absolute on this platform", absPath)
	}
	got := resolveCliPaths("/work", []string{"./src", absPath, "https://x/y", "rel/file"})
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4: %v", len(got), got)
	}
	if !strings.HasSuffix(got[0], "src") || !strings.Contains(got[0], "work") {
		t.Errorf("relative ./src resolved to %q, want it joined under /work", got[0])
	}
	if got[1] != absPath {
		t.Errorf("absolute → %q, want untouched %q", got[1], absPath)
	}
	if got[2] != "https://x/y" {
		t.Errorf("url → %q, want untouched", got[2])
	}
	if got[3] == "rel/file" {
		t.Errorf("rel/file left relative (%q), want joined under /work", got[3])
	}
}

// TestCollectSettingsDiagnostics_Nil verifies nil-safety; a clean in-memory
// manager (no load errors) yields nil. (Full error-path coverage depends on a
// settings error-injection seam not yet exposed.)
func TestCollectSettingsDiagnostics_Nil(t *testing.T) {
	if d := collectSettingsDiagnostics(nil, "ctx"); d != nil {
		t.Errorf("nil manager → %v, want nil", d)
	}
	sm := core.InMemorySettingsManager(core.Settings{})
	if d := collectSettingsDiagnostics(sm, "ctx"); d != nil {
		t.Errorf("clean manager → %v, want nil", d)
	}
}

// TestDiagnosticStyle verifies the type→color/prefix mapping, including the
// default branch (DiagnosticInfo currently carries the bug value "string", which
// must render dim with no prefix — matching the TS info/else path).
func TestDiagnosticStyle(t *testing.T) {
	if c, p := diagnosticStyle(core.DiagnosticError); c != ansiRed || p != "Error: " {
		t.Errorf("error → (%q,%q), want red/Error:", c, p)
	}
	if c, p := diagnosticStyle(core.DiagnosticWarning); c != ansiYellow || p != "Warning: " {
		t.Errorf("warning → (%q,%q), want yellow/Warning:", c, p)
	}
	if c, p := diagnosticStyle(core.DiagnosticInfo); c != ansiDim || p != "" {
		t.Errorf("info(string-bug) → (%q,%q), want dim/empty", c, p)
	}
}

// TestReportDiagnostics verifies color-coded output and the info/"string"-bug
// dim handling by capturing the written bytes.
func TestReportDiagnostics(t *testing.T) {
	diags := []core.AgentSessionRuntimeDiagnostic{
		{Type: core.DiagnosticError, Message: "boom"},
		{Type: core.DiagnosticWarning, Message: "careful"},
		{Type: core.DiagnosticInfo, Message: "fyi"}, // bug value "string" → dim, no prefix
	}
	var buf bytes.Buffer
	reportDiagnostics(&buf, diags)
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], ansiRed) || !strings.Contains(lines[0], "Error: boom") {
		t.Errorf("error line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], ansiYellow) || !strings.Contains(lines[1], "Warning: careful") {
		t.Errorf("warning line = %q", lines[1])
	}
	// Info line: dim, NO "Error:"/"Warning:" prefix.
	if !strings.HasPrefix(lines[2], ansiDim) || !strings.Contains(lines[2], "fyi") {
		t.Errorf("info line = %q", lines[2])
	}
	if strings.Contains(lines[2], "Error:") || strings.Contains(lines[2], "Warning:") {
		t.Errorf("info line should have no prefix: %q", lines[2])
	}
}

// TestParseDiagnosticStyle verifies parse-time (cli.Diagnostic) styling.
func TestParseDiagnosticStyle(t *testing.T) {
	if c, l := parseDiagnosticStyle("error"); c != ansiRed || l != "Error" {
		t.Errorf("error → (%q,%q)", c, l)
	}
	if c, l := parseDiagnosticStyle("warning"); c != ansiYellow || l != "Warning" {
		t.Errorf("warning → (%q,%q)", c, l)
	}
	// Non-error defaults to warning styling (the TS only emits error/warning).
	if c, l := parseDiagnosticStyle("other"); c != ansiYellow || l != "Warning" {
		t.Errorf("other → (%q,%q), want yellow/Warning", c, l)
	}
}
