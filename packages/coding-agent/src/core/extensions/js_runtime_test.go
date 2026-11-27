package extensions

// js_runtime_test.go verifies the qjs runtime wrapper end-to-end:
// eval a CJS module, invoke its factory with a fake `pi`, and call a
// registered JS handler back from Go.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fastschema/qjs"
)

// pinCaptureFunction evals a snippet that pins a JS function which records its
// arguments into globalThis.__captured (as JSON) and returns {ok:true}.
// Returns the pinned handle.
func (j *JSRuntime) pinCaptureFunction(t *testing.T) int64 {
	t.Helper()
	var v *qjs.Value
	if err := j.safeCall(func() error {
		var err error
		v, err = j.ctx.Eval("capture.js", qjs.Code(`(function(){
			globalThis.__captured = null;
			return __piPin(function () {
				globalThis.__captured = JSON.stringify(Array.prototype.slice.call(arguments));
				return { ok: true };
			});
		})()`), qjs.TypeGlobal())
		return err
	}); err != nil {
		t.Fatalf("pin capture fn: %v", err)
	}
	if v == nil {
		t.Fatal("pin returned nil")
	}
	id := int64(v.Int32())
	v.Free()
	return id
}

// capturedArgs reads back globalThis.__captured. Returns nil when the capture
// fn hasn't run.
func (j *JSRuntime) capturedArgs(t *testing.T) []any {
	t.Helper()
	var v *qjs.Value
	if err := j.safeCall(func() error {
		var err error
		v, err = j.ctx.Eval("capture.js", qjs.Code(`globalThis.__captured === null ? "null" : globalThis.__captured`), qjs.TypeGlobal())
		return err
	}); err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if v == nil {
		t.Fatal("read capture returned nil")
	}
	s := v.String()
	v.Free()
	if s == "null" || s == "" {
		return nil
	}
	var out []any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("capture not JSON: %v (%s)", err, s)
	}
	return out
}

