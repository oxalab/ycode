package extensions

// js_context_test.go verifies ScrubRunnerCtx (the JSON-safe ctx converter) and
// the BuildJSContext object rendering (the shape JS handlers/tools receive).

import (
	"context"
	"testing"
	"time"

	"github.com/fastschema/qjs"
)

func TestScrubRunnerCtx_Primitives(t *testing.T) {
	in := map[string]any{
		"cwd":                "/home/user",
		"hasUI":              true,
		"isIdle":             false,
		"hasPendingMessages": true,
		"model":              nil,
	}
	out := ScrubRunnerCtx(in)
	if out["cwd"] != "/home/user" {
		t.Errorf("cwd = %v", out["cwd"])
	}
	if out["hasUI"] != true {
		t.Errorf("hasUI = %v", out["hasUI"])
	}
	if out["isIdle"] != false {
		t.Errorf("isIdle = %v", out["isIdle"])
	}
}

func TestScrubRunnerCtx_DropsNonPrimitives(t *testing.T) {
	// A func and a struct should NOT appear in the output (they don't JSON-marshal).
	in := map[string]any{
		"cwd":   "/x",
		"abort": func() {}, // non-primitive
		"model": struct{ X int }{42}, // struct — may or may not be JSON-safe; ScrubRunnerCtx surfaces it if so
	}
	out := ScrubRunnerCtx(in)
	if _, present := out["abort"]; present {
		t.Error("abort (func) should have been dropped")
	}
	// cwd always preserved
	if out["cwd"] != "/x" {
		t.Errorf("cwd = %v", out["cwd"])
	}
}

func TestScrubRunnerCtx_ResolvesGettersEagerly(t *testing.T) {
	calledCU := false
	calledSP := false
	in := map[string]any{
		"cwd": "/x",
		"getContextUsage": func() any {
			calledCU = true
			return map[string]any{"tokens": 100, "percent": 25}
		},
		"getSystemPrompt": func() string {
			calledSP = true
			return "You are an agent."
		},
	}
	out := ScrubRunnerCtx(in)
	if !calledCU {
		t.Error("getContextUsage was not resolved eagerly")
	}
	if !calledSP {
		t.Error("getSystemPrompt was not resolved eagerly")
	}
	if cu, ok := out["contextUsage"].(map[string]any); !ok || cu["tokens"] != 100 {
		t.Errorf("contextUsage = %v", out["contextUsage"])
	}
	if out["systemPrompt"] != "You are an agent." {
		t.Errorf("systemPrompt = %v", out["systemPrompt"])
	}
}

func TestScrubRunnerCtx_NilSafe(t *testing.T) {
	out := ScrubRunnerCtx(nil)
	if out != nil {
		t.Errorf("ScrubRunnerCtx(nil) = %v, want nil", out)
	}
}

// Story 42 (C16): BuildJSContext renders the runner ctx's signal member as
// {aborted: bool} (a Go context.Context can't cross the bridge — a snapshot,
// not the reference's live AbortSignal) plus the eager getter fields.
func TestBuildJSContextRendersSignalAndGetters(t *testing.T) {
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })

	aborted, cancel := context.WithCancel(context.Background())
	cancel()
	runnerCtx := map[string]any{
		"cwd":       "/proj",
		"hasUI":     true,
		"isIdle":    true,
		"model":     map[string]any{"id": "m1"},
		"signal":    aborted,
		"getContextUsage": func() any { return map[string]any{"tokens": 100, "percent": 25} },
		"getSystemPrompt": func() string { return "You are an agent." },
	}
	jsCtx, err := BuildJSContext(jrt, runnerCtx)
	if err != nil {
		t.Fatalf("BuildJSContext: %v", err)
	}
	t.Cleanup(func() { jrt.safeCall(func() error { jsCtx.Free(); return nil }) })

	if v := jsCtx.GetPropertyStr("cwd"); v == nil || v.String() != "/proj" {
		t.Errorf("cwd = %v, want /proj", v)
	}
	if v := jsCtx.GetPropertyStr("isIdle"); v == nil || !v.Bool() {
		t.Errorf("isIdle = %v, want true", v)
	}
	model := jsCtx.GetPropertyStr("model")
	if model == nil {
		t.Fatal("model missing")
	}
	if id := model.GetPropertyStr("id"); id == nil || id.String() != "m1" {
		t.Errorf("model.id = %v, want m1", id)
	}
	sig := jsCtx.GetPropertyStr("signal")
	if sig == nil {
		t.Fatal("signal missing")
	}
	if v := sig.GetPropertyStr("aborted"); v == nil || !v.Bool() {
		t.Errorf("signal.aborted = %v, want true (cancelled context)", v)
	}
	if v := jsCtx.GetPropertyStr("contextUsage"); v == nil {
		t.Fatal("contextUsage (eager getter) missing")
	}
	if v := jsCtx.GetPropertyStr("systemPrompt"); v == nil || v.String() != "You are an agent." {
		t.Errorf("systemPrompt = %v, want the getter's result", v)
	}
	// Ownership note: the object was NOT handed to a bridge method here, so the
	// test frees it itself (raw Free under safeCall).
}

