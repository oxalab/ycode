package tools

// Audit B3: the read tool's access gate must probe readability (the Go
// equivalent of TS fsAccess(path, R_OK)) and surface the raw fs error — the
// old os.Stat + "cannot read file: %s" hid whether the file was missing or
// unreadable, which the reference's model-visible error always shows.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llm "ycode/packages/llm/src"
)

func TestReadAccessOpProbesReadability(t *testing.T) {
	ops := defaultReadOps{}
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ops.Access(file); err != nil {
		t.Errorf("readable file should pass the access gate: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing.txt")
	err := ops.Access(missing)
	if err == nil {
		t.Fatal("missing file must fail the access gate")
	}
	// The raw fs error carries the path (TS model-visible parity: the message
	// distinguishes not-found from other failures).
	if !strings.Contains(err.Error(), "missing.txt") {
		t.Errorf("access error should mention the path, got: %v", err)
	}
}

func TestReadExecuteMissingFileSurfacesFsError(t *testing.T) {
	def := CreateReadToolDefinition(t.TempDir(), nil)
	_, err := def.Execute(context.Background(), json.RawMessage(`{"path":"gone.txt"}`))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	msg := err.Error()
	if strings.Contains(msg, "cannot read file") {
		t.Errorf("generic wrapper is gone; got: %q", msg)
	}
	if !strings.Contains(msg, "gone.txt") {
		t.Errorf("error should carry the real fs message with the path, got: %q", msg)
	}
}

func TestReadExecuteReadsSmallTextFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(file, []byte("hello b3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := CreateReadToolDefinition(dir, nil)
	res, err := def.Execute(context.Background(), json.RawMessage(`{"path":"hello.txt"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	toolRes, ok := res.(*ToolExecuteResult)
	if !ok || len(toolRes.Content) == 0 {
		t.Fatalf("unexpected result shape: %T %+v", res, res)
	}
	tc, ok := toolRes.Content[0].(*llm.TextContent)
	if !ok || !strings.Contains(tc.Text, "hello b3") {
		t.Errorf("file text missing from result: %+v", toolRes.Content[0])
	}
}

func TestReadExecuteDirectoryPathErrors(t *testing.T) {
	dir := t.TempDir()
	def := CreateReadToolDefinition(dir, nil)
	_, err := def.Execute(context.Background(), json.RawMessage(`{"path":"."}`))
	if err == nil {
		t.Fatal("reading a directory should error")
	}
	if !strings.Contains(err.Error(), filepath.Base(dir)) && !strings.Contains(err.Error(), ".") {
		t.Errorf("error should be informative, got: %q", err.Error())
	}
}
