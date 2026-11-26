package ycode

// agent_session_js_actions.go — the F1 deferred-action executor: the Go
// backend behind ctx.waitForIdle/newSession/fork/navigateTree/switchSession/
// reload invoked from JS extensions. Runs on the JSRuntime's dispatcher
// goroutine OUTSIDE j.mu — session switches emit extension events, which
// re-enter the runtime via ordinary j.mu contention; running inside the
// submitting handler's Eval would deadlock on both the mutex and qjs's
// no-re-entrancy rule (see js_runtime.go's action bridge). Results mirror TS
// ExtensionCommandContext (types.ts:333-364): every switch action resolves
// {cancelled}, true only when a session_before_switch/session_before_fork
// handler vetoed; errors reject the promise.

import (
	"fmt"

	"ycode/packages/coding-agent/src/core/extensions"
)

// executeJSAction dispatches one queued ctx action to its session callback.
// s is the session that wired this executor (Reload/NavigateTree keep the
// same session object across rebuilds, so the capture stays valid).
func executeJSAction(s *AgentSession, jrt *extensions.JSRuntime, name string, payload map[string]any, withSessionID int64) (map[string]any, error) {
	arg, _ := payload["arg"].(string)
	opts, _ := payload["opts"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
	}
	withSession := func(rc *extensions.ReplacedSessionContext) error {
		return invokeWithSessionCallback(jrt, withSessionID, rc)
	}

	switch name {
	case "waitForIdle":
		// TS agent.waitForIdle() — resolves when the in-flight run finishes;
		// blocking here parks only the dispatcher (j.mu stays free, so the
		// run's own end-of-run event emissions proceed).
		s.Agent.WaitForIdle()
		return nil, nil

	case "newSession":
		if s.onNewSession == nil {
			return nil, fmt.Errorf("session replacement not available")
		}
		parent, _ := opts["parentSession"].(string)
		return cancelledResult(s.onNewSession(map[string]any{
			"parentSession": parent,
			"withSession":   withSession,
		}))

	case "fork":
		if arg == "" {
			return nil, fmt.Errorf("fork requires an entry id")
		}
		if s.onFork == nil {
			return nil, fmt.Errorf("session replacement not available")
		}
		position, _ := opts["position"].(string)
		return cancelledResult(s.onFork(arg, map[string]any{
			"position":    position,
			"withSession": withSession,
		}))

	case "navigateTree":
		if arg == "" {
			return nil, fmt.Errorf("navigateTree requires a target entry id")
		}
		// Go's NavigateTree takes no options (no summarize/instructions
		// variants — documented divergence from TS types.ts:351-355).
		s.NavigateTree(arg)
		return map[string]any{"cancelled": false}, nil

	case "switchSession":
		if arg == "" {
			return nil, fmt.Errorf("switchSession requires a session path")
		}
		if s.onSwitchSession == nil {
			return nil, fmt.Errorf("session replacement not available")
		}
		return cancelledResult(s.onSwitchSession(arg, map[string]any{"withSession": withSession}))

	case "reload":
		// TS interactive guards reload while streaming/compacting
		// (interactive-mode.ts:4778-4786); extension-invoked reload gets the
		// same guard.
		if s.IsStreaming() || s.IsCompacting() {
			return nil, fmt.Errorf("Wait for the current response to finish before reloading.")
		}
		if err := s.Reload(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unknown ctx action: %s", name)
}

// cancelledResult converts the session callbacks' map[string]bool result
// into the JS-facing {cancelled: bool} shape (TS types.ts:341).
func cancelledResult(res map[string]bool, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	cancelled := false
	if res != nil {
		cancelled = res["cancelled"]
	}
	return map[string]any{"cancelled": cancelled}, nil
}

// invokeWithSessionCallback fires the JS options.withSession callback (pinned
// by the __piMakeAction shim) with the replacement session rendered as a JS
// command context. Runs on the dispatcher goroutine WITHOUT j.mu
// (CallShortcutHandler takes it itself). The pinned fn lives in the runtime
// that submitted the action — the old extension instance's closure receiving
// the NEW session's ctx, exactly TS's semantics (agent-session.ts:3087-3095);
// the submitting runtime stays open through the replacement because nothing
// in core ever closes a JSRuntime.
func invokeWithSessionCallback(jrt *extensions.JSRuntime, withSessionID int64, rc *extensions.ReplacedSessionContext) error {
	if withSessionID == 0 || jrt == nil {
		return nil
	}
	jsCtx, err := extensions.BuildReplacedSessionContext(jrt, rc)
	if err != nil {
		return fmt.Errorf("build replaced-session ctx: %w", err)
	}
	// CallShortcutHandler consumes jsCtx — no SafeFree (story-42 ownership).
	return jrt.CallShortcutHandler(withSessionID, jsCtx)
}
