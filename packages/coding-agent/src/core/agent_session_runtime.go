package ycode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/extensions"
)

// ============================================================================
// Types — mirrors reference/coding-agent/src/core/agent-session-runtime.ts
// ============================================================================

// CreateAgentSessionRuntimeResult is returned by runtime creation.
// The caller gets the created session, its cwd-bound services, and all
// diagnostics collected during setup.
// Mirrors TS CreateAgentSessionRuntimeResult (lines 17-20).
type CreateAgentSessionRuntimeResult struct {
	CreateAgentSessionResult         // embedded
	Services                *AgentSessionServices
	Diagnostics             []AgentSessionRuntimeDiagnostic
}

// CreateAgentSessionRuntimeFactory is a function that creates a full runtime
// for a target cwd and session manager. The factory closes over process-global
// fixed inputs, recreates cwd-bound services, and creates the AgentSession.
// Mirrors TS CreateAgentSessionRuntimeFactory (lines 29-34).
type CreateAgentSessionRuntimeFactory func(options CreateAgentSessionRuntimeFactoryOptions) (*CreateAgentSessionRuntimeResult, error)

// CreateAgentSessionRuntimeFactoryOptions holds the inputs for the factory.
type CreateAgentSessionRuntimeFactoryOptions struct {
	Cwd               string
	AgentDir          string
	SessionManager    *SessionManager
	SessionStartEvent *extensions.SessionStartEvent
}

// SessionImportFileNotFoundError is thrown when /import references a JSONL
// file path that does not exist.
// Mirrors TS SessionImportFileNotFoundError (lines 39-47).
type SessionImportFileNotFoundError struct {
	FilePath string
}

func (e *SessionImportFileNotFoundError) Error() string {
	return fmt.Sprintf("File not found: %s", e.FilePath)
}

// ============================================================================
// AgentSessionRuntime — mirrors TS lines 67-374
//
// Owns the current AgentSession plus its cwd-bound services.
// Session replacement methods tear down the current runtime first, then create
// and apply the next runtime.
// ============================================================================

type AgentSessionRuntime struct {
	session              *AgentSession
	services             *AgentSessionServices
	createRuntime        CreateAgentSessionRuntimeFactory
	diagnostics          []AgentSessionRuntimeDiagnostic
	modelFallbackMessage string
	rebindSession        func(session *AgentSession) error
	beforeSessionInvalidate func()
}

// NewAgentSessionRuntime creates a new runtime wrapper.
func NewAgentSessionRuntime(
	session *AgentSession,
	services *AgentSessionServices,
	createRuntime CreateAgentSessionRuntimeFactory,
	diagnostics []AgentSessionRuntimeDiagnostic,
	modelFallbackMessage string,
) *AgentSessionRuntime {
	return &AgentSessionRuntime{
		session:              session,
		services:             services,
		createRuntime:        createRuntime,
		diagnostics:          diagnostics,
		modelFallbackMessage: modelFallbackMessage,
	}
}

// --- Getters ---

func (r *AgentSessionRuntime) Services() *AgentSessionServices {
	return r.services
}

func (r *AgentSessionRuntime) Session() *AgentSession {
	return r.session
}

func (r *AgentSessionRuntime) Cwd() string {
	return r.services.Cwd
}

func (r *AgentSessionRuntime) Diagnostics() []AgentSessionRuntimeDiagnostic {
	result := make([]AgentSessionRuntimeDiagnostic, len(r.diagnostics))
	copy(result, r.diagnostics)
	return result
}

func (r *AgentSessionRuntime) ModelFallbackMessage() string {
	return r.modelFallbackMessage
}

// --- Lifecycle Setters ---

// SetRebindSession sets the callback invoked after session replacement.
// Mirrors TS setRebindSession (lines 99-101).
func (r *AgentSessionRuntime) SetRebindSession(fn func(session *AgentSession) error) {
	r.rebindSession = fn
}

// SetBeforeSessionInvalidate sets a synchronous callback that runs after
// session_shutdown handlers finish but before the current session is invalidated.
// Mirrors TS setBeforeSessionInvalidate (lines 111-113).
func (r *AgentSessionRuntime) SetBeforeSessionInvalidate(fn func()) {
	r.beforeSessionInvalidate = fn
}


