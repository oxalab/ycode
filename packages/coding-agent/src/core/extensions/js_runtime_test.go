package extensions

// js_runtime_test.go verifies the qjs runtime wrapper end-to-end:
// eval a CJS module, invoke its factory with a fake `pi`, and call a
// registered JS handler back from Go.

import (
	"testing"
)

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
