package extensions

// js_api_test.go verifies the JS-facing `pi` object: that a factory calling
// pi.registerTool / pi.on / pi.registerCommand / pi.registerFlag actually
// populates the *Extension maps the runner consumes.

import (
	"testing"
)

// newAPIFixture creates a JSRuntime + Extension + runtime + a built `pi` object,
// returning the JSAPI and the Extension it populates. Cleans up the runtime on
// test completion.
func newAPIFixture(t *testing.T) (jsAPI *jsExtensionAPI, ext *Extension, jrt *JSRuntime) {
	t.Helper()
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })
	ext = NewExtension("/test/ext.ts", "/test/ext.ts", nil)
	rt := CreateExtensionRuntime()
	jsAPI, err = BuildJSAPI(jrt, ext, rt, "/test/cwd", nil)
	if err != nil {
		t.Fatalf("BuildJSAPI: %v", err)
	}
	t.Cleanup(func() { jrt.SafeFree(jsAPI.PIValue()) })
	return jsAPI, ext, jrt
}

func TestJSAPI_RegisterTool(t *testing.T) {
	jsAPI, ext, _ := newAPIFixture(t)

	factoryJS := []byte(`module.exports = function (pi) {
		pi.registerTool({
			name: "myTool",
			description: "A test tool",
			promptSnippet: "Test tool",
			parameters: { "type": "object", "properties": { "x": { "type": "number" } } },
			execute: function (toolCallId, params) {
				return { content: [{ type: "text", text: "got " + params.x }] };
			}
		});
	};`)

	// Transpile not needed (pure JS), but go through the pipeline to confirm.
	js, err := TranspileExtension("/test/ext.js", factoryJS, 1)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	fh, err := jsAPI.jrt.EvalModule("/test/ext.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jsAPI.jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	// The tool should be registered on the extension.
	rt, ok := ext.Tools["myTool"]
	if !ok {
		t.Fatalf("myTool not registered; tools = %v", ext.Tools)
	}
	if rt.Definition.Description != "A test tool" {
		t.Errorf("description = %q, want 'A test tool'", rt.Definition.Description)
	}
	if rt.Definition.PromptSnippet != "Test tool" {
		t.Errorf("snippet = %q, want 'Test tool'", rt.Definition.PromptSnippet)
	}
	if rt.Definition.Execute == nil {
		t.Fatal("execute fn is nil — should be the JS bridge")
	}
	// Verify the parameters JSON schema round-tripped.
	if len(rt.Definition.Parameters) == 0 {
		t.Error("parameters schema is empty")
	}

	// Call the execute function and verify the result.
	result, err := rt.Definition.Execute(nil, []byte(`{"x":42}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Execute returned %T, want map: %v", result, result)
	}
	content, ok := m["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content wrong: %v", m["content"])
	}
	textBlock, _ := content[0].(map[string]any)
	if textBlock["text"] != "got 42" {
		t.Errorf("text = %v, want 'got 42'", textBlock["text"])
	}
}

func TestJSAPI_On(t *testing.T) {
	jsAPI, ext, _ := newAPIFixture(t)

	factoryJS := []byte(`module.exports = function (pi) {
		pi.on("session_start", function (event, ctx) {
			return { heard: event.type, cwd: ctx.cwd };
		});
	};`)
	js, _ := TranspileExtension("/test/ext.js", factoryJS, 1)
	fh, err := jsAPI.jrt.EvalModule("/test/ext.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jsAPI.jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	handlers := ext.Handlers["session_start"]
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handlers))
	}
	got, err := handlers[0](
		map[string]any{"type": "session_start"},
		map[string]any{"cwd": "/test/cwd"},
	)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	m, _ := got.(map[string]any)
	if m["heard"] != "session_start" || m["cwd"] != "/test/cwd" {
		t.Errorf("handler returned %v", got)
	}
}

func TestJSAPI_RegisterCommand(t *testing.T) {
	jsAPI, ext, _ := newAPIFixture(t)

	factoryJS := []byte(`module.exports = function (pi) {
		pi.registerCommand("greet", {
			description: "Greet someone",
			handler: function (args, ctx) {
				return;
			}
		});
	};`)
	js, _ := TranspileExtension("/test/ext.js", factoryJS, 1)
	fh, err := jsAPI.jrt.EvalModule("/test/ext.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jsAPI.jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	cmd, ok := ext.Commands["greet"]
	if !ok {
		t.Fatalf("greet not registered; commands = %v", ext.Commands)
	}
	if cmd.Description != "Greet someone" {
		t.Errorf("description = %q", cmd.Description)
	}
	if cmd.Handler == nil {
		t.Error("handler is nil")
	}
}

func TestJSAPI_RegisterFlag(t *testing.T) {
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })
	// BuildJSAPI uses a fresh CreateExtensionRuntime; flag defaults land there.
	rt := CreateExtensionRuntime()
	ext2 := NewExtension("/test/ext2.ts", "/test/ext2.ts", nil)
	jsAPI2, err := BuildJSAPI(jrt, ext2, rt, "/test/cwd", nil)
	if err != nil {
		t.Fatalf("BuildJSAPI: %v", err)
	}
	t.Cleanup(func() { jrt.SafeFree(jsAPI2.PIValue()) })

	factoryJS := []byte(`module.exports = function (pi) {
		pi.registerFlag("verbose", { type: "boolean", default: false, description: "Verbose output" });
	};`)
	js, _ := TranspileExtension("/test/ext2.js", factoryJS, 1)
	fh, err := jsAPI2.jrt.EvalModule("/test/ext2.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jsAPI2.jrt.InvokeFactory(fh, jsAPI2.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	f, ok := ext2.Flags["verbose"]
	if !ok {
		t.Fatalf("verbose flag not registered")
	}
	if f.Description != "Verbose output" {
		t.Errorf("description = %q", f.Description)
	}
	// Flag default should be seeded into runtime.FlagValues.
	if v, ok := rt.FlagValues["verbose"]; !ok || v != false {
		t.Errorf("FlagValues[verbose] = %v, want false", v)
	}
}