// injectSessionCallbacks wires the runtime's session-replacement methods into the session
// so extension actions (NewSession, Fork, SwitchSession) can delegate to the runtime.
func (r *AgentSessionRuntime) injectSessionCallbacks() {
	r.session.onNewSession = func(opts map[string]any) (map[string]bool, error) {
		result, err := r.NewSession(&NewSessionRuntimeOptions{})
		if err != nil {
			return nil, err
		}
		return map[string]bool{"cancelled": result.Cancelled}, nil
	}

	r.session.onFork = func(entryID string, opts map[string]any) (map[string]bool, error) {
		result, err := r.Fork(entryID, &ForkOptions{})
		if err != nil {
			return nil, err
		}
		return map[string]bool{"cancelled": result.Cancelled}, nil
	}

	r.session.onSwitchSession = func(sessionPath string, opts map[string]any) (map[string]bool, error) {
		result, err := r.SwitchSession(sessionPath, &SwitchSessionOptions{})
		if err != nil {
			return nil, err
		}
		return map[string]bool{"cancelled": result.Cancelled}, nil
	}
}

// --- Private helpers ---

// emitBeforeSwitch emits the session_before_switch extension event.
// Mirrors TS emitBeforeSwitch (lines 115-130).
func (r *AgentSessionRuntime) emitBeforeSwitch(reason string, targetSessionFile string) (cancelled bool, err error) {
	runner := r.session.extensionRunner
	if runner == nil || !runner.HasHandlers("session_before_switch") {
		return false, nil
	}

	ctx := getContext()
	result, err := runner.Emit(ctx, map[string]any{
		"type":              "session_before_switch",
		"reason":            reason,
		"targetSessionFile": targetSessionFile,
	})
	if err != nil {
		return false, err
	}
	if resultMap, ok := result.(map[string]any); ok {
		if cancel, ok := resultMap["cancel"].(bool); ok && cancel {
			return true, nil
		}
	}
	return false, nil
}

// emitBeforeFork emits the session_before_fork extension event.
// Mirrors TS emitBeforeFork (lines 132-147).
func (r *AgentSessionRuntime) emitBeforeFork(entryID string, position string) (cancelled bool, err error) {
	runner := r.session.extensionRunner
	if runner == nil || !runner.HasHandlers("session_before_fork") {
		return false, nil
	}

	ctx := getContext()
	result, err := runner.Emit(ctx, map[string]any{
		"type":     "session_before_fork",
		"entryId":  entryID,
		"position": position,
	})
	if err != nil {
		return false, err
	}
	if resultMap, ok := result.(map[string]any); ok {
		if cancel, ok := resultMap["cancel"].(bool); ok && cancel {
			return true, nil
		}
	}
	return false, nil
}

// teardownCurrent emits session_shutdown and disposes the current session.
// Mirrors TS teardownCurrent (lines 149-157).
func (r *AgentSessionRuntime) teardownCurrent(reason string, targetSessionFile string) error {
	runner := r.session.extensionRunner
	if runner != nil {
		extensions.EmitSessionShutdownEvent(runner, &extensions.SessionShutdownEvent{
			Type:             "session_shutdown",
			Reason:           reason,
			TargetSessionFile: targetSessionFile,
		})
	}
	if r.beforeSessionInvalidate != nil {
		r.beforeSessionInvalidate()
	}
	r.session.Dispose()
	return nil
}

// apply swaps in the result from a new runtime creation.
// Mirrors TS apply (lines 159-164).
func (r *AgentSessionRuntime) apply(result *CreateAgentSessionRuntimeResult) {
	r.session = result.Session
	r.services = result.Services
	r.diagnostics = result.Diagnostics
	r.modelFallbackMessage = result.ModelFallbackMessage
	r.injectSessionCallbacks()
}

// finishSessionReplacement invokes rebind and withSession callbacks.
// Mirrors TS finishSessionReplacement (lines 166-173).
func (r *AgentSessionRuntime) finishSessionReplacement(withSession func(ctx *extensions.ReplacedSessionContext) error) error {
	if r.rebindSession != nil {
		if err := r.rebindSession(r.session); err != nil {
			return err
		}
	}
	if withSession != nil {
		ctx := r.session.CreateReplacedSessionContext()
		if err := withSession(ctx); err != nil {
			return err
		}
	}
	return nil
}

// --- Public session replacement methods ---

