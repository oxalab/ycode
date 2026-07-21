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
	"encoding/json"
	"errors"

	"github.com/fastschema/qjs"
)

// jsonMarshal is a tiny alias so isJSONSafe doesn't require importing
// encoding/json at every call site.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// BuildJSContext converts a runner ctx map into a JS object. The result must
// be freed via jrt.SafeFree (NOT raw .Free()) — it lives across the handler
// call which can poison the runtime.
//
// Caller holds jrt.mu (this is invoked from inside fireJSON's call chain,
// which is already locked).
func BuildJSContext(jrt *JSRuntime, runnerCtx map[string]any) (*qjs.Value, error) {
	ctx := jrt.Context()
	obj := ctx.NewObject()

	// --- Data fields (direct copies) ---
	// NOTE: values assigned via SetPropertyStr are owned by the object — do
	// NOT Free them here (Free releases the underlying handle, leaving the
	// property dangling).
	if cwd, ok := runnerCtx["cwd"].(string); ok {
		obj.SetPropertyStr("cwd", ctx.NewString(cwd))
	}
	if hasUI, ok := runnerCtx["hasUI"].(bool); ok {
		obj.SetPropertyStr("hasUI", ctx.NewBool(hasUI))
	}
	if isIdle, ok := runnerCtx["isIdle"].(bool); ok {
		obj.SetPropertyStr("isIdle", ctx.NewBool(isIdle))
	}
	if hasPM, ok := runnerCtx["hasPendingMessages"].(bool); ok {
		obj.SetPropertyStr("hasPendingMessages", ctx.NewBool(hasPM))
	}

	// model: marshal the model object through JSON.
	if model, ok := runnerCtx["model"]; ok && model != nil {
		if mv, err := goToJSValue(ctx, model); err == nil {
			obj.SetPropertyStr("model", mv)
		}
	}

	// --- Method-shaped wrappers (read-only getters) ---
	if getCU, ok := runnerCtx["getContextUsage"].(func() any); ok {
		if cu := getCU(); cu != nil {
			if v, err := goToJSValue(ctx, cu); err == nil {
				obj.SetPropertyStr("contextUsage", v)
			}
		}
	}
	if getSP, ok := runnerCtx["getSystemPrompt"].(func() string); ok {
		obj.SetPropertyStr("systemPrompt", ctx.NewString(getSP()))
	}

	// --- ui (the ExtensionUIContext) ---
	if uiVal, ok := runnerCtx["ui"].(*ExtensionUIContext); ok && uiVal != nil {
		ui := buildJSUI(ctx, uiVal)
		obj.SetPropertyStr("ui", ui)
	}

	return obj, nil
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
// JS API:
//   ui.notify(message, level?)                 → void
//   ui.status(message)                          → void
//   await ui.input(message, placeholder?, timeoutMs?)     → string  ("" on cancel)
//   await ui.select(options: string[], defaultIdx?, timeoutMs?)  → number  (-1 on cancel)
//   await ui.confirm(message, timeoutMs?)       → boolean
//   await ui.editor(message, prefill?)          → string  ("" on cancel)
//
// On error (e.g., the underlying Go function fails), the Promise rejects.
func buildJSUI(ctx *qjs.Context, ui *ExtensionUIContext) *qjs.Value {
	obj := ctx.NewObject()
	// ui.notify(message, level)
	ctx.SetFunc("__ui_notify", func(t *qjs.This) (*qjs.Value, error) {
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
	obj.SetPropertyStr("notify", notifyFn)
	notifyFn.Free() // GetPropertyStr returns a reference; freeing the ref is safe after assign

	// ui.status(message)
	ctx.SetFunc("__ui_status", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		msg := ""
		if len(args) > 0 {
			msg = args[0].String()
		}
		if ui.Status != nil {
			ui.Status(msg)
		}
		return ctx.NewUndefined(), nil
	})
	statusFn := ctx.Global().GetPropertyStr("__ui_status")
	obj.SetPropertyStr("status", statusFn)
	statusFn.Free()

	// ui.input(message, placeholder?, timeoutMs?) → Promise<string>
	ctx.SetAsyncFunc("__ui_input", func(this *qjs.This) {
		args := this.Args()
		message, placeholder := "", ""
		timeoutMs := 0
		if len(args) > 0 {
			message = args[0].String()
		}
		if len(args) > 1 {
			placeholder = args[1].String()
		}
		if len(args) > 2 {
			timeoutMs = int(args[2].Int32())
		}
		text, err := ui.Input(message, placeholder, timeoutMs)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewString(text))
	})
	inputFn := ctx.Global().GetPropertyStr("__ui_input")
	obj.SetPropertyStr("input", inputFn)
	inputFn.Free()

	// ui.select(options: string[], defaultIdx?, timeoutMs?) → Promise<number>
	// Returns the selected index, or -1 if the user cancels.
	ctx.SetAsyncFunc("__ui_select", func(this *qjs.This) {
		args := this.Args()
		if len(args) == 0 || !args[0].IsArray() {
			this.Promise().Reject(this.Context().NewError(errors.New("ui.select: first argument must be an array of strings")))
			return
		}
		arr := args[0]
		n := int(arr.Len())
		options := make([]string, 0, n)
		for i := 0; i < n; i++ {
			elem := arr.GetPropertyIndex(int64(i))
			options = append(options, elem.String())
			elem.Free()
		}
		defaultIdx, timeoutMs := 0, 0
		if len(args) > 1 {
			defaultIdx = int(args[1].Int32())
		}
		if len(args) > 2 {
			timeoutMs = int(args[2].Int32())
		}
		idx, err := ui.Select(options, defaultIdx, timeoutMs)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewInt32(int32(idx)))
	})
	selectFn := ctx.Global().GetPropertyStr("__ui_select")
	obj.SetPropertyStr("select", selectFn)
	selectFn.Free()

	// ui.confirm(message, timeoutMs?) → Promise<boolean>
	ctx.SetAsyncFunc("__ui_confirm", func(this *qjs.This) {
		args := this.Args()
		message := ""
		timeoutMs := 0
		if len(args) > 0 {
			message = args[0].String()
		}
		if len(args) > 1 {
			timeoutMs = int(args[1].Int32())
		}
		ok, err := ui.Confirm(message, timeoutMs)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewBool(ok))
	})
	confirmFn := ctx.Global().GetPropertyStr("__ui_confirm")
	obj.SetPropertyStr("confirm", confirmFn)
	confirmFn.Free()

	// ui.editor(message, prefill?) → Promise<string>
	// Multi-line editor (no timeout — interrupting a compose flow mid-edit
	// would lose the user's input).
	ctx.SetAsyncFunc("__ui_editor", func(this *qjs.This) {
		args := this.Args()
		message, prefill := "", ""
		if len(args) > 0 {
			message = args[0].String()
		}
		if len(args) > 1 {
			prefill = args[1].String()
		}
		text, err := ui.Editor(message, prefill)
		if err != nil {
			this.Promise().Reject(this.Context().NewError(err))
			return
		}
		this.Promise().Resolve(this.Context().NewString(text))
	})
	editorFn := ctx.Global().GetPropertyStr("__ui_editor")
	obj.SetPropertyStr("editor", editorFn)
	editorFn.Free()

	return obj
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
// Note on the v1.x deferrals (documented for future implementers):
//
// The runner's ctx map also carries state-changing callbacks:
//   - abort()              — abort the current agent operation
//   - compact(opts)        — trigger context compaction
//   - shutdown()           — shut down the session
//   - waitForIdle()        — (command ctx only) block until agent idle
//   - newSession/fork/navigateTree/switchSession/reload — (command ctx only)
//
// Bridging these requires calling back into Go from inside a JS handler —
// which is a re-entrancy surface (the handler is itself running inside a
// qjs Eval). The clean way is to queue the Go-side callback on a channel and
// let the host execute it after the Eval returns, then resume the JS if it
// needs the result. That's the v1.x work item. For now, handlers get the
// read-only ctx fields above, which covers the common cases (cwd, model,
// isIdle, contextUsage, systemPrompt).
