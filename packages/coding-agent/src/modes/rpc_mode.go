// RPC mode: headless operation with a JSON stdin/stdout protocol. Ports
// reference/coding-agent/src/modes/rpc/rpc-mode.ts.
//
// Commands arrive as JSON lines on stdin; responses, agent events, and
// extension-UI requests are emitted as JSON lines on stdout. Used for embedding
// the agent in other applications (ycode --mode rpc).
//
// ============================================================================
// Deviations + honest deferrals (documented in-file)
// ============================================================================
//
// ExtensionUIContext: in the TS this is a ~30-method interface (select/confirm/
// input/editor/notify/setStatus/setWidget/setTitle/theme/setEditorText/...). In
// the Go port it is a 5-field STRUCT (Select/Confirm/Input/Notify/Status — see
// core/extensions/runner.go). RPC mode therefore wires exactly those five via
// the dialog protocol; editor/setWidget/setTitle/theme/setEditorText and the
// rest have no Go UIContext field and are simply not emitted. This is the
// current port surface, not a regression.
//
// CommandContextActions: deferred (same as print mode). grep finds zero
// construction sites anywhere in the Go port, so binding them here would be
// unused, untested wiring. Only ShutdownHandler + OnError are bound.
//
// output-guard / killTrackedDetachedChildren: not ported (same as print mode).
// Output goes straight to os.Stdout, serialized by a mutex; signal handling
// disposes the runtime and exits with the TS codes (143 SIGTERM, 129 SIGHUP).
//
// Request IDs: an atomic counter ("rpc-<n>"), not crypto.randomUUID(). They
// exist only for client correlation, so a deterministic counter is fine.
//
// prompt concurrency: the TS fires `void session.prompt(...)` (async) and emits
// the response from the preflight callback. Go's Prompt is synchronous, so the
// handler runs it in a goroutine, capturing the session ref at dispatch time
// (matching the TS closure). Multiple in-flight prompts serialize inside the
// agent's event queue, as in the TS. All stdout writes are mutex-guarded.
// Dispatch concurrency: every stdin line runs on its own goroutine (mirror of
// the TS fire-and-forget), so aborts preempt in-flight commands.
//
// JSON event stream: Subscribe delivers AgentSessionEvent (no json tags →
// PascalCase), the same wire divergence as print mode (documented there).
//
// Genuinely-missing backing (handlers return an honest error rather than
// fabricating success):
//   - set_auto_retry: no SetAutoRetryEnabled on AgentSession.
//   - get_fork_messages: no GetUserMessagesForForking on AgentSession.
// Reimplemented from primitives (no dedicated getter, but computable):
//   - get_last_assistant_text: scans messages backward for the last assistant
//     turn (lastAssistantText).
//   - get_state.pendingMessageCount: deferred to 0 (no getter; would need a
//     queue-length accessor on AgentSession/Agent).

package modes

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	sdk "ycode/packages/agent-sdk/src"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

// RunRPCMode runs headless RPC mode (TS runRpcMode). It binds extensions to the
// RPC UI context, subscribes to agent events, registers signal handlers, then
// reads JSONL commands from stdin until EOF or a shutdown signal. It blocks for
// the lifetime of the process (shutdown calls os.Exit); it returns an error
// only if the initial rebind fails.
func RunRPCMode(runtimeHost *core.AgentSessionRuntime) error {
	srv := &rpcServer{
		runtimeHost: runtimeHost,
		session:     runtimeHost.Session(),
		pending:     make(map[string]chan RpcExtensionUIResponse),
	}

	// Session replacement (fork/switch/new_session) re-binds extensions + events.
	runtimeHost.SetRebindSession(srv.rebind)
	if err := srv.rebind(srv.session); err != nil {
		return err
	}

	srv.registerSignalHandlers()

	// Read commands line-by-line; EOF triggers a clean shutdown. Each line
	// dispatches on its own goroutine (TS: `void handleInputLine(line)`,
	// rpc-mode.ts:744) so a blocking handler (bash, compact, a dialog-bound
	// switch) can't starve the reader — abort_bash preempts in-flight work.
	// Responses interleave on stdout via outputMu, as in the TS.
	go func() {
		_ = AttachJSONLLineReader(os.Stdin, func(line string) {
			go srv.handleLine(line)
		})
		srv.shutdown(0)
	}()

	// Block forever (shutdown exits the process). Mirrors TS `return new Promise(()=>{})`.
	select {}
}