// F1: the event-path ctx renders abort/compact/shutdown as callable
// fire-and-forget shims, and the command-only actions stay absent (TS
// ExtensionContext vs ExtensionCommandContext, types.ts:298-327 vs 333-364).
func TestBuildJSContextRendersActions(t *testing.T) {
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })

	aborted := make(chan struct{})
	compacted := make(chan struct{})
	runnerCtx := map[string]any{
		"cwd":      "/proj",
		"abort":    func() { close(aborted) },
		"compact":  func(opts *CompactOptions) error { close(compacted); return nil },
		"shutdown": func() {},
	}
	jsCtx, err := BuildJSContext(jrt, runnerCtx)
	if err != nil {
		t.Fatalf("BuildJSContext: %v", err)
	}

	// TS parity: the command-only actions are ABSENT from the event ctx —
	// check BEFORE stashing (a global stash consumes the object's reference;
	// after a reset the wrapper is dead and must not be touched — the
	// story-42 ownership contract, probe-verified). Missing properties read
	// back as the string "undefined" through this binding.
	if v := jsCtx.GetPropertyStr("newSession"); v == nil || v.String() != "undefined" {
		t.Errorf("newSession = %v, want undefined on the event ctx", v)
	}
	if v := jsCtx.GetPropertyStr("waitForIdle"); v == nil || v.String() != "undefined" {
		t.Errorf("waitForIdle = %v, want undefined on the event ctx", v)
	}
	abort := jsCtx.GetPropertyStr("abort")
	if abort == nil || !abort.IsFunction() {
		t.Fatalf("abort = %v, want a function", abort)
	}

	// Invoking it must reach the Go func (fire-and-forget goroutine). Stash on
	// a global and LEAVE it there (consumes the ref; the property owns the
	// object until the runtime closes — never reset, never Free).
	jrt.mu.Lock()
	evalErr := jrt.safeCall(func() error {
		g := jrt.ctx.Global()
		g.SetPropertyStr("__piTestCtx", jsCtx)
		v, err := jrt.ctx.Eval("abortcall.js", qjs.Code(`globalThis.__piTestCtx.abort(); "ok";`), qjs.TypeGlobal())
		if v != nil {
			v.Free()
		}
		return err
	})
	jrt.mu.Unlock()
	if evalErr != nil {
		t.Fatalf("invoke abort: %v", evalErr)
	}
	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx.abort() never reached the Go func")
	}
	select {
	case <-compacted:
		t.Fatal("compact must not fire uninvoked")
	case <-time.After(50 * time.Millisecond):
	}
}

