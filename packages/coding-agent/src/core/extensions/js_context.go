package extensions

// js_context.go converts the runner's per-emit context map
// (runner.CreateContext() → map[string]any) into the JS object that handler
// functions receive as their second argument (`ctx`).
//
// The runner builds the ctx map fresh on every emit, with entries like:
//   ctx["cwd"], ctx["model"], ctx["hasUI"], ctx["isIdle"] (bool),
//   ctx["hasPendingMessages"] (bool), ctx["sessionManager"], ctx["abort"] (fn),
//   ctx["getContextUsage"] (fn), ctx["compact"] (fn), ctx["getSystemPrompt"] (fn)
//
// Function-valued entries don't cross the JSON bridge cleanly, so v1 surfaces
// the data fields natively and provides method-shaped wrappers for the
// read-only getters (getContextUsage, getSystemPrompt) by evaluating them
// eagerly when building the ctx. State-changing methods (abort, compact,
// newSession, fork, etc.) are deferred to v1.x — they require wiring the
// runner's callbacks back through the JS bridge, which is its own re-entrancy
// surface.
//
// The JS ctx shape (v1):
//   {
//     cwd: string,
//     hasUI: boolean,
//     model: object | undefined,
//     isIdle: boolean,            // resolved eagerly from the runner fn
//     hasPendingMessages: boolean,// resolved eagerly
//     ui: { notify: fn, status: fn, ... } | undefined,  // v1.x
//     getContextUsage: fn,        // calls back into the runner; see note below
//     getSystemPrompt: fn,
//   }

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fastschema/qjs"
)

// jsonMarshal is a tiny alias so isJSONSafe doesn't require importing
// encoding/json at every call site.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// BuildJSContext converts a runner ctx map into a JS object.
//
// OWNERSHIP (story-42 correction): the returned value holds one reference.
// Handing it to one of the CallXxxWithJSCtx methods TRANSFERS that reference
// (qjs SetPropertyStr consumes it — freeing afterward is a double-free that
// corrupts the WASM heap; verified by probe). Only free it yourself if you do
// NOT pass it to the bridge.
//
// Threading: call sites do NOT hold jrt.mu (the four js_api.go bridges call
// it before CallXxxWithJSCtx, which locks). Internal helpers that need the
// runtime (actionShim) lock it themselves — do NOT call this under j.mu.
func BuildJSContext(jrt *JSRuntime, runnerCtx map[string]any) (*qjs.Value, error) {
	ctx := jrt.Context()
	obj := ctx.NewObject()

	// --- Data fields (direct copies) ---
	// NOTE: values assigned via SetPropertyStr are owned by the object — do
	// NOT Free them here (Free releases the underlying handle, leaving the
	// property dangling).
	if cwd, ok := runnerCtx["cwd"].(string); ok {
		setPropertyNoLeak(obj, "cwd", ctx.NewString(cwd))
	}
	if hasUI, ok := runnerCtx["hasUI"].(bool); ok {
		setPropertyNoLeak(obj, "hasUI", ctx.NewBool(hasUI))
	}
	if isIdle, ok := runnerCtx["isIdle"].(bool); ok {
		setPropertyNoLeak(obj, "isIdle", ctx.NewBool(isIdle))
	}
	if hasPM, ok := runnerCtx["hasPendingMessages"].(bool); ok {
		setPropertyNoLeak(obj, "hasPendingMessages", ctx.NewBool(hasPM))
	}

	// signal: TS ctx.signal is the live AbortSignal (types.ts:309-311). A Go
	// context.Context can't cross the bridge, so it renders as
	// {aborted: <ctx.Err() != nil at build time>} — a snapshot, not live
	// polling (documented divergence).
	if sig, ok := runnerCtx["signal"].(context.Context); ok && sig != nil {
		sigObj := ctx.NewObject()
		setPropertyNoLeak(sigObj, "aborted", ctx.NewBool(sig.Err() != nil))
		setPropertyNoLeak(obj, "signal", sigObj)
	}

	// model: marshal the model object through JSON.
	if model, ok := runnerCtx["model"]; ok && model != nil {
		if mv, err := goToJSValue(ctx, model); err == nil {
			setPropertyNoLeak(obj, "model", mv)
		}
	}

	// --- Method-shaped wrappers (read-only getters) ---
	if getCU, ok := runnerCtx["getContextUsage"].(func() any); ok {
		if cu := getCU(); cu != nil {
			if v, err := goToJSValue(ctx, cu); err == nil {
				setPropertyNoLeak(obj, "contextUsage", v)
			}
		}
	}
	if getSP, ok := runnerCtx["getSystemPrompt"].(func() string); ok {
		setPropertyNoLeak(obj, "systemPrompt", ctx.NewString(getSP()))
	}

	// --- ui (the ExtensionUIContext) ---
	if uiVal, ok := runnerCtx["ui"].(*ExtensionUIContext); ok && uiVal != nil {
		ui := buildJSUI(ctx, uiVal)
		setPropertyNoLeak(obj, "ui", ui)
	}

	// --- State-changing actions (F1): abort/compact/shutdown are
	// ExtensionContext members in the reference (types.ts:298-327) — every
	// ctx (events, tools, shortcuts, commands) carries them. Their Go bodies
	// emit extension events (abort tears down the agent run, shutdown exits,
	// compact compacts), so they run fire-and-forget on their own goroutine:
	// invoked synchronously inside a handler's Eval, the nested emit would
	// re-enter qjs and deadlock (the story-42 compact precedent). JS-visible
	// semantics match the reference (void → undefined immediately).
	setFireAndForget(ctx, obj, "abort", runnerCtx["abort"])
	setFireAndForget(ctx, obj, "compact", wrapCompactFn(runnerCtx["compact"]))
	setFireAndForget(ctx, obj, "shutdown", runnerCtx["shutdown"])

	// --- Command-only actions (F1): waitForIdle/newSession/fork/navigateTree/
	// switchSession/reload exist ONLY on the command ctx (TS
	// ExtensionCommandContext, types.ts:333-364) — the CreateContext map used
	// for events/tools/shortcuts never carries them, so the TS interface
	// split falls out of which map the caller hands us. Each renders as a
	// __piMakeAction shim returning a native Promise that the Go dispatcher
	// settles later (they emit session events; see js_runtime.go).
	for _, name := range []string{"waitForIdle", "newSession", "fork", "navigateTree", "switchSession", "reload"} {
		if _, present := runnerCtx[name]; !present {
			continue
		}
		if shim := jrt.actionShim(name); shim != nil {
			setPropertyNoLeak(obj, name, shim) // consumes shim's ref — no Free
		}
	}

	return obj, nil
}