// rpcServer holds the shared, mutable RPC-mode state.
type rpcServer struct {
	runtimeHost    *core.AgentSessionRuntime
	session        *core.AgentSession
	unsubscribe    func()
	outputMu       sync.Mutex // serializes stdout writes (events + responses + UI requests)
	pendingMu      sync.Mutex // guards pending
	pending        map[string]chan RpcExtensionUIResponse
	idCounter      uint64 // atomic — request IDs
	shutdownFlag   int32  // atomic — set by the extension shutdown handler
	shuttingDown   int32  // atomic — guards shutdown re-entrancy
}

// output serializes a value as a JSON line to stdout.
func (s *rpcServer) output(v any) {
	line, err := SerializeJSONLine(v)
	if err != nil {
		return
	}
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	fmt.Fprint(os.Stdout, line)
}

// newRequestID returns a unique correlation id for an extension-UI request.
func (s *rpcServer) newRequestID() string {
	n := atomic.AddUint64(&s.idCounter, 1)
	return fmt.Sprintf("rpc-%d", n)
}

// ============================================================================
// Lifecycle: rebind, shutdown, signals
// ============================================================================

// rebind (TS rebindSession) binds extensions to the RPC UI context + handlers
// and (re)subscribes to agent events. Called on startup and after every session
// replacement.
func (s *rpcServer) rebind(session *core.AgentSession) error {
	s.session = session
	if err := session.BindExtensions(core.ExtensionBindings{
		UIContext:       s.buildUIContext(),
		ShutdownHandler: func() { atomic.StoreInt32(&s.shutdownFlag, 1) },
		OnError: func(e extensions.ExtensionError) {
			s.output(map[string]any{
				"type":          "extension_error",
				"extensionPath": e.ExtensionPath,
				"event":         e.Event,
				"error":         e.Error,
			})
		},
		// CommandContextActions intentionally omitted (see file header).
	}); err != nil {
		return err
	}
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	s.unsubscribe = session.Subscribe(func(event core.AgentSessionEvent) {
		s.output(event)
	})
	return nil
}

// shutdown disposes the runtime and exits. Idempotent via an atomic CAS.
func (s *rpcServer) shutdown(exitCode int) {
	if !atomic.CompareAndSwapInt32(&s.shuttingDown, 0, 1) {
		os.Exit(exitCode) // already shutting down
	}
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	_ = s.runtimeHost.Dispose()
	os.Exit(exitCode)
}

// checkShutdown exits if the extension shutdown handler was invoked. Called
// after each command, mirroring the TS checkShutdownRequested.
func (s *rpcServer) checkShutdown() {
	if atomic.LoadInt32(&s.shutdownFlag) != 0 {
		s.shutdown(0)
	}
}

// registerSignalHandlers exits on SIGTERM (143) always and SIGHUP (129) off-Windows.
func (s *rpcServer) registerSignalHandlers() {
	sigCh := make(chan os.Signal, 1)
	wanted := []os.Signal{syscall.SIGTERM}
	if runtime.GOOS != "windows" {
		wanted = append(wanted, syscall.SIGHUP)
	}
	signal.Notify(sigCh, wanted...)
	go func() {
		sig := <-sigCh
		code := 143
		if sig == syscall.SIGHUP {
			code = 129
		}
		s.shutdown(code)
	}()
}

// ============================================================================
// Extension UI context — the 5 supported methods, driven via the RPC protocol
// ============================================================================