// F1: the command-path map (CreateCommandContext shape) renders all six
// session-control actions as callable shims alongside the base actions.
func TestBuildJSContextRendersCommandActions(t *testing.T) {
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })

	runnerCtx := map[string]any{
		"cwd":           "/proj",
		"abort":         func() {},
		"waitForIdle":   func() error { return nil },
		"newSession":    func(opts map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		"fork":          func(entryID string, opts map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		"navigateTree":  func(targetID string, opts map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		"switchSession": func(sessionPath string, opts map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		"reload":        func() error { return nil },
	}
	jsCtx, err := BuildJSContext(jrt, runnerCtx)
	if err != nil {
		t.Fatalf("BuildJSContext: %v", err)
	}
	t.Cleanup(func() { jrt.safeCall(func() error { jsCtx.Free(); return nil }) })

	for _, name := range []string{"waitForIdle", "newSession", "fork", "navigateTree", "switchSession", "reload"} {
		v := jsCtx.GetPropertyStr(name)
		if v == nil || !v.IsFunction() {
			t.Errorf("ctx.%s = %v, want a callable shim", name, v)
		}
	}
	if v := jsCtx.GetPropertyStr("abort"); v == nil || !v.IsFunction() {
		t.Errorf("ctx.abort = %v, want a function (base ctx action)", v)
	}
}

// F1: BuildReplacedSessionContext renders the replacement session's ctx plus
// the two send methods; a stale Ctx (panicking runner) degrades to a bare
// object rather than crashing the withSession dispatch.
func TestBuildReplacedSessionContext(t *testing.T) {
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })

	var sent string
	sentUser := ""
	rc := &ReplacedSessionContext{
		Ctx:             func() map[string]any { return map[string]any{"cwd": "/new-session", "isIdle": true} },
		SendMessage:     func(msg any) error { sent, _ = msg.(string); return nil },
		SendUserMessage: func(content string, images []any) error { sentUser = content; return nil },
	}
	jsCtx, err := BuildReplacedSessionContext(jrt, rc)
	if err != nil {
		t.Fatalf("BuildReplacedSessionContext: %v", err)
	}
	// Stash on a global (consumes the ref) and drive it from JS.
	jrt.mu.Lock()
	evalErr := jrt.safeCall(func() error {
		g := jrt.ctx.Global()
		g.SetPropertyStr("__piRC", jsCtx)
		v, err := jrt.ctx.Eval("rc.js", qjs.Code(`
			globalThis.__rcCwd = globalThis.__piRC.cwd;
			globalThis.__rcIdle = globalThis.__piRC.isIdle;
			globalThis.__rcSend = typeof globalThis.__piRC.sendMessage;
			globalThis.__piRC.sendMessage("hello");
			globalThis.__piRC.sendUserMessage("hi there");
			"ok";
		`), qjs.TypeGlobal())
		if v != nil {
			v.Free()
		}
		g.SetPropertyStr("__piRC", jrt.ctx.NewUndefined())
		return err
	})
	jrt.mu.Unlock()
	if evalErr != nil {
		t.Fatalf("drive rc ctx: %v", evalErr)
	}
	if got := evalString(t, jrt, `globalThis.__rcCwd`); got != "/new-session" {
		t.Errorf("rc cwd = %v, want /new-session", got)
	}
	if got := evalString(t, jrt, `String(globalThis.__rcIdle)`); got != "true" {
		t.Errorf("rc isIdle = %v, want true", got)
	}
	if got := evalString(t, jrt, `globalThis.__rcSend`); got != "function" {
		t.Errorf("sendMessage typeof = %v, want function", got)
	}
	// The send shims deliver on their own goroutine — poll, don't race.
	deliveryDeadline := time.Now().Add(2 * time.Second)
	for (sent != "hello" || sentUser != "hi there") && time.Now().Before(deliveryDeadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sent != "hello" {
		t.Errorf("sendMessage delivered %v, want hello", sent)
	}
	if sentUser != "hi there" {
		t.Errorf("sendUserMessage delivered %v, want 'hi there'", sentUser)
	}

	// A stale (panicking) replacement runner degrades to a bare object — the
	// withSession dispatch must not crash the host.
	stale := &ReplacedSessionContext{
		Ctx: func() map[string]any { panic("This extension ctx is stale after session replacement or reload.") },
	}
	bare, err := BuildReplacedSessionContext(jrt, stale)
	if err != nil {
		t.Fatalf("BuildReplacedSessionContext(stale): %v", err)
	}
	if v := bare.GetPropertyStr("cwd"); v == nil || v.String() != "undefined" {
		t.Errorf("stale rc cwd = %v, want undefined (bare object)", v)
	}
	if v := bare.GetPropertyStr("sendMessage"); v == nil || !v.IsFunction() {
		t.Errorf("stale rc sendMessage = %v, want a function (send methods still render)", v)
	}
	jrt.safeCall(func() error { bare.Free(); return nil })

	// A nil rc is a bare object, not a crash.
	nilRC, err := BuildReplacedSessionContext(jrt, nil)
	if err != nil {
		t.Fatalf("BuildReplacedSessionContext(nil): %v", err)
	}
	jrt.safeCall(func() error { nilRC.Free(); return nil })
}