// SwitchSession switches to an existing session.
// Mirrors TS switchSession (lines 175-198).
func (r *AgentSessionRuntime) SwitchSession(sessionPath string, opts *SwitchSessionOptions) (*SessionReplacementResult, error) {
	var cwdOverride string
	var withSession func(ctx *extensions.ReplacedSessionContext) error
	if opts != nil {
		cwdOverride = opts.CwdOverride
		withSession = opts.WithSession
	}

	beforeResult, err := r.emitBeforeSwitch("resume", sessionPath)
	if err != nil {
		return nil, err
	}
	if beforeResult {
		return &SessionReplacementResult{Cancelled: true}, nil
	}

	previousSessionFile := r.session.GetSessionFile()
	sessionManager := OpenSessionManager(sessionPath, "", cwdOverride)
	_ = r.Cwd() // cwd assertion will be added once SessionCwdSource interface is fixed

	if err := r.teardownCurrent("resume", sessionManager.GetSessionFile()); err != nil {
		return nil, err
	}

	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:            sessionManager.GetCwd(),
		AgentDir:       r.services.AgentDir,
		SessionManager: sessionManager,
		SessionStartEvent: &extensions.SessionStartEvent{
			Type:                "session_start",
			Reason:              "resume",
			PreviousSessionFile: previousSessionFile,
		},
	})
	if err != nil {
		return nil, err
	}
	r.apply(result)

	if err := r.finishSessionReplacement(withSession); err != nil {
		return nil, err
	}
	return &SessionReplacementResult{Cancelled: false}, nil
}

// NewSession creates a new session.
// Mirrors TS newSession (lines 200-232).
func (r *AgentSessionRuntime) NewSession(opts *NewSessionRuntimeOptions) (*SessionReplacementResult, error) {
	var parentSession string
	var setup func(sessionManager *SessionManager) error
	var withSession func(ctx *extensions.ReplacedSessionContext) error
	if opts != nil {
		parentSession = opts.ParentSession
		setup = opts.Setup
		withSession = opts.WithSession
	}

	beforeResult, err := r.emitBeforeSwitch("new", "")
	if err != nil {
		return nil, err
	}
	if beforeResult {
		return &SessionReplacementResult{Cancelled: true}, nil
	}

	previousSessionFile := r.session.GetSessionFile()
	sessionDir := r.session.SessionManager.GetSessionDir()
	sessionManager := NewSessionManager(r.Cwd(), sessionDir)

	if parentSession != "" {
		sessionManager.NewSessionEntry(parentSession)
	}

	if err := r.teardownCurrent("new", sessionManager.GetSessionFile()); err != nil {
		return nil, err
	}

	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:      r.Cwd(),
		AgentDir: r.services.AgentDir,
		SessionManager: sessionManager,
		SessionStartEvent: &extensions.SessionStartEvent{
			Type:                "session_start",
			Reason:              "new",
			PreviousSessionFile: previousSessionFile,
		},
	})
	if err != nil {
		return nil, err
	}
	r.apply(result)

	if setup != nil {
		if err := setup(r.session.SessionManager); err != nil {
			return nil, err
		}
		// Rebuild agent messages from updated session context
		ctx := r.session.SessionManager.BuildSessionContext()
		r.session.Agent.SetMessages(ctx.Messages)
	}

	if err := r.finishSessionReplacement(withSession); err != nil {
		return nil, err
	}
	return &SessionReplacementResult{Cancelled: false}, nil
}