func TestJSRuntime_FactoryAndHandler(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	// A CJS module that exports a factory. The factory records the `pi.on`
	// handler so we can call it back. We transpile via esbuild to exercise the
	// real pipeline.
	ts := []byte(`export default function (pi) {
		var captured = null;
		pi.on("session_start", function (event, ctx) {
			return { ok: true, heard: event.type, cwd: ctx.cwd };
		});
	};`)
	js, err := TranspileExtension("/test/ext.ts", ts, 1)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	factoryHandle, err := j.EvalModule("/test/ext.ts", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}

	// Build a fake `pi` object in JS with an `on` that stashes the handler.
	pi, err := j.buildFakePi()
	if err != nil {
		t.Fatalf("buildFakePi: %v", err)
	}
	// NO SafeFree(pi) — InvokeFactory consumes the value's reference (the
	// __pi global stash + reset); freeing it here is a double-free.

	if err := j.InvokeFactory(factoryHandle, pi); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	// The factory should have registered one handler for "session_start".
	// j.callHandlerForTest calls the last-registered handler for a fake event.
	// We invoke the handler through the runtime's pinned-handler mechanism.
	h, err := j.lastRegisteredHandler()
	if err != nil {
		t.Fatalf("no handler registered: %v", err)
	}
	got, err := j.CallHandler(h,
		map[string]any{"type": "session_start"},
		map[string]any{"cwd": "/home"},
	)
	if err != nil {
		t.Fatalf("CallHandler: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("handler returned %T, want map: %v", got, got)
	}
	if m["ok"] != true {
		t.Errorf("handler.ok = %v, want true", m["ok"])
	}
	if m["heard"] != "session_start" {
		t.Errorf("handler.heard = %v, want session_start", m["heard"])
	}
	if m["cwd"] != "/home" {
		t.Errorf("handler.cwd = %v, want /home", m["cwd"])
	}
}

func TestJSRuntime_FactoryThrows(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	// A factory that throws synchronously.
	js := []byte(`module.exports = function (pi) { throw new Error("boom"); };`)
	pi, err := j.buildFakePi()
	if err != nil {
		t.Fatalf("buildFakePi: %v", err)
	}
	// NO SafeFree(pi) — InvokeFactory consumes it, on the throw path too (the
	// __pi reset runs before the error surfaces).

	factoryHandle, err := j.EvalModule("/test/bad.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	err = j.InvokeFactory(factoryHandle, pi)
	if err == nil {
		t.Fatal("InvokeFactory should have errored on a throwing factory")
	}
	// The throw surfaces as a clean error. Whether the runtime is then poisoned
	// is a qjs-version-dependent implementation detail (the panic may or may
	// not fire during result extraction). The hard contract is: no host crash,
	// the error is reported, and IsPoisoned/SafeFree keep subsequent operations
	// safe.
}

func TestJSRuntime_NotAFactory(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	// A module that exports a non-function.
	js := []byte(`module.exports = { not: "a function" };`)
	_, err = j.EvalModule("/test/notfn.js", js)
	if err == nil {
		t.Fatal("EvalModule should reject a non-function export")
	}
}

// A panicking provider (stale runner after Invalidate — CreateContext asserts
// active) must be recovered into the {"cwd"} fallback, not crash the bridge.
func TestRunnerCtxMapRecoversStaleProviderPanic(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	j.SetToolCtxProvider(func() map[string]any {
		panic("This extension ctx is stale after session replacement or reload.")
	})
	if got := j.runnerCtxMap(); got != nil {
		t.Fatalf("runnerCtxMap = %v, want nil (panic recovered)", got)
	}
}

// The command provider shares the stale-panic recovery: a panicking command
// provider degrades to nil (→ the tool provider → the {"cwd"} fallback), not
// a crash in the command dispatch.
func TestCommandCtxMapRecoversStaleProviderPanic(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	j.SetCommandCtxProvider(func() map[string]any {
		panic("This extension ctx is stale after session replacement or reload.")
	})
	if got := j.ctxProviderChain(); got != nil {
		t.Fatalf("ctxProviderChain = %v, want nil (panic recovered, no tool provider)", got)
	}
	// And with a healthy tool provider the chain falls through to it.
	j.SetToolCtxProvider(func() map[string]any { return map[string]any{"cwd": "/fallback"} })
	if got := j.ctxProviderChain(); got == nil || got["cwd"] != "/fallback" {
		t.Fatalf("ctxProviderChain = %v, want the tool provider's map after the command provider panicked", got)
	}
}

// Story 42 (C16): CallToolExecuteWithJSCtx must deliver the flat 5 positional
// args (toolCallId, params, signal, null, ctx) — a nested marshaling bug
// ([]any{toolCallID, params, signal, nil} + ctx) once arrived as ONE array arg.
func TestCallToolExecuteWithJSCtxFlatArgs(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	h := j.pinCaptureFunction(t)
	// jsCtx is CONSUMED by the bridge call below (story-42 ownership) — no
	// deferred free; a pre-consume failure leaks one object until Close.
	jsCtx := j.Context().NewObject()
	setPropertyNoLeak(jsCtx, "cwd", j.Context().NewString("/proj"))

	res, err := j.CallToolExecuteWithJSCtx(h, "call-9", map[string]any{"x": 42}, map[string]any{"aborted": false}, jsCtx)
	if err != nil {
		t.Fatalf("CallToolExecuteWithJSCtx: %v", err)
	}
	if m, ok := res.(map[string]any); !ok || m["ok"] != true {
		t.Fatalf("result = %v, want {ok:true}", res)
	}

	args := j.capturedArgs(t)
	if len(args) != 5 {
		t.Fatalf("execute got %d args, want 5: %v", len(args), args)
	}
	if args[0] != "call-9" {
		t.Errorf("arg0 (toolCallId) = %v, want call-9", args[0])
	}
	params, ok := args[1].(map[string]any)
	if !ok || params["x"] != float64(42) {
		t.Errorf("arg1 (params) = %v, want {x:42}", args[1])
	}
	sig, ok := args[2].(map[string]any)
	if !ok || sig["aborted"] != false {
		t.Errorf("arg2 (signal) = %v, want {aborted:false}", args[2])
	}
	if args[3] != nil {
		t.Errorf("arg3 (onUpdate) = %v, want null", args[3])
	}
	ctxObj, ok := args[4].(map[string]any)
	if !ok || ctxObj["cwd"] != "/proj" {
		t.Errorf("arg4 (ctx) = %v, want the JS object with cwd", args[4])
	}
}

// CallCommandHandlerWithJSCtx delivers (args, ctx) with ctx as the JS object.
func TestCallCommandHandlerWithJSCtxArgs(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	h := j.pinCaptureFunction(t)
	// jsCtx is CONSUMED by the bridge call below — no deferred free.
	jsCtx := j.Context().NewObject()
	setPropertyNoLeak(jsCtx, "cwd", j.Context().NewString("/proj"))

	if err := j.CallCommandHandlerWithJSCtx(h, "do-thing", jsCtx); err != nil {
		t.Fatalf("CallCommandHandlerWithJSCtx: %v", err)
	}
	args := j.capturedArgs(t)
	if len(args) != 2 {
		t.Fatalf("handler got %d args, want 2: %v", len(args), args)
	}
	if args[0] != "do-thing" {
		t.Errorf("arg0 (args) = %v, want do-thing", args[0])
	}
	ctxObj, ok := args[1].(map[string]any)
	if !ok || ctxObj["cwd"] != "/proj" {
		t.Errorf("arg1 (ctx) = %v, want the JS object with cwd", args[1])
	}
}

// CallShortcutHandler delivers (ctx) — the single ExtensionContext argument of
// the reference signature (types.ts:1146); the old bridge passed ({"cwd"}, null).
func TestCallShortcutHandlerSingleCtxArg(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	h := j.pinCaptureFunction(t)
	// jsCtx is CONSUMED by the bridge call below — no deferred free.
	jsCtx := j.Context().NewObject()
	setPropertyNoLeak(jsCtx, "isIdle", j.Context().NewBool(true))

	if err := j.CallShortcutHandler(h, jsCtx); err != nil {
		t.Fatalf("CallShortcutHandler: %v", err)
	}
	args := j.capturedArgs(t)
	if len(args) != 1 {
		t.Fatalf("handler got %d args, want 1 (ctx): %v", len(args), args)
	}
	ctxObj, ok := args[0].(map[string]any)
	if !ok || ctxObj["isIdle"] != true {
		t.Errorf("arg0 (ctx) = %v, want the JS object with isIdle", args[0])
	}
}

// evalLockedSetup runs a global eval under j.mu (test bootstrap helper for the
// action-bridge tests).
func evalJS(t *testing.T, j *JSRuntime, name, code string) {
	t.Helper()
	j.mu.Lock()
	err := j.safeCall(func() error {
		v, err := j.ctx.Eval(name, qjs.Code(code), qjs.TypeGlobal())
		if v != nil {
			v.Free()
		}
		return err
	})
	j.mu.Unlock()
	if err != nil {
		t.Fatalf("eval %s: %v", name, err)
	}
}

func evalString(t *testing.T, j *JSRuntime, code string) string {
	t.Helper()
	var out string
	j.mu.Lock()
	err := j.safeCall(func() error {
		v, err := j.ctx.Eval("read.js", qjs.Code(code), qjs.TypeGlobal())
		if err != nil {
			return err
		}
		if v != nil {
			out = v.String()
			v.Free()
		}
		return nil
	})
	j.mu.Unlock()
	if err != nil {
		t.Fatalf("eval read: %v", err)
	}
	return out
}

// awaitNotPending polls the JS expression until it stops reading "pending"
// (the production dispatcher is a goroutine — it may legitimately complete a
// moment after DispatchPendingActions returns) or the deadline expires.
func awaitNotPending(t *testing.T, j *JSRuntime, code string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := evalString(t, j, code)
		if got != "pending" {
			return got
		}
		if time.Now().After(deadline) {
			j.mu.Lock()
			dbg := j.lastCompleteErr
			j.mu.Unlock()
			t.Fatalf("%s never settled (still %q; lastCompleteErr=%q; dbgComplete=%q)",
				code, got, dbg, evalString(t, j, `String(globalThis.__dbgComplete)`))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// F1: the deferred-action dispatcher resolves the shim's native Promise with
// the executor's result — the TS {cancelled} shape.
func TestActionDispatcherResolvesPromise(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	j.SetActionExecutor(func(name string, payload map[string]any, withSessionID int64) (map[string]any, error) {
		if name != "newSession" {
			return nil, nil
		}
		if parent, _ := payload["opts"].(map[string]any)["parentSession"].(string); parent != "parent-1" {
			return nil, nil // payload didn't thread through
		}
		return map[string]any{"cancelled": false}, nil
	})

	evalJS(t, j, "a1.js", `
		globalThis.__settled = "pending";
		var shim = __piMakeAction("newSession");
		shim({ parentSession: "parent-1" }).then(
			function (v) { globalThis.__settled = JSON.stringify(v); },
			function (e) { globalThis.__settled = "rejected:" + e.message; }
		);
		"started";
	`)
	j.DispatchPendingActions()
	if got := awaitNotPending(t, j, `globalThis.__settled`); got != `{"cancelled":false}` {
		t.Fatalf("settled = %v, want {\"cancelled\":false}", got)
	}
}

// Executor errors reject the promise (TS: errors throw).
func TestActionDispatcherRejectsOnError(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	j.SetActionExecutor(func(name string, payload map[string]any, withSessionID int64) (map[string]any, error) {
		return nil, errors.New("switch vetoed by the host")
	})
	evalJS(t, j, "a2.js", `
		globalThis.__settled = "pending";
		__piMakeAction("switchSession")("/some/path").then(
			function (v) { globalThis.__settled = "resolved:" + JSON.stringify(v); },
			function (e) { globalThis.__settled = "rejected:" + e.message; }
		);
		"started";
	`)
	j.DispatchPendingActions()
	if got := awaitNotPending(t, j, `globalThis.__settled`); got != "rejected:switch vetoed by the host" {
		t.Fatalf("settled = %v, want rejection with the executor error", got)
	}
}

// No executor wired → every action rejects (the silent-no-op TS default is
// handled at the runner layer, not the dispatcher).
func TestActionDispatcherRejectsWithoutExecutor(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	evalJS(t, j, "a3.js", `
		globalThis.__settled = "pending";
		__piMakeAction("waitForIdle")().then(
			function (v) { globalThis.__settled = "resolved"; },
			function (e) { globalThis.__settled = "rejected"; }
		);
		"started";
	`)
	j.DispatchPendingActions()
	if got := awaitNotPending(t, j, `globalThis.__settled`); got != "rejected" {
		t.Fatalf("settled = %v, want rejection without an executor", got)
	}
}

// The eval pumps pending jobs across entries: the handler-side
// `await ctx.newSession()` continuation resumes when the dispatcher settles
// (probe-verified; this test pins it so a qjs upgrade that drops the pump
// surfaces here).
func TestActionDispatcherResumesAwaitContinuation(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	j.SetActionExecutor(func(name string, payload map[string]any, withSessionID int64) (map[string]any, error) {
		return map[string]any{"cancelled": false}, nil
	})
	evalJS(t, j, "a4.js", `
		globalThis.__resumed = false;
		(async function () {
			var r = await __piMakeAction("newSession")();
			globalThis.__resumed = (r && r.cancelled === false);
		})();
		"started";
	`)
	j.DispatchPendingActions()
	// Poll FOR the value: the continuation resumes on a later eval's job
	// pump, so the first read can legitimately see the not-yet-resumed
	// "false" (unlike the settled-marker polls, "false" is a valid
	// intermediate state here).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := evalString(t, j, `String(globalThis.__resumed)`); got == "true" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("continuation resumed = %v, want true (job pump)", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A panicking executor must reject, not kill the dispatcher (recovered into a
// rejection).
func TestActionDispatcherRecoversExecutorPanic(t *testing.T) {
	j, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer j.Close()

	j.SetActionExecutor(func(name string, payload map[string]any, withSessionID int64) (map[string]any, error) {
		panic("This extension ctx is stale after session replacement or reload.")
	})
	evalJS(t, j, "a5.js", `
		globalThis.__settled = "pending";
		__piMakeAction("newSession")().then(
			function (v) { globalThis.__settled = "resolved"; },
			function (e) { globalThis.__settled = "rejected"; }
		);
		"started";
	`)
	j.DispatchPendingActions() // must not panic the test goroutine
	if got := awaitNotPending(t, j, `globalThis.__settled`); got != "rejected" {
		t.Fatalf("settled = %v, want rejection after an executor panic", got)
	}
	// The dispatcher must still work afterwards.
	evalJS(t, j, "a5b.js", `
		globalThis.__settled2 = "pending";
		__piMakeAction("newSession")().then(
			function (v) { globalThis.__settled2 = "resolved"; },
			function (e) { globalThis.__settled2 = "rejected"; }
		);
		"started";
	`)
	j.SetActionExecutor(func(name string, payload map[string]any, withSessionID int64) (map[string]any, error) {
		return map[string]any{"cancelled": false}, nil
	})
	j.DispatchPendingActions()
	if got := awaitNotPending(t, j, `globalThis.__settled2`); got != "resolved" {
		t.Fatalf("after-panic dispatch = %v, want resolved", got)
	}
}