// buildUIContext returns the ExtensionUIContext whose dialogs round-trip through
// the RPC protocol (emit a request, await the client's extension_ui_response).
func (s *rpcServer) buildUIContext() extensions.ExtensionUIContext {
	return extensions.ExtensionUIContext{
		// Select returns the INDEX of the chosen option (-1 if cancelled/absent),
		// matching NoOpUIContext's -1 sentinel; the wire response carries a value
		// string, so we resolve it against the option list.
		Select: func(options []string, _ int, timeoutMs int) (int, error) {
			resp, ok := s.dialog(RpcExtensionUIRequest{Method: MethodSelect, Options: options, Timeout: timeoutMs}, timeoutMs)
			return dialogSelectIndex(resp, options, ok), nil
		},
		Confirm: func(message string, timeoutMs int) (bool, error) {
			resp, ok := s.dialog(RpcExtensionUIRequest{Method: MethodConfirm, Message: message, Timeout: timeoutMs}, timeoutMs)
			return dialogConfirmValue(resp, ok), nil
		},
		Input: func(message, placeholder string, timeoutMs int) (string, error) {
			resp, ok := s.dialog(RpcExtensionUIRequest{Method: MethodInput, Message: message, Placeholder: placeholder, Timeout: timeoutMs}, timeoutMs)
			return dialogInputValue(resp, ok), nil
		},
		// Fire-and-forget: no response expected. Map the level to the RPC
		// notify-type so the host can theme the message.
		Notify: func(message, level string) {
			notifyType := NotifyInfo
			switch level {
			case "warning":
				notifyType = NotifyWarning
			case "error":
				notifyType = NotifyError
			}
			s.output(RpcExtensionUIRequest{Type: ExtUIRequestType, ID: s.newRequestID(), Method: MethodNotify, Message: message, NotifyType: notifyType})
		},
		Status: func(message string) {
			s.output(RpcExtensionUIRequest{Type: ExtUIRequestType, ID: s.newRequestID(), Method: MethodSetStatus, StatusText: message})
		},
	}
}

// dialog emits an extension-UI request and blocks for the matching response
// (or until the timeout elapses). Returns (response, ok); ok is false on timeout.
func (s *rpcServer) dialog(req RpcExtensionUIRequest, timeoutMs int) (RpcExtensionUIResponse, bool) {
	req.Type = ExtUIRequestType
	req.ID = s.newRequestID()

	ch := make(chan RpcExtensionUIResponse, 1)
	s.pendingMu.Lock()
	s.pending[req.ID] = ch
	s.pendingMu.Unlock()

	s.output(req)

	if timeoutMs > 0 {
		select {
		case resp := <-ch:
			return resp, true
		case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
			s.pendingMu.Lock()
			delete(s.pending, req.ID)
			s.pendingMu.Unlock()
			return RpcExtensionUIResponse{}, false
		}
	}
	// No timeout: block until the client responds (or the process shuts down).
	resp := <-ch
	return resp, true
}

// ============================================================================
// stdin handling
// ============================================================================

// handleLine parses one JSON line: an extension_ui_response is routed to its
// waiting dialog; anything else is dispatched as a command.
func (s *rpcServer) handleLine(line string) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &head); err != nil {
		s.output(NewErrorResponse("", "parse", "Failed to parse command: "+err.Error()))
		return
	}

	if head.Type == ExtUIResponseType {
		var resp RpcExtensionUIResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			return
		}
		s.routeUIResponse(resp)
		return
	}

	var cmd RpcCommand
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		s.output(NewErrorResponse("", "parse", "Failed to parse command: "+err.Error()))
		return
	}
	if response := s.handleCommand(cmd); response != nil {
		s.output(*response)
	}
	s.checkShutdown()
}

// routeUIResponse delivers an extension-UI reply to the waiting dialog (if any).
func (s *rpcServer) routeUIResponse(resp RpcExtensionUIResponse) {
	s.pendingMu.Lock()
	ch, ok := s.pending[resp.ID]
	if ok {
		delete(s.pending, resp.ID)
	}
	s.pendingMu.Unlock()
	if ok {
		ch <- resp // buffered(1) → never blocks
	}
}

// ============================================================================
// Command dispatch (TS handleCommand)
// ============================================================================