// Fork creates a forked session at the specified entry.
// Mirrors TS fork (lines 234-320).
func (r *AgentSessionRuntime) Fork(entryID string, opts *ForkOptions) (*ForkResult, error) {
	position := "before"
	var withSession func(ctx *extensions.ReplacedSessionContext) error
	if opts != nil {
		if opts.Position != "" {
			position = opts.Position
		}
		withSession = opts.WithSession
	}

	beforeResult, err := r.emitBeforeFork(entryID, position)
	if err != nil {
		return nil, err
	}
	if beforeResult {
		return &ForkResult{Cancelled: true}, nil
	}

	var selectedText string
	selectedEntry := r.session.SessionManager.GetEntry(entryID)
	if selectedEntry == nil {
		return nil, fmt.Errorf("invalid entry ID for forking")
	}

	var targetLeafID string
	if position == "at" {
		targetLeafID = selectedEntry.ID
	} else {
		// "before" — entry must be a user message
		if selectedEntry.Type != compaction.EntryTypeMessage || selectedEntry.Message == nil {
			return nil, fmt.Errorf("invalid entry ID for forking")
		}
		role := getRoleFromMessage(selectedEntry.Message)
		if role != "user" {
			return nil, fmt.Errorf("invalid entry ID for forking")
		}
		targetLeafID = selectedEntry.ParentID
		selectedText = getTextFromMessage(selectedEntry.Message)
	}

	previousSessionFile := r.session.GetSessionFile()
	isPersisted := r.session.SessionManager.IsPersisted()

	if isPersisted {
		currentSessionFile := r.session.GetSessionFile()
		if currentSessionFile == "" {
			return nil, fmt.Errorf("persisted session is missing a session file")
		}
		sessionDir := r.session.SessionManager.GetSessionDir()

		if targetLeafID == "" {
			// No target leaf — create new session with parent
			sessionManager := NewSessionManager(r.Cwd(), sessionDir)
			sessionManager.NewSessionEntry(currentSessionFile)
			if err := r.teardownCurrent("fork", sessionManager.GetSessionFile()); err != nil {
				return nil, err
			}
			result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
				Cwd:      r.Cwd(),
				AgentDir: r.services.AgentDir,
				SessionManager: sessionManager,
				SessionStartEvent: &extensions.SessionStartEvent{
					Type:                "session_start",
					Reason:              "fork",
					PreviousSessionFile: previousSessionFile,
				},
			})
			if err != nil {
				return nil, err
			}
			r.apply(result)
			if err := r.finishSessionReplacement(withSession); err != nil {
				return nil, err
			}
			return &ForkResult{Cancelled: false, SelectedText: selectedText}, nil
		}

		// Branch at the target leaf
		sourceManager := OpenSessionManager(currentSessionFile, sessionDir, "")
		forkedSessionPath := sourceManager.CreateBranchedSession(targetLeafID)
		if forkedSessionPath == "" {
			return nil, fmt.Errorf("failed to create forked session")
		}
		sessionManager := OpenSessionManager(forkedSessionPath, sessionDir, "")
		if err := r.teardownCurrent("fork", sessionManager.GetSessionFile()); err != nil {
			return nil, err
		}
		result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
			Cwd:      sessionManager.GetCwd(),
			AgentDir: r.services.AgentDir,
			SessionManager: sessionManager,
			SessionStartEvent: &extensions.SessionStartEvent{
				Type:                "session_start",
				Reason:              "fork",
				PreviousSessionFile: previousSessionFile,
			},
		})
		if err != nil {
			return nil, err
		}
		r.apply(result)
		if err := r.finishSessionReplacement(withSession); err != nil {
			return nil, err
		}
		return &ForkResult{Cancelled: false, SelectedText: selectedText}, nil
	}

	// Non-persisted session
	sessionManager := r.session.SessionManager
	if targetLeafID == "" {
		sessionManager.NewSessionEntry(r.session.GetSessionFile())
	} else {
		sessionManager.CreateBranchedSession(targetLeafID)
	}
	if err := r.teardownCurrent("fork", sessionManager.GetSessionFile()); err != nil {
		return nil, err
	}
	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:      r.Cwd(),
		AgentDir: r.services.AgentDir,
		SessionManager: sessionManager,
		SessionStartEvent: &extensions.SessionStartEvent{
			Type:                "session_start",
			Reason:              "fork",
			PreviousSessionFile: previousSessionFile,
		},
	})
	if err != nil {
		return nil, err
	}
	r.apply(result)
	if err := r.finishSessionReplacement(withSession); err != nil {
		return nil, err
	}
	return &ForkResult{Cancelled: false, SelectedText: selectedText}, nil
}