// setFireAndForget installs fn as a JS method on obj that invokes it on its
// own goroutine and returns undefined immediately. fnValue is the raw map
// entry; only plain func() values render (a nil func or a differently-typed
// entry is skipped — the JS property stays absent, matching the reference's
// undefined).
func setFireAndForget(ctx *qjs.Context, obj *qjs.Value, jsName string, fnValue any) {
	fn, ok := fnValue.(func())
	if !ok || fn == nil {
		return
	}
	scratch := "__pi_ff_" + jsName
	setFuncNoLeak(ctx, scratch, func(t *qjs.This) (*qjs.Value, error) {
		go func() {
			defer func() { _ = recover() }() // a panicking action must not crash the host
			fn()
		}()
		return t.Context().NewUndefined(), nil
	})
	fnVal := ctx.Global().GetPropertyStr(scratch)
	// NO Free: SetPropertyStr consumes the get's reference (the global keeps
	// its own) — freeing left two holders on one ref.
	setPropertyNoLeak(obj, jsName, fnVal)
}

// BuildReplacedSessionContext renders a ReplacedSessionContext for a JS
// withSession callback: the replacement session's command ctx (via rc.Ctx())
// plus sendMessage/sendUserMessage (TS ReplacedSessionContext adds both,
// types.ts:366-381). Both send methods run fire-and-forget and take a single
// argument coerced with .String() — object fidelity (CustomMessage shapes) is
// a documented v1 limitation.
//
// OWNERSHIP: the returned value holds one reference — hand it to
// CallShortcutHandler (which consumes it), never Free it.
func BuildReplacedSessionContext(jrt *JSRuntime, rc *ReplacedSessionContext) (*qjs.Value, error) {
	runnerCtx := func() (m map[string]any) {
		if rc == nil || rc.Ctx == nil {
			return nil
		}
		defer func() {
			if recover() != nil {
				m = nil // stale replacement runner (assertActive) → bare ctx
			}
		}()
		return rc.Ctx()
	}()
	if runnerCtx == nil {
		runnerCtx = map[string]any{}
	}
	obj, err := BuildJSContext(jrt, runnerCtx)
	if err != nil {
		return nil, err
	}
	if rc != nil {
		setSingleArgFireAndForget(jrt.Context(), obj, "sendMessage", "__pi_ff_sendMessage", func(arg string) {
			if rc.SendMessage != nil {
				_ = rc.SendMessage(arg) //nolint:errcheck — fire-and-forget; failures surface via session events
			}
		})
		setSingleArgFireAndForget(jrt.Context(), obj, "sendUserMessage", "__pi_ff_sendUserMessage", func(arg string) {
			if rc.SendUserMessage != nil {
				_ = rc.SendUserMessage(arg, nil) //nolint:errcheck — fire-and-forget
			}
		})
	}
	return obj, nil
}

