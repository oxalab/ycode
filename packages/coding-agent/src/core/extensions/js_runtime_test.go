package extensions

// js_runtime_test.go verifies the qjs runtime wrapper end-to-end:
// eval a CJS module, invoke its factory with a fake `pi`, and call a
// registered JS handler back from Go.

import (
	"encoding/json"
	"testing"

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
	defer j.SafeFree(pi) // SafeFree, not pi.Free — survives a poisoned runtime

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
	defer j.SafeFree(pi)

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
	// safe. Verify SafeFree doesn't panic regardless of poison state.
	j.SafeFree(pi) // immediate; the defer is belt-and-suspenders
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
	jsCtx.SetPropertyStr("cwd", j.Context().NewString("/proj"))

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
	jsCtx.SetPropertyStr("cwd", j.Context().NewString("/proj"))

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
	jsCtx.SetPropertyStr("isIdle", j.Context().NewBool(true))

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
