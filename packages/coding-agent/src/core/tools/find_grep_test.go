package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ycode/packages/coding-agent/src/config"
	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

// ============================================================================
// Pure helpers
// ============================================================================

// C5: the old code TrimRight'd the cutset "/r", mangling paths
// ("…/driver" → "…/drive") and destroying directory markers.
func TestRelativizeFindResults(t *testing.T) {
	searchPath := string(filepath.Separator) + "base"

	got := relativizeFindResults([]string{
		filepath.Join(searchPath, "driver"), // must NOT lose its trailing 'r'
		filepath.Join(searchPath, "folder") + string(filepath.Separator), // dir marker kept
		filepath.Join(searchPath, "nested", "a.txt"),
		filepath.Join(string(filepath.Separator) + "elsewhere", "x.txt"), // outside → Rel
		"   ",
	}, searchPath)

	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "drive\n") || strings.HasSuffix(joined, "drive") {
		t.Errorf("path mangled (C5 regression): %q", joined)
	}
	if !strings.Contains(got[0], "driver") {
		t.Errorf("expected driver intact, got %q", got[0])
	}
	if !strings.HasSuffix(got[1], "/") {
		t.Errorf("trailing dir slash should be preserved, got %q", got[1])
	}
	if !strings.Contains(got[2], "nested/a.txt") {
		t.Errorf("expected posix-relative nested path, got %q", got[2])
	}
	if len(got) != 4 {
		t.Errorf("blank line should be dropped, got %d results: %q", len(got), got)
	}
}

// C6: relative patterns containing "/" must anchor as "**/"+pattern so
// top-level matches (e.g. src/a.ts for "src/*.ts") are found.
func TestBuildFdPattern(t *testing.T) {
	cases := []struct {
		in        string
		want      string
		fullPath  bool
	}{
		{"src/*.ts", "**/src/*.ts", true},
		{"**/x", "**/x", true},
		{"/abs/*.go", "/abs/*.go", true},
		{"**", "**", false}, // no "/" → no --full-path (TS only enters that branch on "/")
		{"plain-name", "plain-name", false},
	}
	for _, tc := range cases {
		got, full := buildFdPattern(tc.in)
		if got != tc.want || full != tc.fullPath {
			t.Errorf("buildFdPattern(%q) = (%q, %v), want (%q, %v)", tc.in, got, full, tc.want, tc.fullPath)
		}
	}
}

// ============================================================================
// Integration (real ripgrep; skipped when rg is not on PATH)
// ============================================================================

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not available: %v", err)
	}
}

// grepWorkspace builds the grep tool and a temp workspace with content.
func grepWorkspace(t *testing.T) (*extensions.ToolDefinition, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "long.txt"),
		[]byte("start "+strings.Repeat("x", 200*1024)+" needle end\nshort needle line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return CreateGrepToolDefinition(dir, nil), dir
}

// runGrep executes the tool with a watchdog so a deadlock fails the test
// instead of wedging the suite (pattern from bash_exec_test.go).
func runGrep(t *testing.T, def *extensions.ToolDefinition, args string) (any, error) {
	t.Helper()
	type res struct {
		r   any
		err error
	}
	ch := make(chan res, 1)
	go func() {
		r, err := def.Execute(context.Background(), json.RawMessage(args))
		ch <- res{r, err}
	}()
	select {
	case got := <-ch:
		return got.r, got.err
	case <-time.After(30 * time.Second):
		t.Fatal("grep Execute did not return within 30s (deadlock regression)")
		return nil, nil
	}
}