// handleCommand executes one command and returns its response (nil = no sync
// response, e.g. prompt which replies asynchronously from the preflight hook).
func (s *rpcServer) handleCommand(cmd RpcCommand) *RpcResponse {
	id := cmd.ID

	switch cmd.Type {
	// ----------------------------------------------------------------- Prompting
	case CmdPrompt:
		// Fire-and-forget: emit success from the preflight hook, error only if
		// preflight never succeeded. Capture the session ref at dispatch time.
		sess := s.session
		go func() {
			preflightSucceeded := false
			err := sess.Prompt(cmd.Message, &core.PromptOptions{
				Images:           imagesToAny(cmd.Images),
				StreamingBehavior: cmd.StreamingBehavior,
				Source:            extensions.InputSourceRPC,
				PreflightResult: func(didSucceed bool) {
					if didSucceed {
						preflightSucceeded = true
						s.output(NewSuccessResponse(id, CmdPrompt, nil))
					}
				},
			})
			if err != nil && !preflightSucceeded {
				s.output(NewErrorResponse(id, CmdPrompt, errMsg(err)))
			}
		}()
		return nil

	case CmdSteer:
		if err := s.session.Steer(cmd.Message, imagesToAny(cmd.Images)); err != nil {
			return errResp(id, CmdSteer, errMsg(err))
		}
		return okResp(id, CmdSteer, nil)

	case CmdFollowUp:
		if err := s.session.FollowUp(cmd.Message, imagesToAny(cmd.Images)); err != nil {
			return errResp(id, CmdFollowUp, errMsg(err))
		}
		return okResp(id, CmdFollowUp, nil)

	case CmdAbort:
		s.session.Abort()
		return okResp(id, CmdAbort, nil)

	case CmdNewSession:
		opts := &core.NewSessionRuntimeOptions{}
		if cmd.ParentSession != "" {
			opts.ParentSession = cmd.ParentSession
		}
		result, err := s.runtimeHost.NewSession(opts)
		if err != nil {
			return errResp(id, CmdNewSession, errMsg(err))
		}
		if !result.Cancelled {
			if err := s.rebind(s.session); err != nil {
				return errResp(id, CmdNewSession, errMsg(err))
			}
		}
		return okResp(id, CmdNewSession, result)

	// -------------------------------------------------------------------- State
	case CmdGetState:
		return okResp(id, CmdGetState, s.buildState(s.session))

	// -------------------------------------------------------------------- Model
	case CmdSetModel:
		models, err := s.session.GetModelRegistry().GetAvailable()
		if err != nil {
			return errResp(id, CmdSetModel, errMsg(err))
		}
		var found *llm.Model[llm.Api]
		for _, m := range models {
			if string(m.Provider) == cmd.Provider && m.ID == cmd.ModelID {
				found = m
				break
			}
		}
		if found == nil {
			return errResp(id, CmdSetModel, fmt.Sprintf("Model not found: %s/%s", cmd.Provider, cmd.ModelID))
		}
		if err := s.session.SetModel(*found); err != nil {
			return errResp(id, CmdSetModel, errMsg(err))
		}
		return okResp(id, CmdSetModel, found)

	case CmdCycleModel:
		result, err := s.session.CycleModel("") // "" → "forward" (TS no-arg default)
		if err != nil {
			return errResp(id, CmdCycleModel, errMsg(err))
		}
		if result == nil {
			return okResp(id, CmdCycleModel, nil)
		}
		return okResp(id, CmdCycleModel, result)

	case CmdGetAvailableModels:
		models, err := s.session.GetModelRegistry().GetAvailable()
		if err != nil {
			return errResp(id, CmdGetAvailableModels, errMsg(err))
		}
		return okResp(id, CmdGetAvailableModels, map[string]any{"models": models})

	// ----------------------------------------------------------------- Thinking
	case CmdSetThinkingLevel:
		s.session.SetThinkingLevel(sdk.ThinkingLevel(cmd.Level))
		return okResp(id, CmdSetThinkingLevel, nil)

	case CmdCycleThinkingLevel:
		level := s.session.CycleThinkingLevel()
		if level == nil {
			return okResp(id, CmdCycleThinkingLevel, nil)
		}
		return okResp(id, CmdCycleThinkingLevel, map[string]any{"level": string(*level)})

	// --------------------------------------------------------------- Queue modes
	case CmdSetSteeringMode:
		s.session.SetSteeringMode(cmd.Mode)
		return okResp(id, CmdSetSteeringMode, nil)

	case CmdSetFollowUpMode:
		s.session.SetFollowUpMode(cmd.Mode)
		return okResp(id, CmdSetFollowUpMode, nil)

	// --------------------------------------------------------------- Compaction
	case CmdCompact:
		result, err := s.session.Compact(cmd.CustomInstructions)
		if err != nil {
			return errResp(id, CmdCompact, errMsg(err))
		}
		return okResp(id, CmdCompact, result)

	case CmdSetAutoCompaction:
		s.session.SetAutoCompactionEnabled(cmd.Enabled)
		return okResp(id, CmdSetAutoCompaction, nil)

	// -------------------------------------------------------------------- Retry
	case CmdSetAutoRetry:
		// No SetAutoRetryEnabled on AgentSession yet (see file header).
		return errResp(id, CmdSetAutoRetry, "set_auto_retry is not yet supported in this build")

	case CmdAbortRetry:
		s.session.AbortRetry()
		return okResp(id, CmdAbortRetry, nil)

	// --------------------------------------------------------------------- Bash
	case CmdBash:
		result, err := s.session.ExecuteBash(cmd.Command, nil)
		if err != nil {
			return errResp(id, CmdBash, errMsg(err))
		}
		return okResp(id, CmdBash, result)

	case CmdAbortBash:
		s.session.AbortBash()
		return okResp(id, CmdAbortBash, nil)

	// ------------------------------------------------------------------ Session
	case CmdGetSessionStats:
		return okResp(id, CmdGetSessionStats, s.session.GetSessionStats())

	case CmdExportHTML:
		path, err := s.session.ExportToHtml(cmd.OutputPath)
		if err != nil {
			return errResp(id, CmdExportHTML, errMsg(err))
		}
		return okResp(id, CmdExportHTML, map[string]any{"path": path})

	case CmdSwitchSession:
		result, err := s.runtimeHost.SwitchSession(cmd.SessionPath, nil)
		if err != nil {
			return errResp(id, CmdSwitchSession, errMsg(err))
		}
		if !result.Cancelled {
			if err := s.rebind(s.session); err != nil {
				return errResp(id, CmdSwitchSession, errMsg(err))
			}
		}
		return okResp(id, CmdSwitchSession, result)

	case CmdFork:
		result, err := s.runtimeHost.Fork(cmd.EntryID, nil)
		if err != nil {
			return errResp(id, CmdFork, errMsg(err))
		}
		if !result.Cancelled {
			if err := s.rebind(s.session); err != nil {
				return errResp(id, CmdFork, errMsg(err))
			}
		}
		return okResp(id, CmdFork, map[string]any{"text": result.SelectedText, "cancelled": result.Cancelled})

	case CmdClone:
		leafID := s.session.SessionManager.GetLeafId()
		if leafID == "" {
			return errResp(id, CmdClone, "Cannot clone session: no current entry selected")
		}
		result, err := s.runtimeHost.Fork(leafID, &core.ForkOptions{Position: "at"})
		if err != nil {
			return errResp(id, CmdClone, errMsg(err))
		}
		if !result.Cancelled {
			if err := s.rebind(s.session); err != nil {
				return errResp(id, CmdClone, errMsg(err))
			}
		}
		return okResp(id, CmdClone, map[string]any{"cancelled": result.Cancelled})

	case CmdGetForkMessages:
		// No GetUserMessagesForForking on AgentSession yet (see file header).
		return errResp(id, CmdGetForkMessages, "get_fork_messages is not yet supported in this build")

	case CmdGetLastAssistantText:
		return okResp(id, CmdGetLastAssistantText, map[string]any{"text": lastAssistantText(s.session.Agent.Messages())})

	case CmdSetSessionName:
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			return errResp(id, CmdSetSessionName, "Session name cannot be empty")
		}
		s.session.SetSessionName(name)
		return okResp(id, CmdSetSessionName, nil)

	// ----------------------------------------------------------------- Messages
	case CmdGetMessages:
		return okResp(id, CmdGetMessages, map[string]any{"messages": s.session.Agent.Messages()})

	// ----------------------------------------------------------------- Commands
	case CmdGetCommands:
		return okResp(id, CmdGetCommands, map[string]any{
			"commands": buildCommandsList(s.session.GetExtensionCommands(), s.session.GetPromptTemplates(), s.session.GetSkills()),
		})

	default:
		return errResp("", cmd.Type, "Unknown command: "+cmd.Type)
	}
}

