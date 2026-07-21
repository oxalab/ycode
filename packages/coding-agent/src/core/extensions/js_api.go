package extensions

// js_api.go builds the JavaScript-facing `pi` object (the ExtensionAPI) for JS
// extensions. Each method is a Go-backed qjs function installed on a plain JS
// object; when the extension's factory calls `pi.registerTool(...)`,
// `pi.on(...)`, etc., the corresponding Go closure runs, marshals the JS
// arguments, and populates the same *Extension maps the JSON loader and the
// runner consume.
//
// Design:
//   - Registration methods (registerTool/on/registerCommand/registerShortcut/
//     registerFlag/registerMessageRenderer/registerProvider) translate JS args
//     into Go values and delegate to the existing extensionAPI so bookkeeping
//     (source-info stamping, flag-default seeding, provider queueing) happens
//     identically to JSON-loaded extensions.
//   - Any JS function value passed by the extension (a handler, an execute fn,
//     a command/shortcut handler) is pinned in the JS-side registry via
//     JSRuntime.pinCallable; the Go-side ExtensionHandler / ToolDefinition.Execute
//     closure calls back into JSRuntime.CallHandler / CallToolExecute / etc.
//   - Action methods (sendMessage/sendUserMessage/setModel/exec/...) delegate
//     to the shared runtime's Actions the same way extensionAPI does.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fastschema/qjs"
)

// jsExtensionAPI is the Go backing for a JS extension's `pi` object. It owns:
//   - the underlying extensionAPI (for map population + bookkeeping)
//   - the JSRuntime (so it can pin/call JS function values)
//   - the *qjs.Value of the `pi` object handed to the factory
type jsExtensionAPI struct {
	api  ExtensionAPI
	jrt  *JSRuntime
	cwd  string
	pi   *qjs.Value
}

