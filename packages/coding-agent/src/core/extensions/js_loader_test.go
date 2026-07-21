package extensions

// js_loader_test.go verifies the end-to-end loader path: write a real .ts
// file, call LoadExtensions (the public entry the resource loader uses), and
// confirm the resulting *Extension has the tool/handler/command the factory
// registered. This is the test that proves the loader wiring is correct —
// if it passes, the runner picks the extension up automatically.

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExtFile writes content to a temp file named filename under t.TempDir()
// and returns its absolute path.
func writeExtFile(t *testing.T, filename, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	return p
}

func TestLoadExtensions_TSExtension(t *testing.T) {
	ts := `export default function (pi) {
		pi.on("session_start", function (event, ctx) {
			return { ok: true };
		});
		pi.registerTool({
			name: "greet",
			description: "Greet",
			execute: function (id, params) {
				return { content: [{ type: "text", text: "hi " + params.name }] };
			}
		});
		pi.registerCommand("hello", {
			description: "Say hello",
			handler: function (args, ctx) { return; }
		});
	};`
	p := writeExtFile(t, "ext.ts", ts)

	result := LoadExtensions([]string{p, "/nonexistent/for/json-fallback.json"}, "/test/cwd", nil)

	// The TS extension should load; the missing JSON path should error.
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error (the missing JSON path), got %d: %v", len(result.Errors), result.Errors)
	}
	if len(result.Extensions) != 1 {
		t.Fatalf("expected 1 loaded extension, got %d", len(result.Extensions))
	}
	ext := result.Extensions[0]
	if ext.Tools["greet"] == nil {
		t.Error("greet tool not registered")
	}
	if len(ext.Handlers["session_start"]) != 1 {
		t.Error("session_start handler not registered")
	}
	if ext.Commands["hello"] == nil {
		t.Error("hello command not registered")
	}
	if result.JSRuntime == nil {
		t.Error("JSRuntime should be anchored on the result for JS-loaded extensions")
	}
}

func TestLoadExtensions_JSRuntimeAnchoredForJSONOnly(t *testing.T) {
	// A pure-JSON batch (no JS files) should NOT create a JSRuntime.
	// Use minimal JSON with no commands (which carry func fields that can't
	// unmarshal) to keep the parse clean.
	jsonContent := `{"tools":[]}`
	p := writeExtFile(t, "ext.json", jsonContent)

	result := LoadExtensions([]string{p}, "/test/cwd", nil)
	if result.JSRuntime != nil {
		t.Errorf("JSRuntime should be nil for a pure-JSON batch; got %T", result.JSRuntime)
	}
}

func TestLoadExtensions_JSMalformedSurfacesError(t *testing.T) {
	// A TS file with a syntax error should produce an ExtensionLoadError,
	// not crash the process.
	bad := `export default function ( { ;`
	p := writeExtFile(t, "bad.ts", bad)

	result := LoadExtensions([]string{p}, "/test/cwd", nil)
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error for malformed TS, got %d", len(result.Errors))
	}
	if len(result.Extensions) != 0 {
		t.Errorf("expected 0 extensions, got %d", len(result.Extensions))
	}
}

func TestLoadExtensions_JSFactoryThrowsSurfacesError(t *testing.T) {
	// A factory that throws should produce an ExtensionLoadError.
	throws := `export default function (pi) { throw new Error("boom from factory"); };`
	p := writeExtFile(t, "throws.ts", throws)

	result := LoadExtensions([]string{p}, "/test/cwd", nil)
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error for throwing factory, got %d: %v", len(result.Errors), result.Errors)
	}
	// Other extensions should still load after a throw — verify by loading a
	// good JSON extension alongside.
	good := writeExtFile(t, "good.json", `{"tools":[],"commands":[]}`)
	result2 := LoadExtensions([]string{p, good}, "/test/cwd", nil)
	if len(result2.Errors) != 1 {
		t.Errorf("expected 1 error in mixed batch, got %d", len(result2.Errors))
	}
	if len(result2.Extensions) != 1 {
		t.Errorf("expected 1 good extension in mixed batch, got %d", len(result2.Extensions))
	}
}
