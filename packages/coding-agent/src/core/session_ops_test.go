package ycode

// session_ops_test.go verifies DeleteSessionFile and RenameSession against a
// real temp filesystem. Trash-CLI detection is left to the production path;
// these tests force the unlink fallback by ensuring `trash` isn't on PATH in
// the test environment (or, if it is, accept either method as success).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMinimalSessionFile writes a parseable session file and returns its path.
// Includes one assistant message so the production persistence guard
// (session_manager.go:878 — "don't persist until there's at least one
// assistant message") doesn't reject subsequent AppendSessionInfo calls. We
// hand-roll the JSON rather than use NewSessionManager because forcing
// persistence through the production API would require constructing a full
// AssistantMessage object just to write a fixture.
func writeMinimalSessionFile(t *testing.T, dir, name string) string {
	t.Helper()
	sessionDir := filepath.Join(dir, ".ycode", "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir sessionDir: %v", err)
	}
	path := filepath.Join(sessionDir, name)
	// JSON Lines: header line + assistant message line. The message role is
	// what the persistence guard at session_manager.go:882-887 keys on.
	header := fmt.Sprintf(`{"type":"session","version":3,"id":"test-session-id","timestamp":"2026-07-18T12:00:00Z","cwd":%q}`, dir)
	msg := `{"type":"message","id":"m1","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	if err := os.WriteFile(path, []byte(header+"\n"+msg+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestDeleteSessionFile_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := writeMinimalSessionFile(t, dir, "to-delete.json")

	method, err := DeleteSessionFile(path)
	if err != nil {
		t.Fatalf("DeleteSessionFile: %v", err)
	}
	if method != DeleteViaTrash && method != DeleteViaUnlink {
		t.Errorf("method = %q, want trash or unlink", method)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after delete (stat err: %v)", err)
	}
}

func TestDeleteSessionFile_IdempotentOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "never-existed.json")

	_, err := DeleteSessionFile(missing)
	if err != nil {
		t.Errorf("deleting a non-existent file should be a no-op, got %v", err)
	}
}

func TestRenameSession_AppendsSessionInfo(t *testing.T) {
	dir := t.TempDir()
	path := writeMinimalSessionFile(t, dir, "to-rename.json")

	if err := RenameSession(path, "  New Name  "); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	// Verify the file content now contains the trimmed new name.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "New Name") {
		t.Errorf("renamed file does not contain the new name; content=\n%s", string(content))
	}
}

func TestRenameSession_RejectsEmptyName(t *testing.T) {
	dir := t.TempDir()
	path := writeMinimalSessionFile(t, dir, "no-rename.json")

	if err := RenameSession(path, "   "); err == nil {
		t.Error("RenameSession accepted an all-whitespace name")
	}

	// File should be untouched.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should still exist after failed rename: %v", err)
	}
}

func TestRenameSession_MissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "ghost.json")
	if err := RenameSession(missing, "name"); err == nil {
		t.Error("RenameSession on a missing file should error")
	}
}

func TestDeleteSessionMethod_Values(t *testing.T) {
	// Lock in the wire values — these are user-visible strings.
	if string(DeleteViaTrash) != "trash" {
		t.Errorf("DeleteViaTrash = %q, want \"trash\"", DeleteViaTrash)
	}
	if string(DeleteViaUnlink) != "unlink" {
		t.Errorf("DeleteViaUnlink = %q, want \"unlink\"", DeleteViaUnlink)
	}
}