// setSingleArgFireAndForget installs a JS method on obj that takes one
// argument (coerced with .String()), passes it to fn on a goroutine, and
// returns undefined immediately.
func setSingleArgFireAndForget(ctx *qjs.Context, obj *qjs.Value, jsName, scratch string, fn func(arg string)) {
	setFuncNoLeak(ctx, scratch, func(t *qjs.This) (*qjs.Value, error) {
		arg := ""
		if args := t.Args(); len(args) > 0 {
			arg = args[0].String()
		}
		go func() {
			defer func() { _ = recover() }()
			fn(arg)
		}()
		return t.Context().NewUndefined(), nil
	})
	fnVal := ctx.Global().GetPropertyStr(scratch)
	// NO Free — SetPropertyStr consumed the get's reference (see
	// setFireAndForget).
	setPropertyNoLeak(obj, jsName, fnVal)
}

// wrapCompactFn adapts the map's compact entry — func(*CompactOptions) error
// in the runner map (story-42 binding, itself already fire-and-forget) — to
// the plain func() the shim needs. A JS compact() call takes no arguments
// (v1; TS takes optional CompactOptions).
func wrapCompactFn(v any) func() {
	fn, ok := v.(func(*CompactOptions) error)
	if !ok || fn == nil {
		if f0, ok0 := v.(func()); ok0 {
			return f0
		}
		return nil
	}
	return func() { _ = fn(nil) }
}