// ImportFromJsonl imports a session JSONL file and switches to it.
// Mirrors TS importFromJsonl (lines 329-364).
func (r *AgentSessionRuntime) ImportFromJsonl(inputPath string, cwdOverride string) (*SessionReplacementResult, error) {
	resolvedPath, err := filepath.Abs(inputPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path %q: %w", inputPath, err)
	}

	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return nil, &SessionImportFileNotFoundError{FilePath: resolvedPath}
	}

	sessionDir := r.session.SessionManager.GetSessionDir()
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session dir: %w", err)
	}

	destinationPath := filepath.Join(sessionDir, filepath.Base(resolvedPath))

	beforeResult, err := r.emitBeforeSwitch("resume", destinationPath)
	if err != nil {
		return nil, err
	}
	if beforeResult {
		return &SessionReplacementResult{Cancelled: true}, nil
	}

	previousSessionFile := r.session.GetSessionFile()

	// Copy file if source and destination differ
	if resolvedPath != destinationPath {
		if err := copyFile(resolvedPath, destinationPath); err != nil {
			return nil, fmt.Errorf("failed to copy session file: %w", err)
		}
	}

	sessionManager := OpenSessionManager(destinationPath, sessionDir, cwdOverride)
	_ = r.Cwd() // cwd assertion will be added once SessionCwdSource interface is fixed

	if err := r.teardownCurrent("resume", sessionManager.GetSessionFile()); err != nil {
		return nil, err
	}

	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:      sessionManager.GetCwd(),
		AgentDir: r.services.AgentDir,
		SessionManager: sessionManager,
		SessionStartEvent: &extensions.SessionStartEvent{
			Type:                "session_start",
			Reason:              "resume",
			PreviousSessionFile: previousSessionFile,
		},
	})
	if err != nil {
		return nil, err
	}
	r.apply(result)

	if err := r.finishSessionReplacement(nil); err != nil {
		return nil, err
	}
	return &SessionReplacementResult{Cancelled: false}, nil
}

// Dispose shuts down the runtime and disposes the session.
// Mirrors TS dispose (lines 366-374).
func (r *AgentSessionRuntime) Dispose() error {
	runner := r.session.extensionRunner
	if runner != nil {
		extensions.EmitSessionShutdownEvent(runner, &extensions.SessionShutdownEvent{
			Type:   "session_shutdown",
			Reason: "quit",
		})
	}
	if r.beforeSessionInvalidate != nil {
		r.beforeSessionInvalidate()
	}
	r.session.Dispose()
	return nil
}

// ============================================================================
// Options types for session replacement methods
// ============================================================================

type SwitchSessionOptions struct {
	CwdOverride string
	WithSession func(ctx *extensions.ReplacedSessionContext) error
}

type NewSessionRuntimeOptions struct {
	ParentSession string
	Setup         func(sessionManager *SessionManager) error
	WithSession   func(ctx *extensions.ReplacedSessionContext) error
}

type ForkOptions struct {
	Position   string // "before" | "at"
	WithSession func(ctx *extensions.ReplacedSessionContext) error
}

type SessionReplacementResult struct {
	Cancelled bool `json:"cancelled"`
}

type ForkResult struct {
	Cancelled    bool
	SelectedText string
}

// ============================================================================
// CreateAgentSessionRuntime — standalone function
// Mirrors TS createAgentSessionRuntime (lines 382-400)
// ============================================================================

func CreateAgentSessionRuntime(
	createRuntime CreateAgentSessionRuntimeFactory,
	options CreateAgentSessionRuntimeFactoryOptions,
) (*AgentSessionRuntime, error) {
	_ = options.Cwd // cwd assertion will be added once SessionCwdSource interface is fixed
	result, err := createRuntime(options)
	if err != nil {
		return nil, err
	}
		runtime := NewAgentSessionRuntime(
			result.Session,
			result.Services,
			createRuntime,
			result.Diagnostics,
			result.ModelFallbackMessage,
		)
		runtime.injectSessionCallbacks()
		return runtime, nil
	}

// ============================================================================
// ExtractUserMessageText — mirrors TS lines 49-58
// ============================================================================

// ExtractUserMessageText extracts text content from a message content array.
func ExtractUserMessageText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, part := range c {
			if m, ok := part.(map[string]any); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		result := ""
		for _, p := range parts {
			result += p
		}
		return result
	default:
		return ""
	}
}

// ============================================================================
// Helpers
// ============================================================================

// getContext returns a background context for extension events.
func getContext() context.Context {
	return context.Background()
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// getRoleFromMessage extracts the role from a session entry message.
func getRoleFromMessage(msg any) string {
	if m, ok := msg.(interface{ GetRole() string }); ok {
		return m.GetRole()
	}
	return ""
}

// getTextFromMessage extracts text from a session entry message.
func getTextFromMessage(msg any) string {
	if m, ok := msg.(interface{ GetContent() string }); ok {
		return m.GetContent()
	}
	return ""
}
