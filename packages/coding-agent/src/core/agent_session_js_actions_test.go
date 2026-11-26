package ycode

// agent_session_js_actions_test.go — F1: the deferred-action executor
// (executeJSAction), the runtime-injected default command-context actions,
// and the JS rendering seam on ReplacedSessionContext.

import (
	"errors"
	"testing"
	"time"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
)

// newActionsTestSession builds a minimal session (no model needed — the
// action paths under test don't touch the model).
func newActionsTestSession(t *testing.T) *AgentSession {
	t.Helper()
	agent := sdk.NewAgent(sdk.AgentOptions{})
	return &AgentSession{
		Agent:          agent,
		SessionManager: NewSessionManager("/proj", t.TempDir()),
	}
}

// testSessionWithActions builds a minimal session, runs injectSessionCallbacks
// (the production wiring point for the default command actions), and returns
// the session. jrt stays nil — the withSession wrapper no-ops on it.
func testSessionWithActions(t *testing.T) *AgentSession {
	t.Helper()
	s := newActionsTestSession(t)
	r := &AgentSessionRuntime{session: s}
	r.injectSessionCallbacks()
	return s
}

// TestInjectSessionCallbacksBuildsDefaultCommandActions: the runtime-injected
// defaults populate all six (the modes pass no bindings — TS wires these
// per-mode; the runtime is the Go equivalent wiring point).
func TestInjectSessionCallbacksBuildsDefaultCommandActions(t *testing.T) {
	s := testSessionWithActions(t)

	actions := s.extensionCommandContextActions
	if actions == nil {
		t.Fatal("injectSessionCallbacks left extensionCommandContextActions nil")
	}
	for name, fn := range map[string]any{
		"WaitForIdle":   actions.WaitForIdle,
		"NewSession":    actions.NewSession,
		"Fork":          actions.Fork,
		"NavigateTree":  actions.NavigateTree,
		"SwitchSession": actions.SwitchSession,
		"Reload":        actions.Reload,
	} {
		if fn == nil {
			t.Errorf("default action %s is nil", name)
		}
	}
	// waitForIdle on an idle agent returns promptly.
	if err := actions.WaitForIdle(); err != nil {
		t.Errorf("WaitForIdle: %v", err)
	}
	// The callbacks delegate to the runtime replacements (set by the same
	// injectSessionCallbacks pass).
	if actions.NewSession == nil || s.onNewSession == nil {
		t.Fatal("NewSession action or its onNewSession delegate missing")
	}
}

// An explicit ExtensionBindings override wins over the injected defaults (TS
// bindCommandContext(?actions)).
func TestExtensionBindingsOverrideCommandActions(t *testing.T) {
	s := testSessionWithActions(t)
	// BindExtensions emits session_start through the session's runner — give
	// it a live one.
	s.extensionRunner = extensions.NewExtensionRunner(nil, extensions.CreateExtensionRuntime(), "/proj", nil, nil)
	override := &extensions.ExtensionCommandContextActions{
		WaitForIdle: func() error { return nil },
	}
	if err := s.BindExtensions(ExtensionBindings{
		CommandContextActions: override,
	}); err != nil {
		t.Fatalf("BindExtensions: %v", err)
	}
	if s.extensionCommandContextActions != override {
		t.Fatal("explicit CommandContextActions binding did not override the defaults")
	}
	// And a nil binding keeps the defaults.
	s2 := testSessionWithActions(t)
	s2.extensionRunner = extensions.NewExtensionRunner(nil, extensions.CreateExtensionRuntime(), "/proj", nil, nil)
	defaults := s2.extensionCommandContextActions
	if err := s2.BindExtensions(ExtensionBindings{}); err != nil {
		t.Fatalf("BindExtensions(nil actions): %v", err)
	}
	if s2.extensionCommandContextActions != defaults {
		t.Fatal("a nil binding must keep the injected defaults")
	}
}

// F1: executeJSAction threads the payload into the replacement callbacks and
// maps their result to the JS-facing {cancelled} shape.
func TestExecuteJSActionNewSession(t *testing.T) {
	s := testSessionWithActions(t)

	var gotOpts map[string]any
	var gotWithSession func(ctx *extensions.ReplacedSessionContext) error
	s.onNewSession = func(opts map[string]any) (map[string]bool, error) {
		gotOpts = opts
		gotWithSession, _ = opts["withSession"].(func(ctx *extensions.ReplacedSessionContext) error)
		return map[string]bool{"cancelled": false}, nil
	}

	res, err := executeJSAction(s, nil, "newSession", map[string]any{
		"arg":  nil,
		"opts": map[string]any{"parentSession": "parent-9"},
	}, 0)
	if err != nil {
		t.Fatalf("executeJSAction: %v", err)
	}
	if res["cancelled"] != false {
		t.Errorf("cancelled = %v, want false", res["cancelled"])
	}
	if parent, _ := gotOpts["parentSession"].(string); parent != "parent-9" {
		t.Errorf("parentSession = %v, want parent-9", gotOpts["parentSession"])
	}
	if gotWithSession == nil {
		t.Fatal("withSession wrapper not threaded into onNewSession opts")
	}
}