// buildJSUI constructs the JS-side `ui` object from a Go *ExtensionUIContext.
//
// notify and status are fire-and-forget (return immediately). input, select,
// confirm, and editor are async — they return Promises that resolve when the
// user responds.
//
// Implementation note: the async function bodies block synchronously on the Go
// widget call (no goroutine). This is deliberate — qjs's WASM runtime is not
// reentrant, and resolving a Promise from a goroutine while Eval is on the
// stack crashes with "wasm: unreachable". Blocking synchronously is safe
// because the TUI runs on its own goroutine and stays responsive while the JS
// thread waits; extension handlers are sequential by design, so no other JS
// work is starved.
//
// JS API (mirrors the reference's ExtensionUIContext, extensions/types.ts):
//
//	ui.notify(message, level?)                 → void
//	ui.status(key, text)                        → void
//	await ui.input(title, placeholder?, opts?)  → string  ("" on cancel)
//	await ui.select(title, options, opts?)      → number  (-1 on cancel;
//	                                                      the reference resolves
//	                                                      the option string)
//	await ui.confirm(title, message, opts?)     → boolean
//	await ui.editor(title, prefill?)            → string  ("" on cancel)
//
// opts is { timeout?: number, defaultIdx?: number } (the reference also takes
// { signal }, which ycode does not support yet). A bare number in the opts
// position is tolerated as timeoutMs.
//
// On error (e.g., the underlying Go function fails), the Promise rejects.
func buildJSUI(ctx *qjs.Context, ui *ExtensionUIContext) *qjs.Value {
	obj := ctx.NewObject()
	// ui.notify(message, level)
	setFuncNoLeak(ctx, "__ui_notify", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		msg := ""
		level := "info"
		if len(args) > 0 {
			msg = args[0].String()
		}
		if len(args) > 1 {
			level = args[1].String()
		}
		if ui.Notify != nil {
			ui.Notify(msg, level)
		}
		return ctx.NewUndefined(), nil
	})
	notifyFn := ctx.Global().GetPropertyStr("__ui_notify")
	// NO Free: SetPropertyStr consumes the get's reference (F1 ref-accounting
	// fix — the old Free left two holders on one ref).
	setPropertyNoLeak(obj, "notify", notifyFn)

	// ui.status(key, text)
	setFuncNoLeak(ctx, "__ui_status", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		key, msg := "", ""
		if len(args) > 0 {
			key = args[0].String()
		}
		if len(args) > 1 {
			msg = args[1].String()
		}
		if ui.Status != nil {
			ui.Status(key, msg)
		}
		return ctx.NewUndefined(), nil
	})
	statusFn := ctx.Global().GetPropertyStr("__ui_status")
	// NO Free — SetPropertyStr consumed the get's reference (see notify).
	setPropertyNoLeak(obj, "status", statusFn)

	// ui.input(title, placeholder?, opts?) → Promise<string>
	setAsyncFuncNoLeak(ctx, "__ui_input", func(this *qjs.This) {
		args := this.Args()
		title, placeholder := "", ""
		if len(args) > 0 {
			title = args[0].String()
		}
		if len(args) > 1 {
			placeholder = args[1].String()
		}
		_, timeoutMs := 0, 0
		if len(args) > 2 {
			_, timeoutMs = uiDialogOpts(args[2])
		}
		text, err := ui.Input(title, placeholder, timeoutMs)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewString(text))
	})
	inputFn := ctx.Global().GetPropertyStr("__ui_input")
	// NO Free — SetPropertyStr consumed the get's reference (see notify).
	setPropertyNoLeak(obj, "input", inputFn)

	// ui.select(title, options, opts?) → Promise<number>
	// Returns the selected index, or -1 if the user cancels.
	setAsyncFuncNoLeak(ctx, "__ui_select", func(this *qjs.This) {
		args := this.Args()
		if len(args) < 2 || !args[0].IsString() || !args[1].IsArray() {
			this.Promise().Reject(this.Context().NewError(errors.New("ui.select: expected (title: string, options: string[], opts?)")))
			return
		}
		title := args[0].String()
		arr := args[1]
		n := int(arr.Len())
		options := make([]string, 0, n)
		for i := 0; i < n; i++ {
			elem := arr.GetPropertyIndex(int64(i))
			options = append(options, elem.String())
			elem.Free()
		}
		defaultIdx, timeoutMs := 0, 0
		if len(args) > 2 {
			defaultIdx, timeoutMs = uiDialogOpts(args[2])
		}
		idx, err := ui.Select(title, options, defaultIdx, timeoutMs)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewInt32(int32(idx)))
	})
	selectFn := ctx.Global().GetPropertyStr("__ui_select")
	// NO Free — SetPropertyStr consumed the get's reference (see notify).
	setPropertyNoLeak(obj, "select", selectFn)

	// ui.confirm(title, message, opts?) → Promise<boolean>
	setAsyncFuncNoLeak(ctx, "__ui_confirm", func(this *qjs.This) {
		args := this.Args()
		title, message := "", ""
		if len(args) > 0 {
			title = args[0].String()
		}
		if len(args) > 1 {
			message = args[1].String()
		}
		_, timeoutMs := 0, 0
		if len(args) > 2 {
			_, timeoutMs = uiDialogOpts(args[2])
		}
		ok, err := ui.Confirm(title, message, timeoutMs)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewBool(ok))
	})
	confirmFn := ctx.Global().GetPropertyStr("__ui_confirm")
	// NO Free — SetPropertyStr consumed the get's reference (see notify).
	setPropertyNoLeak(obj, "confirm", confirmFn)

	// ui.editor(title, prefill?) → Promise<string>
	// Multi-line editor (no timeout — interrupting a compose flow mid-edit
	// would lose the user's input).
	setAsyncFuncNoLeak(ctx, "__ui_editor", func(this *qjs.This) {
		args := this.Args()
		title, prefill := "", ""
		if len(args) > 0 {
			title = args[0].String()
		}
		if len(args) > 1 {
			prefill = args[1].String()
		}
		text, err := ui.Editor(title, prefill)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewString(text))
	})
	editorFn := ctx.Global().GetPropertyStr("__ui_editor")
	// NO Free — SetPropertyStr consumed the get's reference (see notify).
	setPropertyNoLeak(obj, "editor", editorFn)

	return obj
}

