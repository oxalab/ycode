// Print mode (single-shot): send prompts, output the result, exit. Ports
// reference/coding-agent/src/modes/print-mode.ts.
//
// Used for `ycode -p "prompt"` (text) and `ycode --mode json "prompt"` (JSON
// event stream).
//
// ============================================================================
// Deviations + honest deferrals (documented in-file)
// ============================================================================
//
// output-guard: writeRawStdout/flushRawStdout are not ported to Go. Writes go
// straight to os.Stdout (unbuffered/synchronous), so flush is a no-op.
//
// killTrackedDetachedChildren (utils/shell) is not ported. Signal handling
// disposes the runtime and exits with the TS exit codes (143 for SIGTERM, 129
// for SIGHUP) without the detached-children kill.
//
// CommandContextActions: the TS bindExtensions wires waitForIdle/newSession/
// fork/navigateTree/switchSession/reload. None of these are wired ANYWHERE in
// the Go port today (including the interactive App — grep finds zero
// CommandContextActions construction sites), so deferring here matches the
// current port state rather than introducing unused, untested wiring. Only
// OnError is bound. Port the actions when extensions gain command-context
// support (a shared builder serving both interactive and print modes).
//
// JSON event stream: the TS subscribes to raw agent streaming events and writes
// each as a JSON line. The Go AgentSession.Subscribe delivers AgentSessionEvent
// (queue/compaction/session-info level), which has no json tags — so JSON mode
// emits PascalCase AgentSessionEvent objects, not the TS raw streaming-event
// shape. Text mode (the primary `-p` path) is fully faithful. Full JSON event-
// stream parity awaits a streaming-event bridge + json tags.
//
// Error contract: Prompt/rebind errors return (1, err) for the caller to log
// (the TS catches + logs + returns 1). A stopReason error/aborted is logged here
// and returned as (1, nil) — it is a result, not a thrown exception.

package modes

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

// Print output modes (PrintModeOptions.Mode values).
const (
	PrintOutputText = "text"
	PrintOutputJSON = "json"
)

// PrintModeOptions mirrors the TS PrintModeOptions.
type PrintModeOptions struct {
	// Mode is PrintOutputText (final response only) or PrintOutputJSON (events).
	Mode string
	// Messages are additional prompts sent after InitialMessage.
	Messages []string
	// InitialMessage is the first prompt (may carry @file content).
	InitialMessage string
	// InitialImages attach to the initial message (PromptOptions.Images is []any).
	InitialImages []any
}

// RunPrintMode runs single-shot print mode (TS runPrintMode). It sends the
// initial message and any follow-up messages, emits output per Mode, and returns
// the exit code (1 on a failed/aborted assistant turn or a prompt error).
func RunPrintMode(runtimeHost *core.AgentSessionRuntime, options PrintModeOptions) (int, error) {
	exitCode := 0
	session := runtimeHost.Session()

	var (
		unsubscribe func()
		disposed    bool
	)

	disposeRuntime := func() {
		if disposed {
			return
		}
		disposed = true
		if unsubscribe != nil {
			unsubscribe()
		}
		_ = runtimeHost.Dispose()
	}

	// --- Signal handling: SIGTERM always, SIGHUP off-Windows (TS registerSignalHandlers) ---
	sigCh := make(chan os.Signal, 1)
	wanted := []os.Signal{syscall.SIGTERM}
	if runtime.GOOS != "windows" {
		wanted = append(wanted, syscall.SIGHUP)
	}
	signal.Notify(sigCh, wanted...)
	defer signal.Stop(sigCh)
	go func() {
		sig := <-sigCh
		disposeRuntime()
		if sig == syscall.SIGHUP {
			os.Exit(129)
		}
		os.Exit(143)
	}()

	// --- Rebind: bind extensions + subscribe to events (TS rebindSession) ---
	mode := options.Mode
	rebind := func(sess *core.AgentSession) error {
		if err := sess.BindExtensions(core.ExtensionBindings{
			// CommandContextActions intentionally omitted (see file header).
			OnError: func(e extensions.ExtensionError) {
				fmt.Fprintf(os.Stderr, "Extension error (%s): %s\n", e.ExtensionPath, e.Error)
			},
		}); err != nil {
			return err
		}
		if unsubscribe != nil {
			unsubscribe()
		}
		unsubscribe = sess.Subscribe(func(event core.AgentSessionEvent) {
			if mode != PrintOutputJSON {
				return
			}
			if line, err := SerializeJSONLine(event); err == nil {
				fmt.Fprint(os.Stdout, line)
			}
		})
		session = sess // keep the outer handle in sync (TS rebindSession reassigns session)
		return nil
	}
	runtimeHost.SetRebindSession(rebind)
	defer disposeRuntime()

	// --- JSON mode: emit the session header first ---
	if mode == PrintOutputJSON {
		if header := session.SessionManager.GetHeader(); header != nil {
			if line, err := SerializeJSONLine(header); err == nil {
				fmt.Fprint(os.Stdout, line)
			}
		}
	}

	// --- Bind extensions, then send the prompts ---
	if err := rebind(session); err != nil {
		return 1, err
	}
	if options.InitialMessage != "" {
		if err := session.Prompt(options.InitialMessage, &core.PromptOptions{Images: options.InitialImages}); err != nil {
			return 1, err
		}
	}
	for _, msg := range options.Messages {
		if err := session.Prompt(msg, &core.PromptOptions{}); err != nil {
			return 1, err
		}
	}

	// --- Text mode: emit the last assistant message's text (TS text-mode block) ---
	if mode == PrintOutputText {
		result := extractPrintResult(session.Agent.Messages())
		if result.exitCode != 0 {
			fmt.Fprintln(os.Stderr, result.errMsg)
			exitCode = result.exitCode
		} else if result.text != "" {
			fmt.Fprintln(os.Stdout, result.text)
		}
	}

	return exitCode, nil
}

// printResult captures the text-mode outcome of the final assistant turn.
type printResult struct {
	text     string // concatenated text-block content (success)
	exitCode int    // 1 on error/aborted, else 0
	errMsg   string // stderr message when exitCode != 0
}

// extractPrintResult inspects the LAST message and produces the text-mode output
// (pure, extracted for testability). Mirrors the TS text-mode block: only the
// final message is considered, and only when it is an assistant message. An
// error/aborted stop reason yields exit 1 (with the assistant's error message or
// a "Request <reason>" fallback); otherwise the text content blocks are joined.
func extractPrintResult(messages []llm.Message) printResult {
	if len(messages) == 0 {
		return printResult{}
	}
	am, ok := messages[len(messages)-1].(llm.AssistantMessage)
	if !ok {
		// Last message is not assistant → no output (TS: the if-guard fails silently).
		return printResult{}
	}

	if am.StopReason == llm.StopReasonError || am.StopReason == llm.StopReasonAborted {
		msg := am.ErrorMessage
		if msg == "" {
			msg = "Request " + string(am.StopReason)
		}
		return printResult{exitCode: 1, errMsg: msg}
	}

	var sb strings.Builder
	for _, block := range am.Content {
		// ContentBlock concrete values may be stored as value or pointer; handle both.
		switch c := block.(type) {
		case *llm.TextContent:
			if c != nil {
				sb.WriteString(c.Text)
			}
		case llm.TextContent:
			sb.WriteString(c.Text)
		}
	}
	return printResult{text: sb.String()}
}