// BuildJSAPI constructs the JS `pi` object for one extension, wiring every
// method to a Go-backed qjs function. The returned value must be freed via
// jrt.SafeFree (NOT raw .Free()) because it lives across factory invocation,
// which can poison the runtime.
func BuildJSAPI(jrt *JSRuntime, extension *Extension, runtime *ExtensionRuntime, cwd string, eventBus EventBus) (*jsExtensionAPI, error) {
	api := createExtensionAPI(extension, runtime, cwd, eventBus)
	jsAPI := &jsExtensionAPI{api: api, jrt: jrt, cwd: cwd}

	ctx := jrt.Context()
	pi := ctx.NewObject()

	// --- Registration methods ---

	// pi.on(eventType, handler)
	ctx.SetFunc("__pi_on", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 2 {
			return ctx.NewUndefined(), nil
		}
		eventName := args[0].String()
		handlerFn := args[1]
		if !handlerFn.IsFunction() {
			return ctx.NewUndefined(), nil
		}
		handle, err := jrt.pinCallable(handlerFn)
		if err != nil {
			return nil, fmt.Errorf("pi.on: %w", err)
		}
		// Wrap as a Go ExtensionHandler that bridges back into JS. The ctx
		// passed by the runner is a map[string]any with func/pointer entries
		// (including the *ExtensionUIContext) that don't JSON-marshal. We build
		// a real JS ctx object (with cwd/model/isIdle/contextUsage/systemPrompt
		// AND a ui object carrying notify/status Go-backed functions) and pass
		// it via CallHandlerWithJSCtx so ctx.ui.notify works from JS handlers.
		api.On(eventName, func(event, ctxVal any) (any, error) {
			runnerCtx, _ := ctxVal.(map[string]any)
			if runnerCtx == nil {
				runnerCtx = map[string]any{"cwd": jsAPI.cwd}
			}
			jsCtx, err := BuildJSContext(jrt, runnerCtx)
			if err != nil {
				return nil, fmt.Errorf("build js ctx: %w", err)
			}
			defer jrt.SafeFree(jsCtx)
			return jrt.CallHandlerWithJSCtx(handle, event, jsCtx)
		})
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "on", "__pi_on"); err != nil {
		return nil, err
	}

	// pi.registerTool(toolDef)
	ctx.SetFunc("__pi_registerTool", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 1 {
			return ctx.NewUndefined(), nil
		}
		def, err := jsToolDefToGo(args[0], jrt)
		if err != nil {
			return nil, fmt.Errorf("pi.registerTool: %w", err)
		}
		api.RegisterTool(def, extension.SourceInfo)
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "registerTool", "__pi_registerTool"); err != nil {
		return nil, err
	}

	// pi.registerCommand(name, { description, handler, getArgumentCompletions })
	ctx.SetFunc("__pi_registerCommand", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 2 {
			return ctx.NewUndefined(), nil
		}
		name := args[0].String()
		opts := args[1]
		// Extract handler function and pin it.
		handlerProp := opts.GetPropertyStr("handler")
		var goHandler func(args string, ctx context.Context) error
		if handlerProp.IsFunction() {
			hh, err := jrt.pinCallable(handlerProp)
			if err != nil {
				return nil, fmt.Errorf("pi.registerCommand: %w", err)
			}
			goHandler = func(argsStr string, _ context.Context) error {
				return jrt.CallCommandHandler(hh, argsStr, map[string]any{"cwd": cwd})
			}
		}
		handlerProp.Free()
		// Build options map for the underlying API (description only; completions
		// can't cross the JS↔Go function boundary cleanly in v1).
		options := map[string]any{}
		if descVal := opts.GetPropertyStr("description"); descVal != nil {
			options["description"] = descVal.String()
			descVal.Free()
		}
		api.RegisterCommand(name, goHandler, options)
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "registerCommand", "__pi_registerCommand"); err != nil {
		return nil, err
	}

	// pi.registerShortcut(shortcut, { description, handler })
	ctx.SetFunc("__pi_registerShortcut", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 2 {
			return ctx.NewUndefined(), nil
		}
		shortcut := args[0].String()
		opts := args[1]
		handlerProp := opts.GetPropertyStr("handler")
		var goHandler func(ctx context.Context) error
		if handlerProp.IsFunction() {
			hh, err := jrt.pinCallable(handlerProp)
			if err != nil {
				handlerProp.Free()
				return nil, fmt.Errorf("pi.registerShortcut: %w", err)
			}
			goHandler = func(_ context.Context) error {
				_, err := jrt.CallHandler(hh, map[string]any{"cwd": cwd}, nil)
				return err
			}
		}
		handlerProp.Free()
		options := map[string]any{}
		if descVal := opts.GetPropertyStr("description"); descVal != nil {
			options["description"] = descVal.String()
			descVal.Free()
		}
		api.RegisterShortcut(shortcut, goHandler, options)
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "registerShortcut", "__pi_registerShortcut"); err != nil {
		return nil, err
	}

	// pi.registerFlag(name, { description, type, default })
	ctx.SetFunc("__pi_registerFlag", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 2 {
			return ctx.NewUndefined(), nil
		}
		name := args[0].String()
		opts := args[1]
		f := &ExtensionFlag{Name: name}
		if d := opts.GetPropertyStr("description"); d != nil {
			f.Description = d.String()
			d.Free()
		}
		if ty := opts.GetPropertyStr("type"); ty != nil {
			f.Type = ty.String()
			ty.Free()
		}
		if dv := opts.GetPropertyStr("default"); dv != nil {
			f.Default = jsValueToGo(dv)
			dv.Free()
		}
		api.RegisterFlag(f)
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "registerFlag", "__pi_registerFlag"); err != nil {
		return nil, err
	}

	// pi.getFlag(name) → boolean | string | undefined
	ctx.SetFunc("__pi_getFlag", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 1 {
			return ctx.NewUndefined(), nil
		}
		name := args[0].String()
		if v, ok := runtime.FlagValues[name]; ok {
			return goToJSValue(ctx, v)
		}
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "getFlag", "__pi_getFlag"); err != nil {
		return nil, err
	}

	// pi.registerMessageRenderer(customType, renderer) — v1 stores the renderer
	// as a no-op (the TUI MessageRenderer signature is Go-only; bridging it
	// would require the full ctx/ui surface). The registration is recorded so
	// the extension loads without error.
	ctx.SetFunc("__pi_registerMessageRenderer", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 1 {
			return ctx.NewUndefined(), nil
		}
		customType := args[0].String()
		api.RegisterMessageRenderer(customType, func(msg any, opts MessageRenderOptions, theme any) any {
			return nil // v1: JS-side rendering not bridged
		})
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "registerMessageRenderer", "__pi_registerMessageRenderer"); err != nil {
		return nil, err
	}

	// pi.registerProvider(name, config)
	ctx.SetFunc("__pi_registerProvider", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 2 {
			return ctx.NewUndefined(), nil
		}
		name := args[0].String()
		cfg, err := jsProviderConfigToGo(args[1])
		if err != nil {
			return nil, fmt.Errorf("pi.registerProvider: %w", err)
		}
		api.RegisterProvider(name, cfg)
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "registerProvider", "__pi_registerProvider"); err != nil {
		return nil, err
	}

	// pi.unregisterProvider(name)
	ctx.SetFunc("__pi_unregisterProvider", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 1 {
			return ctx.NewUndefined(), nil
		}
		api.UnregisterProvider(args[0].String())
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "unregisterProvider", "__pi_unregisterProvider"); err != nil {
		return nil, err
	}

	// --- Action methods ---

	// pi.sendMessage(msg) — msg is a JS object; marshal and delegate.
	ctx.SetFunc("__pi_sendMessage", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 1 {
			return ctx.NewUndefined(), nil
		}
		msg := jsValueToGo(args[0])
		_ = api.SendMessage(msg)
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "sendMessage", "__pi_sendMessage"); err != nil {
		return nil, err
	}

	// pi.sendUserMessage(text, images?)
	ctx.SetFunc("__pi_sendUserMessage", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 1 {
			return ctx.NewUndefined(), nil
		}
		text := args[0].String()
		var images []any
		if len(args) > 1 && args[1].IsObject() {
			images = jsValueToGo(args[1]).([]any)
		}
		_ = api.SendUserMessage(text, images)
		return ctx.NewUndefined(), nil
	})
	if err := setMethod(ctx, pi, "sendUserMessage", "__pi_sendUserMessage"); err != nil {
		return nil, err
	}

	// pi.exec(command, args?, options?) — v1: not bridged (returns rejected promise
	// stub). Pi.exec is Go-backed in the reference too; wiring it requires the
	// bash executor which lives in the parent package. Deferred to v1.x.
	ctx.SetFunc("__pi_exec_rejected", func(t *qjs.This) (*qjs.Value, error) {
		return nil, fmt.Errorf("pi.exec is not yet supported in this build")
	})
	if err := setMethod(ctx, pi, "exec", "__pi_exec_rejected"); err != nil {
		return nil, err
	}

	jsAPI.pi = pi
	return jsAPI, nil
}