// uiDialogOpts extracts { timeout, defaultIdx } from a dialog's trailing opts
// argument (pi passes { signal?, timeout? }; ycode adds defaultIdx for its
// preselected-entry extension). A bare number is tolerated as timeoutMs.
// Non-object/non-number values yield zeroes.
func uiDialogOpts(v *qjs.Value) (defaultIdx, timeoutMs int) {
	if v == nil || v.IsUndefined() || v.IsNull() {
		return 0, 0
	}
	if v.IsNumber() {
		return 0, int(v.Int32())
	}
	if !v.IsObject() {
		return 0, 0
	}
	if t := v.GetPropertyStr("timeout"); t != nil && t.IsNumber() {
		timeoutMs = int(t.Int32())
	}
	if d := v.GetPropertyStr("defaultIdx"); d != nil && d.IsNumber() {
		defaultIdx = int(d.Int32())
	}
	return defaultIdx, timeoutMs
}

// ScrubRunnerCtx converts a runner ctx map (which may carry Go func values,
// pointers, non-JSON-serializable types) into a plain map[string]any that's
// safe to JSON-marshal across the JS bridge. Function-valued getters are
// resolved eagerly so their results appear as fields on the JS ctx object.
//
// This is the JSON-path counterpart to BuildJSContext (which builds a JS
// object directly when a *qjs.Value is wanted). Most call sites use the JSON
// path via CallHandler/fireJSON, so this is the primary scrubber.
func ScrubRunnerCtx(runnerCtx map[string]any) map[string]any {
	if runnerCtx == nil {
		return nil
	}
	out := make(map[string]any, len(runnerCtx)+2)
	// Copy JSON-safe data fields directly.
	for k, v := range runnerCtx {
		switch v.(type) {
		case string, bool, int, int32, int64, float32, float64, nil:
			out[k] = v
		default:
			// Skip non-primitive values (model struct, sessionManager, funcs);
			// they're handled below or intentionally omitted.
		}
	}
	// Resolve function-valued getters eagerly into data-shaped fields.
	if getCU, ok := runnerCtx["getContextUsage"].(func() any); ok {
		if cu := getCU(); cu != nil {
			out["contextUsage"] = cu
		}
	}
	if getSP, ok := runnerCtx["getSystemPrompt"].(func() string); ok {
		out["systemPrompt"] = getSP()
	}
	// Try to surface `model` if it JSON-marshals (skip on failure).
	if m, ok := runnerCtx["model"]; ok && m != nil {
		if isJSONSafe(m) {
			out["model"] = m
		}
	}
	return out
}

// isJSONSafe reports whether v can be marshaled to JSON without error.
func isJSONSafe(v any) bool {
	var buf []byte
	var err error
	// json.Marshal lives in js_api.go's import block; reuse via a small closure.
	buf, err = jsonMarshal(v)
	return err == nil && len(buf) > 0 && string(buf) != "null"
}

// Note on the former v1.x deferrals (implemented in F1, 2026-11-25):
//
// The runner's ctx map carries state-changing callbacks. abort/compact/
// shutdown render as fire-and-forget shims on every ctx. The six command-only
// actions (waitForIdle/newSession/fork/navigateTree/switchSession/reload)
// render as __piMakeAction shims that return native Promises settled by the
// Go dispatcher (js_runtime.go) — they emit session events, so they can never
// run inside the submitting handler's Eval (qjs forbids re-entrancy). The
// eval's job pump resumes `await ctx.action()` continuations (probe-verified
// 2026-11-25). Remaining known divergences: newSession's setup(sessionManager)
// callback is not bridged (SessionManager has no JS rendering), and the
// modes' TUI glue (editor text, loading animation) does not run for
// extension-initiated switches.