func resultText(t *testing.T, r any) string {
	t.Helper()
	tr, ok := r.(*ToolExecuteResult)
	if !ok {
		t.Fatalf("expected *ToolExecuteResult, got %T", r)
	}
	var b strings.Builder
	for _, block := range tr.Content {
		if tc, ok := block.(*llm.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// C7: a 200KB matched line must not silently end scanning (64KB default cap)
// or deadlock the pipe; the match must be found.
func TestGrepLongLineNoDeadlock(t *testing.T) {
	requireRipgrep(t)
	def, _ := grepWorkspace(t)

	r, err := runGrep(t, def, `{"pattern":"needle","path":""}`)
	if err != nil {
		t.Fatalf("grep error: %v", err)
	}
	out := resultText(t, r)
	if !strings.Contains(out, "needle") {
		t.Errorf("match missing from output: %q", out)
	}
}

// C7: a bad regex must surface rg's stderr, not "No matches found".
func TestGrepBadRegexIsError(t *testing.T) {
	requireRipgrep(t)
	def, dir := grepWorkspace(t)

	_, err := runGrep(t, def, `{"pattern":"[unclosed","path":""}`)
	_ = dir
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
	if strings.Contains(err.Error(), "No matches found") {
		t.Errorf("bad regex masqueraded as no-match: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "regex") && !strings.Contains(err.Error(), "error") {
		t.Errorf("error should carry rg's diagnostics: %q", err.Error())
	}
}

// TS parity: no matches → exactly "No matches found" (no trailing period).
func TestGrepNoMatchesWording(t *testing.T) {
	requireRipgrep(t)
	def, _ := grepWorkspace(t)

	r, err := runGrep(t, def, `{"pattern":"zzz-no-such-token-zzz","path":""}`)
	if err != nil {
		t.Fatalf("grep error: %v", err)
	}
	if out := resultText(t, r); out != "No matches found" {
		t.Errorf("no-match text = %q, want %q", out, "No matches found")
	}
}

// LOW typo folded into C7: the limit notice must say "limit", not "limti".
func TestGrepLimitNoticeTypo(t *testing.T) {
	requireRipgrep(t)
	def, _ := grepWorkspace(t)

	r, err := runGrep(t, def, `{"pattern":"needle","path":"","limit":1}`)
	if err != nil {
		t.Fatalf("grep error: %v", err)
	}
	out := resultText(t, r)
	if strings.Contains(out, "limti") {
		t.Errorf("typo still present: %q", out)
	}
	if !strings.Contains(out, "matches limit reached") {
		t.Errorf("limit notice missing: %q", out)
	}
}

// ============================================================================
// C8: ensureTool wiring — hard-fail wordings and (gated) real download
// ============================================================================

// ensureUnavailableEnv points the managed bin dir at an empty temp dir, empties
// PATH and enables offline mode, so EnsureTool can neither find nor download
// fd/rg. Restored by t.Setenv cleanup.
func ensureUnavailableEnv(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvAgentDir, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("YCODE_OFFLINE", "1")
}

// C8: find hard-fails when fd is unavailable (reference find.ts wording). The
// old code silently fell back to the gitignore-blind WalkDir — the tool
// description promises .gitignore respect, which only fd provides.
func TestFindHardFailsWithoutFd(t *testing.T) {
	ensureUnavailableEnv(t)

	def := CreateFindToolDefinition(t.TempDir(), nil)
	_, err := def.Execute(context.Background(), json.RawMessage(`{"pattern":"*.txt"}`))
	if err == nil {
		t.Fatal("expected an error when fd is unavailable (no silent WalkDir fallback)")
	}
	if err.Error() != "fd is not available and could not be downloaded" {
		t.Errorf("got %q, want reference wording", err.Error())
	}
}

// C8: grep failure wording is reference-exact (was "not available on PATH").
func TestGrepUnavailableWording(t *testing.T) {
	ensureUnavailableEnv(t)

	def := CreateGrepToolDefinition(t.TempDir(), nil)
	_, err := def.Execute(context.Background(), json.RawMessage(`{"pattern":"x"}`))
	if err == nil {
		t.Fatal("expected an error when rg is unavailable")
	}
	if err.Error() != "ripgrep (rg) is not available and could not be downloaded" {
		t.Errorf("got %q, want reference wording", err.Error())
	}
}

// C8 end-to-end: real GitHub download of fd (skipped unless
// YCODE_TEST_DOWNLOADS=1 — same spirit as requireRipgrep's gate).
func TestEnsureToolDownloadsFd(t *testing.T) {
	if os.Getenv("YCODE_TEST_DOWNLOADS") != "1" {
		t.Skip("set YCODE_TEST_DOWNLOADS=1 to run the real fd download")
	}
	t.Setenv(config.EnvAgentDir, t.TempDir())

	path := EnsureTool("fd", true)
	if path == "" {
		t.Fatal("download failed")
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		t.Fatalf("downloaded fd does not run: %v", err)
	}
	if !strings.Contains(string(out), "fd") {
		t.Errorf("unexpected --version output: %q", out)
	}
}

// --- C15: find custom-ops notice form, abort wording, byte-only truncation ---

type fakeFindOps struct {
	entries []string
}

func (f *fakeFindOps) Exists(string) bool { return true }
func (f *fakeFindOps) Glob(string, string, []string, int) []string {
	if len(f.entries) > 0 {
		return f.entries
	}
	return nil
}

func newFindDef(ops *fakeFindOps) *extensions.ToolDefinition {
	return CreateFindToolDefinition(".", &FindToolOptions{Operations: ops})
}

// C15: the custom-operations path emits the SHORT limit notice (find.ts:197);
// the long "Use limit=N for more" hint belongs to the default fd path only.
func TestFindCustomOpsLimitNoticeShortForm(t *testing.T) {
	ops := &fakeFindOps{entries: []string{"a", "b", "c"}}
	res, err := newFindDef(ops).Execute(context.Background(), json.RawMessage(`{"pattern":"*","limit":3}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := res.(*ToolExecuteResult).Content[0].(*llm.TextContent).Text
	if !strings.Contains(text, "[3 results limit reached]") {
		t.Errorf("custom-ops output missing short-form notice: %q", text)
	}
	if strings.Contains(text, "Use limit=6") {
		t.Errorf("custom-ops output must not carry the fd-path hint: %q", text)
	}
}

// C15: find truncates bytes only (TS maxLines: Number.MAX_SAFE_INTEGER,
// find.ts:192) — 3000 tiny lines must not hit the 2000-line default.
func TestFindCustomOpsByteOnlyTruncation(t *testing.T) {
	entries := make([]string, 3000)
	for i := range entries {
		entries[i] = fmt.Sprintf("f%04d", i)
	}
	res, err := newFindDef(&fakeFindOps{entries: entries}).Execute(context.Background(), json.RawMessage(`{"pattern":"*"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := res.(*ToolExecuteResult).Content[0].(*llm.TextContent).Text
	if strings.Count(text, "\n")+1 < 3000 {
		t.Errorf("line cap still applied: only %d lines for 3000 tiny results", strings.Count(text, "\n")+1)
	}
}

// C15: find's abort surfaces as the reference's "Operation aborted"
// (find.ts:320), not the raw context error.
func TestFindAbortWording(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newFindDef(&fakeFindOps{}).Execute(ctx, json.RawMessage(`{"pattern":"*"}`))
	if err == nil || err.Error() != "Operation aborted" {
		t.Errorf("abort error = %v, want Operation aborted", err)
	}
}