// buildState snapshots the session for get_state.
func (s *rpcServer) buildState(session *core.AgentSession) RpcSessionState {
	model := session.GetModel()
	var modelPtr *llm.Model[llm.Api]
	if model.ID != "" {
		modelPtr = &model
	}
	return RpcSessionState{
		Model:                 modelPtr,
		ThinkingLevel:         llm.ThinkingLevel(session.GetThinkingLevel()),
		IsStreaming:           session.IsStreaming(),
		IsCompacting:          session.IsCompacting(),
		SteeringMode:          string(session.Agent.SteeringMode()),
		FollowUpMode:          string(session.Agent.FollowUpMode()),
		SessionFile:           session.GetSessionFile(),
		SessionID:             session.GetSessionID(),
		SessionName:           session.GetSessionName(),
		AutoCompactionEnabled: session.IsAutoCompactionEnabled(),
		MessageCount:          len(session.Agent.Messages()),
		PendingMessageCount:   0, // deferred — no queue-length getter (see file header)
	}
}

// ============================================================================
// Pure helpers (testable — no runtime/session dependency)
// ============================================================================

// buildCommandsList assembles the slash-command catalog for get_commands from
// the three command sources. Pure: takes the slices, returns wire objects.
func buildCommandsList(extCmds []*extensions.ResolvedCommand, templates []core.PromptTemplate, skills []core.Skill) []RpcSlashCommand {
	commands := make([]RpcSlashCommand, 0, len(extCmds)+len(templates)+len(skills))
	for _, c := range extCmds {
		commands = append(commands, RpcSlashCommand{
			Name: c.InvocationName, Description: c.Description, Source: SourceExtension, SourceInfo: c.SourceInfo,
		})
	}
	for _, t := range templates {
		commands = append(commands, RpcSlashCommand{
			Name: t.Name, Description: t.Description, Source: SourcePrompt, SourceInfo: t.SourceInfo,
		})
	}
	for _, sk := range skills {
		commands = append(commands, RpcSlashCommand{
			Name: "skill:" + sk.Name, Description: sk.Description, Source: SourceSkill, SourceInfo: sk.SourceInfo,
		})
	}
	return commands
}

