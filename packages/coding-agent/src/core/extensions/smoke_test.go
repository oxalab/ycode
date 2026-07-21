package extensions

// smoke_test.go is the end-to-end proof that the JS extension loader works
// against a realistic pi-style extension. It writes a self-contained TS
// extension (no external imports — the parts of pi that need imports are
// either type-only and erased by esbuild, or replaced with inline equivalents)
// and exercises the full path: LoadExtensions → factory runs → tool registered
// → tool execute called from Go → session_start handler fires.

import (
	"testing"
)

func TestSmoke_RealisticPiStyleExtension(t *testing.T) {
	// This extension mirrors the shape of reference/examples/extensions/hello.ts
	// + dynamic-tools.ts but is self-contained: it registers a tool at factory
	// time, registers another inside a session_start handler, and registers a
	// command. No external imports (TypeBox/defineTool) — the parameters schema
	// is a plain object literal, which is what the JS bridge sees after esbuild
	// strips the type-only imports anyway.
	src := `export default function (pi) {
		// Tool registered at factory time.
		pi.registerTool({
			name: "hello",
			label: "Hello",
			description: "A simple greeting tool",
			promptSnippet: "Greet someone",
			parameters: { "type": "object", "properties": { "name": { "type": "string", "description": "Name to greet" } }, "required": ["name"] },
			execute: function (toolCallId, params) {
				return {
					content: [{ type: "text", text: "Hello, " + params.name + "!" }],
					details: { greeted: params.name }
				};
			}
		});

		// Handler that fires on session_start.
		pi.on("session_start", function (event, ctx) {
			return { type: event.type, cwd: ctx.cwd };
		});

		// A slash command.
		pi.registerCommand("greet", {
			description: "Greet by command",
			handler: function (args, ctx) { return; }
		});
	};`
	p := writeExtFile(t, "hello.ts", src)

	result := LoadExtensions([]string{p}, "/home/user/project", nil)

	// Must load cleanly.
	if len(result.Errors) != 0 {
		t.Fatalf("load errors: %v", result.Errors)
	}
	if len(result.Extensions) != 1 {
		t.Fatalf("expected 1 extension, got %d", len(result.Extensions))
	}
	ext := result.Extensions[0]

	// 1. The factory-time tool must be registered.
	hello, ok := ext.Tools["hello"]
	if !ok {
		t.Fatal("hello tool not registered")
	}
	if hello.Definition.Description != "A simple greeting tool" {
		t.Errorf("description = %q", hello.Definition.Description)
	}
	if hello.Definition.PromptSnippet != "Greet someone" {
		t.Errorf("snippet = %q", hello.Definition.PromptSnippet)
	}

	// 2. Calling the tool's execute from Go must round-trip through JS.
	got, err := hello.Definition.Execute(nil, []byte(`{"name":"World"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("Execute returned %T: %v", got, got)
	}
	content, _ := m["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d", len(content))
	}
	block, _ := content[0].(map[string]any)
	if block["text"] != "Hello, World!" {
		t.Errorf("text = %v, want 'Hello, World!'", block["text"])
	}

	// 3. The session_start handler must be registered and callable.
	handlers := ext.Handlers["session_start"]
	if len(handlers) != 1 {
		t.Fatalf("expected 1 session_start handler, got %d", len(handlers))
	}
	res, err := handlers[0](
		map[string]any{"type": "session_start"},
		map[string]any{"cwd": "/home/user/project"},
	)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	resMap, _ := res.(map[string]any)
	if resMap["type"] != "session_start" || resMap["cwd"] != "/home/user/project" {
		t.Errorf("handler returned %v", res)
	}

	// 4. The command must be registered.
	if ext.Commands["greet"] == nil {
		t.Error("greet command not registered")
	}

	// 5. The JSRuntime must be anchored for lifetime.
	if result.JSRuntime == nil {
		t.Error("JSRuntime should be anchored on the result")
	}
}