// Guards: empty required args and unknown actions reject with clear errors.
func TestExecuteJSActionArgGuards(t *testing.T) {
	s := testSessionWithActions(t)

	for _, tc := range []struct {
		name, action, arg string
		wantErr           string
	}{
		{"fork-empty", "fork", "", "fork requires an entry id"},
		{"navigateTree-empty", "navigateTree", "", "navigateTree requires a target entry id"},
		{"switchSession-empty", "switchSession", "", "switchSession requires a session path"},
		{"unknown", "teleport", "x", "unknown ctx action: teleport"},
	} {
		_, err := executeJSAction(s, nil, tc.action, map[string]any{"arg": tc.arg, "opts": map[string]any{}}, 0)
		if err == nil || err.Error() != tc.wantErr {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
		}
	}
}

// The reload guard (TS interactive-mode.ts:4778-4786) refuses while
// compacting, with the reference wording.
func TestExecuteJSActionReloadGuard(t *testing.T) {
	s := testSessionWithActions(t)
	s.compactionAbort = func() {} // non-nil → IsCompacting() true

	_, err := executeJSAction(s, nil, "reload", map[string]any{}, 0)
	if err == nil || err.Error() != "Wait for the current response to finish before reloading." {
		t.Fatalf("reload guard err = %v, want the reference wording", err)
	}
	// The injected default action carries the same guard.
	if err := s.extensionCommandContextActions.Reload(); err == nil {
		t.Fatal("default Reload action bypassed the guard")
	}
}

// F1: CreateReplacedSessionContext exposes the replacement session's command
// ctx for JS rendering (TS agent-session.ts:3087-3095).
func TestCreateReplacedSessionContextJS(t *testing.T) {
	s := newActionsTestSession(t)
	s.extensionRunner = extensions.NewExtensionRunner(nil, extensions.CreateExtensionRuntime(), "/proj", nil, nil)

	rc := s.CreateReplacedSessionContext()
	if rc.Ctx == nil {
		t.Fatal("ReplacedSessionContext.Ctx not populated")
	}
	ctxMap := rc.Ctx()
	for _, name := range []string{"waitForIdle", "newSession", "fork", "navigateTree", "switchSession", "reload"} {
		if _, present := ctxMap[name]; !present {
			t.Errorf("replaced ctx missing %q (want the command ctx, not the plain one)", name)
		}
	}
	if rc.SendMessage == nil || rc.SendUserMessage == nil {
		t.Error("send methods missing from the replaced context")
	}
}

// End-to-end withSession: a JS newSession() whose options.withSession is a
// pinned JS function must be invoked with the replacement session rendered as
// a JS command ctx (cwd + the send methods) — TS finishSessionReplacement →
// withSession(createReplacedSessionContext()).
func TestExecuteJSActionInvokesWithSessionCallback(t *testing.T) {
	s := testSessionWithActions(t)
	jrt, err := extensions.NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })

	handle, err := jrt.PinCaptureFunctionForTest()
	if err != nil {
		t.Fatalf("pin withSession fn: %v", err)
	}

	s.onNewSession = func(opts map[string]any) (map[string]bool, error) {
		ws, _ := opts["withSession"].(func(ctx *extensions.ReplacedSessionContext) error)
		if ws == nil {
			return nil, errors.New("withSession wrapper missing")
		}
		// Simulate the replacement session's context (the runtime's
		// CreateReplacedSessionContext would bind the NEW session's runner).
		rc := &extensions.ReplacedSessionContext{
			Ctx:             func() map[string]any { return map[string]any{"cwd": "/replacement", "isIdle": true} },
			SendMessage:     func(msg any) error { return nil },
			SendUserMessage: func(content string, images []any) error { return nil },
		}
		return map[string]bool{"cancelled": false}, ws(rc)
	}

	res, err := executeJSAction(s, jrt, "newSession", map[string]any{
		"arg":  nil,
		"opts": map[string]any{},
	}, handle)
	if err != nil {
		t.Fatalf("executeJSAction: %v", err)
	}
	if res["cancelled"] != false {
		t.Errorf("cancelled = %v, want false", res["cancelled"])
	}

	// The withSession callback received the replacement session as a JS
	// command ctx. Wait for the dispatcher's completion eval (the withSession
	// invocation happens inside it, on the dispatcher goroutine).
	deadline := time.Now().Add(2 * time.Second)
	var args []any
	for {
		args = jrt.CapturedArgsForTest(t)
		if len(args) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("withSession callback never fired")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(args) != 1 {
		t.Fatalf("withSession got %d args, want 1 (the ctx)", len(args))
	}
	rcObj, ok := args[0].(map[string]any)
	if !ok {
		t.Fatalf("withSession arg = %T, want the ctx object", args[0])
	}
	if rcObj["cwd"] != "/replacement" {
		t.Errorf("withSession ctx.cwd = %v, want /replacement", rcObj["cwd"])
	}
	// NOTE: the send-method shims can't be asserted here — the capture
	// JSON.stringify's the arguments and JSON drops function properties.
	// TestBuildReplacedSessionContext (extensions) pins them via IsFunction.
}