// lastAssistantText returns the concatenated text of the last assistant message
// (scanning backward), or "" if there is none. Used by get_last_assistant_text.
func lastAssistantText(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		am, ok := messages[i].(llm.AssistantMessage)
		if !ok {
			continue
		}
		return concatTextBlocks(am.Content)
	}
	return ""
}

// concatTextBlocks joins the text of all text content blocks (value or pointer).
func concatTextBlocks(content []llm.ContentBlock) string {
	var sb strings.Builder
	for _, block := range content {
		switch c := block.(type) {
		case *llm.TextContent:
			if c != nil {
				sb.WriteString(c.Text)
			}
		case llm.TextContent:
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// dialogSelectIndex resolves a select response to an option index (-1 if none).
func dialogSelectIndex(resp RpcExtensionUIResponse, options []string, ok bool) int {
	if !ok || resp.Cancelled {
		return -1
	}
	for i, opt := range options {
		if opt == resp.Value {
			return i
		}
	}
	return -1
}

// dialogConfirmValue resolves a confirm response to a bool (false if none/cancelled).
func dialogConfirmValue(resp RpcExtensionUIResponse, ok bool) bool {
	if !ok || resp.Cancelled {
		return false
	}
	if resp.Confirmed != nil {
		return *resp.Confirmed
	}
	return false
}

// dialogInputValue resolves an input response to a string ("" if none/cancelled).
func dialogInputValue(resp RpcExtensionUIResponse, ok bool) string {
	if !ok || resp.Cancelled {
		return ""
	}
	return resp.Value
}

// imagesToAny converts the command's image pointers to the []any the session
// prompt/steer/follow-up APIs expect.
func imagesToAny(images []*llm.ImageContent) []any {
	if len(images) == 0 {
		return nil
	}
	out := make([]any, len(images))
	for i, img := range images {
		out[i] = img
	}
	return out
}

// okResp / errResp are thin pointer-returning wrappers over the rpc_types constructors.
func okResp(id, command string, data any) *RpcResponse {
	r := NewSuccessResponse(id, command, data)
	return &r
}

func errResp(id, command, msg string) *RpcResponse {
	r := NewErrorResponse(id, command, msg)
	return &r
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
