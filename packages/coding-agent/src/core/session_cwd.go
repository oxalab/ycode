package ycode

import (
	"fmt"
	"os"
)

type SessionCwdIssue struct {
	SessionFile string
	SessionCwd 	string
	FallbackCwd string
}

type SessionCwdSource interface {
	GetCwd() string
	GetSesionFile() string
}

func GetMissingSessionCwdIssue(sessionManager SessionCwdSource, fallbackCwd string) *SessionCwdIssue {
	sessionFile := sessionManager.GetSesionFile()
	if sessionFile == "" {
		return nil
	}

	sessionCwd := sessionManager.GetCwd()
	if sessionCwd == "" {
		return nil
	}
	if _, err := os.Stat(sessionCwd); err == nil {
		return nil
	}
	return &SessionCwdIssue{
		SessionFile: sessionFile,
		SessionCwd: sessionCwd,
		FallbackCwd: fallbackCwd,
	}
}

func FormatMissingSessionCwdError(issue SessionCwdIssue) string {
	sessionFile := ""
	if issue.SessionFile != "" {
		sessionFile = fmt.Sprintf("\nSession file: %s", issue.SessionFile)
	}
	return fmt.Sprintf(
		"Stored session working directory does not exist: %s%s\nCurrent working directory: %s",
		issue.SessionCwd, sessionFile, issue.FallbackCwd,
	) 
}

func FormatMissingSessionCwdPrompt(issue SessionCwdIssue) string {
	return fmt.Sprintf(
		"cwd from session file does not exist\n%s\n\ncontinue in current cwd\n%s",
		issue.SessionCwd, issue.FallbackCwd,
	)
}

type MissingSessionCwdError struct {
	Issue SessionCwdIssue
}

func (e *MissingSessionCwdError) Error() string {
	return FormatMissingSessionCwdError(e.Issue)
}

func AssertSessionCwdExists(sessionManager SessionCwdSource, fallbackCwd string) error {
	issue := GetMissingSessionCwdIssue(sessionManager, fallbackCwd)
	if issue != nil {
		return &MissingSessionCwdError{Issue: *issue}
	}
	return nil
}

/* KEYFLAGS:
 1. TS class → Go struct: MissingSessionCwdError extends Error in TS. In Go, 
 it implements the error interface via Error() string. Standard pattern.

 2. Returns error not panics: TS assertSessionCwdExists throws. Go convention is to 
 return error — the caller decides what to do. Assert name is kept for readability but 
 it doesn't panic.

 3. Pointer returns for nil check: GetMissingSessionCwdIssue returns 
 *SessionCwdIssue (pointer) so nil means "no issue", matching TS's undefined return.

*/