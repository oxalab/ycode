package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	llm "ycode/packages/llm/src"
)

// C12 parity pin: every built-in tool definition must carry the reference's
// exact promptSnippet (system-prompt tool list). ls was the lone missing one —
// its read-only contexts lost the "List directory contents" line. Asserts
// through the exposure sets (coding + read-only), not just the constructors,
// so a tool left out of a set fails too.
func TestBuiltinToolsCarryReferencePromptSnippets(t *testing.T) {
	want := map[string]string{
		"read":  "Read file contents",
		"bash":  "Execute bash commands (ls, grep, find, etc.)",
		"edit":  "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		"write": "Create or overwrite files",
		"grep":  "Search file contents for patterns (respects .gitignore)",
		"find":  "Find files by glob pattern (respects .gitignore)",
		"ls":    "List directory contents",
	}

	coding := CreateCodingToolDefinitions(".", &ToolsOptions{})
	readOnly := CreateReadOnlyToolDefinitions(".", &ToolsOptions{})

	seen := map[string]string{} // name -> snippet (union of both sets)
	for _, def := range append(coding, readOnly...) {
		if def == nil {
			continue
		}
		seen[def.Name] = def.PromptSnippet
	}

	for name, snippet := range want {
		got, ok := seen[name]
		if !ok {
			t.Errorf("tool %q not exposed in coding or read-only sets", name)
			continue
		}
		if got != snippet {
			t.Errorf("tool %q PromptSnippet = %q, want reference %q", name, got, snippet)
		}
	}
}

// --- C15: error casing + byte-only truncation ---

type fakeFileInfo struct{ dir bool }

func (f fakeFileInfo) Name() string       { return "x" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { if f.dir { return os.ModeDir }; return 0 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

type fakeLsOps struct {
	exists  bool
	isDir   bool
	entries []string
	readErr error
}

func (o *fakeLsOps) Exists(string) bool                  { return o.exists }
func (o *fakeLsOps) Stat(string) (os.FileInfo, error)    { return fakeFileInfo{dir: o.isDir}, nil }
func (o *fakeLsOps) Readdir(string) ([]string, error)    { return o.entries, o.readErr }

// C15: ls error strings must carry the reference's capitalization (ls.ts:133,
// :140, :149).
func TestLsErrorCasing(t *testing.T) {
	cases := []struct {
		name string
		ops  *fakeLsOps
		want string
	}{
		{"path not found", &fakeLsOps{exists: false}, "Path not found: "},
		{"not a directory", &fakeLsOps{exists: true, isDir: false}, "Not a directory: "},
		{"read error", &fakeLsOps{exists: true, isDir: true, readErr: fmt.Errorf("boom")}, "Cannot read directory: boom"},
	}
	for _, c := range cases {
		def := CreateLsToolDefinition(".", &LsToolOptions{Operations: c.ops})
		_, err := def.Execute(context.Background(), json.RawMessage(`{}`))
		if err == nil {
			t.Errorf("%s: expected error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %q, want prefix %q", c.name, err.Error(), c.want)
		}
	}
}

// C15: ls truncates bytes only — 2500 tiny entries (well under the 50KB byte
// cap) must NOT hit the old 2000-line default (TS passes
// maxLines: Number.MAX_SAFE_INTEGER, ls.ts:186).
func TestLsTruncatesBytesOnly(t *testing.T) {
	entries := make([]string, 2500)
	for i := range entries {
		entries[i] = fmt.Sprintf("f%04d", i)
	}
	ops := &fakeLsOps{exists: true, isDir: true, entries: entries}
	def := CreateLsToolDefinition(".", &LsToolOptions{Operations: ops})
	res, err := def.Execute(context.Background(), json.RawMessage(`{"limit": 3000}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	ter := res.(*ToolExecuteResult)
	text := ter.Content[0].(*llm.TextContent).Text
	if got := strings.Count(text, "\n") + 1; got < 2500 {
		t.Errorf("only %d lines in output, want all 2500 (line cap wrongly applied)", got)
	}
	if strings.Contains(text, "limit reached") {
		t.Errorf("byte-only run must not report truncation: %q", text[len(text)-120:])
	}
}
