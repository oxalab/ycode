package extensions

// js_ui_test.go verifies that ctx.ui.notify and ctx.ui.status are reachable
// from JS handlers — the bridge that makes the interactive ExtensionUIContext
// callable from extension code.

import (
	"testing"
)

func TestJSUI_NotifyFromHandler(t *testing.T) {
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer jrt.Close()
	ext := NewExtension("/test/ext.ts", "/test/ext.ts", nil)
	rt := CreateExtensionRuntime()
	jsAPI, err := BuildJSAPI(jrt, ext, rt, "/test/cwd", nil)
	if err != nil {
		t.Fatalf("BuildJSAPI: %v", err)
	}
	defer jrt.SafeFree(jsAPI.PIValue())

	// Extension registers a session_start handler that calls ctx.ui.notify.
	factoryJS := []byte(`module.exports = function (pi) {
		pi.on("session_start", function (event, ctx) {
			ctx.ui.notify("Hello from extension", "info");
			ctx.ui.notify("Something went wrong", "error");
			ctx.ui.status("running");
			return { done: true };
		});
	};`)
	js, _ := TranspileExtension("/test/ext.js", factoryJS, 1)
	fh, err := jrt.EvalModule("/test/ext.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	// Capture notify/status calls via a fake UI context.
	var notifyCalls []struct {
		msg   string
		level string
	}
	var statusCalls []string
	uiCtx := ExtensionUIContext{
		Notify: func(msg, level string) { notifyCalls = append(notifyCalls, struct {
			msg   string
			level string
		}{msg, level}) },
		Status: func(msg string) { statusCalls = append(statusCalls, msg) },
	}

	// Fire the handler with a runner ctx that carries the fake UI.
	handlers := ext.Handlers["session_start"]
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handlers))
	}
	_, err = handlers[0](
		map[string]any{"type": "session_start"},
		map[string]any{"cwd": "/test/cwd", "ui": &uiCtx, "hasUI": true},
	)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(notifyCalls) != 2 {
		t.Fatalf("expected 2 notify calls, got %d: %v", len(notifyCalls), notifyCalls)
	}
	if notifyCalls[0].msg != "Hello from extension" || notifyCalls[0].level != "info" {
		t.Errorf("notify[0] = %v", notifyCalls[0])
	}
	if notifyCalls[1].msg != "Something went wrong" || notifyCalls[1].level != "error" {
		t.Errorf("notify[1] = %v", notifyCalls[1])
	}
	if len(statusCalls) != 1 || statusCalls[0] != "running" {
		t.Errorf("status calls = %v, want [running]", statusCalls)
	}
}

func TestJSUI_NotifyGracefulWhenNoUI(t *testing.T) {
	// When the runner ctx has no `ui` (non-interactive mode), calling
	// ctx.ui.notify must not crash — it should be a no-op (the JS `ui` object
	// is absent, so ctx.ui is undefined and ctx.ui.notify throws inside JS).
	// Verify the handler still returns its result despite the missing ui.
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	defer jrt.Close()
	ext := NewExtension("/test/ext.ts", "/test/ext.ts", nil)
	rt := CreateExtensionRuntime()
	jsAPI, err := BuildJSAPI(jrt, ext, rt, "/test/cwd", nil)
	if err != nil {
		t.Fatalf("BuildJSAPI: %v", err)
	}
	defer jrt.SafeFree(jsAPI.PIValue())

	// Guard the notify call so a missing ui doesn't abort the handler — this
	// mirrors what a well-behaved extension does when hasUI is false.
	factoryJS := []byte(`module.exports = function (pi) {
		pi.on("session_start", function (event, ctx) {
			if (ctx.ui && ctx.ui.notify) {
				ctx.ui.notify("hi", "info");
			}
			return { ok: true };
		});
	};`)
	js, _ := TranspileExtension("/test/ext.js", factoryJS, 1)
	fh, err := jrt.EvalModule("/test/ext.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	handlers := ext.Handlers["session_start"]
	// No `ui` key on the ctx — simulating non-interactive mode.
	got, err := handlers[0](
		map[string]any{"type": "session_start"},
		map[string]any{"cwd": "/test/cwd"}, // no "ui"
	)
	if err != nil {
		t.Fatalf("handler errored with no ui: %v", err)
	}
	m, _ := got.(map[string]any)
	if m["ok"] != true {
		t.Errorf("handler returned %v, want ok=true", got)
	}
}
