package ycode

// session_ops.go ports the reference's delete/rename session helpers
// (session-selector.ts:625-666 deleteSessionFile, interactive-mode.ts:4313-4319
// renameSession). Both are filesystem-only operations: delete tries `trash`
// (recoverable) before falling back to permanent unlink; rename opens the
// session file and appends a session_info entry with the new name, preserving
// the full history (the same pattern the agent uses for any other rename).

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DeleteSessionMethod reports how a delete was performed.
type DeleteSessionMethod string

const (
	DeleteViaTrash  DeleteSessionMethod = "trash"  // moved to system trash (recoverable)
	DeleteViaUnlink DeleteSessionMethod = "unlink" // permanently removed
)

// DeleteSessionFile deletes a session file. It tries the `trash` CLI first
// (recoverable), then falls back to permanent removal. Mirrors the reference's
// deleteSessionFile (session-selector.ts:631-666).
//
// Returns the method used and any error. A missing file is treated as success
// (idempotent) — the caller wants the file gone, and it is.
func DeleteSessionFile(sessionPath string) (DeleteSessionMethod, error) {
	// Idempotent: if the file is already gone, treat as success.
	if _, err := os.Stat(sessionPath); err != nil && os.IsNotExist(err) {
		return DeleteViaUnlink, nil
	}

	// Try `trash` first. It's recoverable, which is much friendlier for
	// accidental deletes. If `trash` isn't installed, LookPath fails fast and
	// we fall through to unlink without spawning.
	if trashPath, err := exec.LookPath("trash"); err == nil {
		// `--` so a session path that starts with "-" isn't parsed as a flag.
		cmd := exec.Command(trashPath, append([]string{"--"}, sessionPath)...)
		// trash writes nothing we care about; discard stdout/stderr to keep
		// the TUI clean.
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err == nil {
			// Confirm the file is actually gone — `trash` can report success
			// on configurations that don't end up moving the file.
			if _, statErr := os.Stat(sessionPath); statErr != nil && os.IsNotExist(statErr) {
				return DeleteViaTrash, nil
			}
		}
	}

	// Fallback: permanent unlink.
	if err := os.Remove(sessionPath); err != nil {
		// Race: another process removed it between our stat and remove.
		if os.IsNotExist(err) {
			return DeleteViaUnlink, nil
		}
		return DeleteViaUnlink, fmt.Errorf("delete session %s: %w", sessionPath, err)
	}
	return DeleteViaUnlink, nil
}

// RenameSession opens the session file at sessionPath and appends a
// session_info entry carrying the new name. The file's history is preserved —
// the rename is just another entry, matching how the agent records every other
// rename (interactive-mode.ts:4313-4319 → SessionManager.open + appendSessionInfo).
//
// newName is trimmed; an empty name after trimming is rejected.
func RenameSession(sessionPath, newName string) error {
	trimmed := strings.TrimSpace(newName)
	if trimmed == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		return fmt.Errorf("session file not found: %w", err)
	}
	// OpenSessionManager loads + parses the file. We pass empty sessionDir /
	// cwd because we don't need them for an append-only operation.
	sm := OpenSessionManager(sessionPath, "", "")
	if sm == nil {
		return fmt.Errorf("could not open session file: %s", sessionPath)
	}
	sm.AppendSessionInfo(trimmed)
	return nil
}
