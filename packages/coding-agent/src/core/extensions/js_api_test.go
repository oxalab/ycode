package extensions

// js_api_test.go verifies the JS-facing `pi` object: that a factory calling
// pi.registerTool / pi.on / pi.registerCommand / pi.registerFlag actually
// populates the *Extension maps the runner consumes.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func jsonUnmarshalText(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

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
	// NO SafeFree(jsAPI.PIValue()): InvokeFactory consumes the pi value's
	// reference (the __pi global stash + reset) — callers never free it.
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

// echoCtxTool registers a tool whose execute JSON-echoes its own arguments,
// so tests can pin the exact bridge shape (story 42 / C16).
func echoCtxTool(t *testing.T, jsAPI *jsExtensionAPI, ext *Extension) {
	t.Helper()
	factoryJS := []byte(`module.exports = function (pi) {
		pi.registerTool({
			name: "echoCtx",
			execute: function (toolCallId, params, signal, onUpdate, ctx) {
				return { content: [{ type: "text", text: JSON.stringify({
					id: toolCallId,
					params: params,
					aborted: signal ? signal.aborted : null,
					ctx: ctx
				}) }] };
			}
		});
	};`)
	js, err := TranspileExtension("/test/echo.js", factoryJS, 1)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	fh, err := jsAPI.jrt.EvalModule("/test/echo.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jsAPI.jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}
	if ext.Tools["echoCtx"] == nil {
		t.Fatal("echoCtx not registered")
	}
}

// With a ctx provider wired (the buildRuntime seam), execute must receive the
// rich runner ctx (cwd/model/isIdle), the REAL toolCallId, and the signal
// snapshot — not the old ("", params, null, null, {cwd}).
func TestJSAPI_ExecuteReceivesRunnerCtxAndToolCallID(t *testing.T) {
	jsAPI, ext, jrt := newAPIFixture(t)

	jrt.SetToolCtxProvider(func() map[string]any {
		return map[string]any{
			"cwd":    "/real/cwd",
			"model":  map[string]any{"id": "glm-x"},
			"isIdle": true,
		}
	})
	echoCtxTool(t, jsAPI, ext)

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt := ext.Tools["echoCtx"]
	result, err := rt.Definition.Execute(WithToolCallID(cctx, "call-7"), []byte(`{"k":"v"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m := result.(map[string]any)
	seen := m["content"].([]any)[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := jsonUnmarshalText(seen, &payload); err != nil {
		t.Fatalf("echo not JSON: %v (%s)", err, seen)
	}
	if payload["id"] != "call-7" {
		t.Errorf("toolCallId = %v, want call-7 (threaded via WithToolCallID)", payload["id"])
	}
	if payload["aborted"] != true {
		t.Errorf("signal.aborted = %v, want true (cancelled ctx snapshot; the echo reads signal.aborted)", payload["aborted"])
	}
	ctxObj, ok := payload["ctx"].(map[string]any)
	if !ok {
		t.Fatalf("ctx = %v, want object", payload["ctx"])
	}
	if ctxObj["cwd"] != "/real/cwd" {
		t.Errorf("ctx.cwd = %v, want /real/cwd (provider)", ctxObj["cwd"])
	}
	model, ok := ctxObj["model"].(map[string]any)
	if !ok || model["id"] != "glm-x" {
		t.Errorf("ctx.model = %v, want {id:glm-x}", ctxObj["model"])
	}
	if ctxObj["isIdle"] != true {
		t.Errorf("ctx.isIdle = %v, want true", ctxObj["isIdle"])
	}
}

// Without a provider (pre-bind / direct test calls), execute still gets a
// usable {"cwd"} ctx — the no-op-default shape TS's runner uses.
func TestJSAPI_ExecuteFallbackCtxWithoutProvider(t *testing.T) {
	jsAPI, ext, _ := newAPIFixture(t)
	echoCtxTool(t, jsAPI, ext)

	rt := ext.Tools["echoCtx"]
	result, err := rt.Definition.Execute(nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m := result.(map[string]any)
	seen := m["content"].([]any)[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := jsonUnmarshalText(seen, &payload); err != nil {
		t.Fatalf("echo not JSON: %v (%s)", err, seen)
	}
	ctxObj, ok := payload["ctx"].(map[string]any)
	if !ok {
		t.Fatalf("ctx = %v, want object", payload["ctx"])
	}
	if _, ok := ctxObj["cwd"]; !ok {
		t.Errorf("ctx = %v, want at least cwd (fallback)", ctxObj)
	}
	if ctxObj["model"] != nil {
		t.Errorf("ctx.model = %v, want absent (no provider)", ctxObj["model"])
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
	// NO SafeFree(jsAPI2.PIValue()) — InvokeFactory consumes it (see
	// newAPIFixture).

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

// F1 (TS interface split): command handlers receive ExtensionCommandContext —
// the six session-control actions PLUS the base actions — while event
// handlers receive the plain ExtensionContext without the six
// (types.ts:1066 vs 1079). The command bridge resolves the command provider;
// the event bridge the tool provider.
func TestJSAPI_CommandVsEventCtxSurface(t *testing.T) {
	jsAPI, ext, jrt := newAPIFixture(t)

	// Command provider: base + the six; tool provider: base only.
	base := map[string]any{"cwd": "/cmd/vs/event", "abort": func() {}}
	jrt.SetCommandCtxProvider(func() map[string]any {
		cmd := map[string]any{"cwd": "/cmd/vs/event", "abort": func() {}}
		cmd["waitForIdle"] = func() error { return nil }
		cmd["newSession"] = func(opts map[string]any) (map[string]bool, error) {
			return map[string]bool{"cancelled": false}, nil
		}
		cmd["fork"] = func(entryID string, opts map[string]any) (map[string]bool, error) {
			return map[string]bool{"cancelled": false}, nil
		}
		cmd["navigateTree"] = func(targetID string, opts map[string]any) (map[string]bool, error) {
			return map[string]bool{"cancelled": false}, nil
		}
		cmd["switchSession"] = func(sessionPath string, opts map[string]any) (map[string]bool, error) {
			return map[string]bool{"cancelled": false}, nil
		}
		cmd["reload"] = func() error { return nil }
		return cmd
	})
	jrt.SetToolCtxProvider(func() map[string]any { return base })

	factoryJS := []byte(`module.exports = function (pi) {
		pi.registerCommand("probeCmd", {
			description: "probe",
			handler: function (args, ctx) {
				globalThis.__cmdCtx = JSON.stringify({
					newSession: typeof ctx.newSession,
					waitForIdle: typeof ctx.waitForIdle,
					abort: typeof ctx.abort
				});
			}
		});
		pi.on("session_start", function (event, ctx) {
			return {
				newSession: typeof ctx.newSession,
				waitForIdle: typeof ctx.waitForIdle,
				abort: typeof ctx.abort
			};
		});
	};`)
	js, err := TranspileExtension("/test/split.js", factoryJS, 1)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	fh, err := jsAPI.jrt.EvalModule("/test/split.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jsAPI.jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}

	// Command path: all six render (spot-check three) + base abort.
	cmd := ext.Commands["probeCmd"]
	if cmd == nil || cmd.Handler == nil {
		t.Fatal("probeCmd not registered")
	}
	if err := cmd.Handler("", nil); err != nil {
		t.Fatalf("command handler: %v", err)
	}
	var cmdCtx map[string]string
	if err := jsonUnmarshalText(evalString(t, jsAPI.jrt, `globalThis.__cmdCtx`), &cmdCtx); err != nil {
		t.Fatalf("cmd ctx echo: %v", err)
	}
	for _, k := range []string{"newSession", "waitForIdle"} {
		if cmdCtx[k] != "function" {
			t.Errorf("command ctx.%s = %q, want function", k, cmdCtx[k])
		}
	}
	if cmdCtx["abort"] != "function" {
		t.Errorf("command ctx.abort = %q, want function", cmdCtx["abort"])
	}

	// Event path: the six must be ABSENT (undefined), base abort present. The
	// pi.on bridge renders whatever map the runner's Emit passes (js_api.go
	// on-bridge: ctxVal) — a runner-shaped base ctx here.
	handlers := ext.Handlers["session_start"]
	if len(handlers) != 1 {
		t.Fatalf("expected 1 session_start handler, got %d", len(handlers))
	}
	got, err := handlers[0](map[string]any{"type": "session_start"}, map[string]any{
		"cwd":   "/cmd/vs/event",
		"abort": func() {},
	})
	if err != nil {
		t.Fatalf("event handler: %v", err)
	}
	m, _ := got.(map[string]any)
	if m["newSession"] != "undefined" || m["waitForIdle"] != "undefined" {
		t.Errorf("event ctx got command actions: %v (TS: only commands carry them)", m)
	}
	if m["abort"] != "function" {
		t.Errorf("event ctx.abort = %v, want function (base action)", m["abort"])
	}
}

// --- F2: pi.exec ---

// newExecFixture builds a fixture whose extension factory stashes pi.exec on
// a global (the factory-consumed pi object can't be reached otherwise — the
// captured method value stays alive through the closure). cwd is the load
// default the runtime records via SetExecDefaultCwd (a nonexistent dir makes
// the default-cwd plumbing observable: a child failing to chdir resolves
// code 1).
func newExecFixture(t *testing.T, cwd string) (*JSRuntime, string) {
	t.Helper()
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	t.Cleanup(func() { jrt.Close() })
	ext := NewExtension("/test/ext.ts", "/test/ext.ts", nil)
	rt := CreateExtensionRuntime()
	jsAPI, err := BuildJSAPI(jrt, ext, rt, cwd, nil)
	if err != nil {
		t.Fatalf("BuildJSAPI: %v", err)
	}
	factoryJS := []byte(`module.exports = function (pi) {
		globalThis.__piExec = pi.exec;
	};`)
	js, err := TranspileExtension("/test/exec.js", factoryJS, 1)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	fh, err := jrt.EvalModule("/test/exec.js", js)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if err := jrt.InvokeFactory(fh, jsAPI.PIValue()); err != nil {
		t.Fatalf("InvokeFactory: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return jrt, exe
}

// execCallJS builds one synchronous pi.exec call: exe + the -test.run arg +
// passthrough args (everything after "--" reaches TestExecHelperProcess via
// os.Args) + an opts JSON literal. YCODE_EXEC_MODE must be set in the parent
// env (the child inherits it). The result crosses as JSON text; the trailing
// "setup" keeps the eval's completion value a string (a promise completion
// value would make QJS_Eval js_std_await it — moot now that pi.exec is
// sync, kept as cheap insurance for future reshapes).
func execCallJS(exe, opts string, passthrough ...string) string {
	args, _ := json.Marshal(append([]string{"-test.run=^TestExecHelperProcess$", "--"}, passthrough...))
	return `globalThis.__execRes = JSON.stringify(globalThis.__piExec(` + mustJSONString(exe) + `, ` + string(args) + `, ` + opts + `));
		"setup";`
}

func readExecResult(t *testing.T, jrt *JSRuntime) string {
	t.Helper()
	return evalString(t, jrt, `String(globalThis.__execRes)`)
}

// The bootstrap installed the proxy (setFuncNoLeak → __pi_exec) and the build
// copied it onto pi.
func TestJSAPI_ExecIsFunction(t *testing.T) {
	jrt, _ := newExecFixture(t, "/test/cwd")
	if got := evalString(t, jrt, `typeof globalThis.__piExec`); got != "function" {
		t.Fatalf("pi.exec typeof = %q, want function", got)
	}
}

func TestJSAPI_ExecResolvesShape(t *testing.T) {
	jrt, exe := newExecFixture(t, t.TempDir()) // an existing default cwd
	t.Setenv("YCODE_EXEC_MODE", "echo")
	evalJS(t, jrt, "exec.js", execCallJS(exe, "{}", "a1", "a2"))
	_ = readExecResult(t, jrt)
	keys := evalString(t, jrt, `JSON.stringify(Object.keys(JSON.parse(globalThis.__execRes)).sort())`)
	if keys != `["code","killed","stderr","stdout"]` {
		t.Errorf("result keys = %s, want exactly the TS ExecResult shape", keys)
	}
	if out := evalString(t, jrt, `JSON.parse(globalThis.__execRes).stdout`); out != "hello-exec a1 a2" {
		t.Errorf("stdout = %q, want 'hello-exec a1 a2' (args pass through)", out)
	}
	if code := evalString(t, jrt, `JSON.parse(globalThis.__execRes).code`); code != "0" {
		t.Errorf("code = %s, want 0", code)
	}
	if killed := evalString(t, jrt, `String(JSON.parse(globalThis.__execRes).killed)`); killed != "false" {
		t.Errorf("killed = %s, want false", killed)
	}
}

// TS exec.ts:99-105 — a spawn failure resolves {code:1, empty}; it never
// rejects and the error text must not leak into stderr.
func TestJSAPI_ExecSpawnFailureResolves(t *testing.T) {
	jrt, _ := newExecFixture(t, "/test/cwd")
	evalJS(t, jrt, "execfail.js", `globalThis.__execRes = JSON.stringify(globalThis.__piExec("ycode-no-such-binary-f2", [], {}));
		"setup";`)
	_ = readExecResult(t, jrt)
	if code := evalString(t, jrt, `JSON.parse(globalThis.__execRes).code`); code != "1" {
		t.Errorf("code = %s, want 1 (TS resolves the catch path)", code)
	}
	if out := evalString(t, jrt, `JSON.parse(globalThis.__execRes).stdout + JSON.parse(globalThis.__execRes).stderr`); out != "" {
		t.Errorf("stdout+stderr = %q, want empty", out)
	}
}

func TestJSAPI_ExecTimeoutKills(t *testing.T) {
	jrt, exe := newExecFixture(t, t.TempDir())
	t.Setenv("YCODE_EXEC_MODE", "slow")
	start := time.Now()
	evalJS(t, jrt, "timeout.js", execCallJS(exe, `{timeout: 150}`))
	_ = readExecResult(t, jrt)
	if killed := evalString(t, jrt, `String(JSON.parse(globalThis.__execRes).killed)`); killed != "true" {
		t.Errorf("killed = %s, want true (the sole timeout indicator)", killed)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("timeout kill took %v — the process was not killed", elapsed)
	}
}

// An already-aborted signal short-circuits (TS exec.ts:66-68): {code:0,
// killed:true} with no run — the slow child would hang the synchronous call
// until its 30s exit if the check regressed and the spawn went ahead.
func TestJSAPI_ExecPreAbortedSignal(t *testing.T) {
	jrt, exe := newExecFixture(t, t.TempDir())
	t.Setenv("YCODE_EXEC_MODE", "slow")
	evalJS(t, jrt, "preabort.js", execCallJS(exe, `{signal: {aborted: true}}`))
	_ = readExecResult(t, jrt)
	if code := evalString(t, jrt, `JSON.parse(globalThis.__execRes).code`); code != "0" {
		t.Errorf("code = %s, want 0", code)
	}
	if killed := evalString(t, jrt, `String(JSON.parse(globalThis.__execRes).killed)`); killed != "true" {
		t.Errorf("killed = %s, want true", killed)
	}
}

// The runtime default cwd (SetExecDefaultCwd ← the load cwd, TS
// loader.ts:300-303) reaches the child: the fixture's default is a
// nonexistent dir, so the child fails to start and the result is code 1. If
// the default were dropped, the child would run and yield code 0.
func TestJSAPI_ExecDefaultCwdPlumbed(t *testing.T) {
	jrt, exe := newExecFixture(t, filepath.Join(t.TempDir(), "missing-dir"))
	t.Setenv("YCODE_EXEC_MODE", "echo")
	evalJS(t, jrt, "defaultcwd.js", execCallJS(exe, `{}`))
	_ = readExecResult(t, jrt)
	if code := evalString(t, jrt, `JSON.parse(globalThis.__execRes).code`); code != "1" {
		t.Errorf("code = %s, want 1 (the loader cwd default reached the child)", code)
	}
}