// PIValue returns the JS `pi` object to pass to the extension factory. The
// caller invokes jrt.InvokeFactory(factoryHandle, jsAPI.PIValue()).
func (j *jsExtensionAPI) PIValue() *qjs.Value { return j.pi }

// setMethod assigns the named global Go-function under `name` on the object.
// qjs's SetFunc installs into the global object, so we copy it onto `obj` via
// a property assignment using a stable global placeholder.
func setMethod(ctx *qjs.Context, obj *qjs.Value, name, globalFn string) error {
	g := ctx.Global()
	fn := g.GetPropertyStr(globalFn)
	defer fn.Free()
	obj.SetPropertyStr(name, fn)
	return nil
}

// jsToolDefToGo converts a JS tool-definition object into a *ToolDefinition.
// The `execute` function (if present) is pinned and wrapped as a Go closure.
func jsToolDefToGo(v *qjs.Value, jrt *JSRuntime) (*ToolDefinition, error) {
	def := &ToolDefinition{}
	if n := v.GetPropertyStr("name"); n != nil {
		def.Name = n.String()
		n.Free()
	}
	if l := v.GetPropertyStr("label"); l != nil {
		def.Label = l.String()
		l.Free()
	}
	if d := v.GetPropertyStr("description"); d != nil {
		def.Description = d.String()
		d.Free()
	}
	if s := v.GetPropertyStr("promptSnippet"); s != nil {
		def.PromptSnippet = s.String()
		s.Free()
	}
	if pg := v.GetPropertyStr("promptGuidelines"); pg != nil && pg.IsObject() {
		// Array of strings.
		pgJSON, _ := pg.JSONStringify()
		_ = json.Unmarshal([]byte(pgJSON), &def.PromptGuidelines)
		pg.Free()
	}
	if p := v.GetPropertyStr("parameters"); p != nil && p.IsObject() {
		// Re-serialize the JS object as JSON schema bytes.
		jstr, _ := p.JSONStringify()
		def.Parameters = json.RawMessage(jstr)
		p.Free()
	}
	if em := v.GetPropertyStr("executionMode"); em != nil {
		def.ExecutionMode = ToolExecutionMode(em.String())
		em.Free()
	}
	// execute(toolCallId, params, signal, onUpdate, ctx)
	if ex := v.GetPropertyStr("execute"); ex.IsFunction() {
		exHandle, err := jrt.pinCallable(ex)
		ex.Free()
		if err != nil {
			return nil, fmt.Errorf("pin execute: %w", err)
		}
		def.Execute = func(ctx context.Context, args json.RawMessage) (any, error) {
			var params any
			if len(args) > 0 {
				_ = json.Unmarshal(args, &params)
			}
			return jrt.CallToolExecute(exHandle, params, map[string]any{"cwd": jrt.cwdLabel()})
		}
	} else {
		ex.Free()
	}
	return def, nil
}

// jsProviderConfigToGo converts a JS provider-config object.
func jsProviderConfigToGo(v *qjs.Value) (*ProviderConfig, error) {
	jstr, err := v.JSONStringify()
	if err != nil {
		return nil, err
	}
	var cfg ProviderConfig
	if err := json.Unmarshal([]byte(jstr), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// jsValueToGo converts a qjs Value to a Go any via JSON. Used for free-form
// values like sendMessage payloads, flag defaults, etc.
func jsValueToGo(v *qjs.Value) any {
	if v == nil {
		return nil
	}
	s, err := v.JSONStringify()
	if err != nil || s == "" || s == "undefined" {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return v.String()
	}
	return out
}

// goToJSValue converts a Go any to a qjs Value via JSON.
func goToJSValue(ctx *qjs.Context, v any) (*qjs.Value, error) {
	if v == nil {
		return ctx.NewUndefined(), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return ctx.ParseJSON(string(b)), nil
}
