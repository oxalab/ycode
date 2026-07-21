package extensions

// js_runtime.go owns the qjs (QuickJS-NG/WASM) runtime that executes pi
// extensions. Each JSRuntime is created once per LoadExtensions call and lives
// as long as the extensions do (anchored on LoadExtensionsResult.JSRuntime).
//
// Responsibilities:
//   - Own the *qjs.Runtime / *qjs.Context and free it on Close.
//   - Evaluate a transpiled extension module and return its default export
//     (the factory function).
//   - Store JS-side callables (event handlers, tool execute fns, command/shortcut
//     handlers) as *qjs.Value, pinned so they survive across Go calls.
//   - Invoke those callables from Go, marshaling Go values → JS and the JS
//     return → Go via JSON (the safe, shape-stable bridge across the WASM
//     boundary).

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/fastschema/qjs"
)

// JSRuntime wraps a single qjs runtime/context used to run JS extensions.
type JSRuntime struct {
	mu  sync.Mutex
	rt  *qjs.Runtime
	ctx *qjs.Context

	// cwd is the working directory the extensions were loaded against; it's
	// surfaced to handlers/tools via their ctx objects.
	cwd string

	// holder is a persistent JS object used as a Go-owned function registry:
	// pinCallable stores JS functions here as holder[id] = fn via
	// SetPropertyStr (NOT Eval — Eval re-entrancy from a Go callback deadlocks
	// the qjs runtime). __piFire reads from this same holder.
	holder *qjs.Value

	// poisoned is set when a qjs WASM call panicked (typically after a JS
	// exception left the runtime in an unsafe state). Once poisoned, every
	// operation returns errPoisonedRuntime without touching qjs — the runtime
	// is dead until Close. This prevents a single throwing extension from
	// crashing the host process via the deferred Value.Free() calls that follow.
	poisoned bool
	nextID   int64
}

// errPoisonedRuntime is returned by every operation after the qjs runtime
// entered an unrecoverable state (a WASM panic, usually triggered by a JS
// exception poisoning the handle table). Callers should treat it as fatal
// for this JSRuntime and let the load fail; the host process keeps running.
var errPoisonedRuntime = fmt.Errorf("JS runtime is poisoned (a prior operation panicked; the runtime must be closed and recreated)")

// safeCall runs fn against qjs and recovers from any panic. If the panic
// fires, the runtime is marked poisoned and the recovered value is returned
// as part of the error. Caller MUST hold j.mu (all callers do — every public
// method locks first).
func (j *JSRuntime) safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			j.poisoned = true
			err = fmt.Errorf("qjs panic (runtime poisoned): %v", r)
		}
	}()
	return fn()
}

// NewJSRuntime creates a qjs runtime. The returned JSRuntime must be Closed
// when the extensions it owns are discarded.
func NewJSRuntime() (*JSRuntime, error) {
	j := &JSRuntime{}
	var rt *qjs.Runtime
	if err := j.safeCall(func() error {
		var rerr error
		rt, rerr = qjs.New()
		if rerr != nil {
			return fmt.Errorf("create qjs runtime: %w", rerr)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	j.rt = rt
	j.ctx = rt.Context()
	if err := j.installMinimalShims(); err != nil {
		j.Close()
		return nil, err
	}
	return j, nil
}

// Close releases the qjs runtime. After Close the JSRuntime is unusable.
// The qjs runtime frees ALL values on Close, so we do NOT free individual
// pinned values (that would double-free and panic after a JS exception
// corrupts the handle table). Just reap the runtime. Recover defensively in
// case Close trips on a poisoned runtime.
func (j *JSRuntime) Close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer func() { _ = recover() }()
	if j.rt != nil {
		j.rt.Close()
		j.rt = nil
		j.ctx = nil
	}
	j.poisoned = false
}

// installMinimalShims provides the few globals that pi extensions expect but
// qjs doesn't ship by default: console.log, and the handler registry + invoker.
//
// CRITICAL re-entrancy constraint: qjs is single-threaded and a Go callback
// fired during an Eval CANNOT itself call Eval (it deadlocks). So the pin
// mechanism is split:
//   - The Go-owned holder object (j.holder) is built once here. Go callbacks
//     store JS functions into it via SetPropertyStr (a non-Eval WASM call,
//     re-entrancy-safe).
//   - __piFire(id, ...args) reads from this same holder by id and invokes.
//   - __piPin(fn) (used by the EvalModule CJS wrapper, called from pure JS
//     during module load, NOT from a Go callback) stores via the holder too
//     so both paths share one registry.
func (j *JSRuntime) installMinimalShims() error {
	c := j.ctx
	c.SetFunc("__js_log", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, a.String())
		}
		if jsLogger != nil {
			jsLogger(strings.Join(parts, " "))
		}
		return c.NewUndefined(), nil
	})
	// Build the Go-owned holder object. We keep a Go reference (j.holder) so
	// pinCallable can SetPropertyStr on it directly without Eval.
	j.holder = c.NewObject()
	g := c.Global()
	g.SetPropertyStr("__piHolder", j.holder)

	_, err := j.ctx.Eval("shims.js", qjs.Code(`(function(){
		var c = { log: __js_log, info: __js_log, warn: __js_log, error: __js_log, debug: __js_log };
		if (typeof globalThis !== "undefined") { globalThis.console = c; }
		// Shared id counter for both __piPin (JS-side) and Go-side pinCallable.
		if (!globalThis.__piNextId) globalThis.__piNextId = 1;
		globalThis.__piPin = function (fn) {
			var id = globalThis.__piNextId++;
			globalThis.__piHolder[id] = fn;
			return id;
		};
		// __piFire(id, argsArray) — takes the handle and a pre-built array of
		// positional args. We avoid fn.apply (qjs v0.0.6 has a WASM OOB bug
		// with .apply in certain eval shapes) by switching on arity, since
		// every call site uses a known fixed arg count (handlers=2, execute=5,
		// commands=2). The switch covers 0-8 args which is more than enough.
		globalThis.__piFire = function (id, argsArray) {
			var fn = globalThis.__piHolder[id];
			if (typeof fn !== "function") return undefined;
			var a = argsArray || [];
			switch (a.length) {
				case 0: return fn();
				case 1: return fn(a[0]);
				case 2: return fn(a[0], a[1]);
				case 3: return fn(a[0], a[1], a[2]);
				case 4: return fn(a[0], a[1], a[2], a[3]);
				case 5: return fn(a[0], a[1], a[2], a[3], a[4]);
				case 6: return fn(a[0], a[1], a[2], a[3], a[4], a[5]);
				case 7: return fn(a[0], a[1], a[2], a[3], a[4], a[5], a[6]);
				case 8: return fn(a[0], a[1], a[2], a[3], a[4], a[5], a[6], a[7]);
				default: return fn.apply(null, a);
			}
		};
	})();`), qjs.TypeGlobal())
	return err
}

// jsLogger is the optional Go-side sink for console.* output from extensions.
var jsLogger func(msg string)

// SetJSLogger installs a sink for console.log/info/warn/error from extensions.
func SetJSLogger(fn func(msg string)) { jsLogger = fn }

// pinCallable pins a JS function value in the JS-side registry via __piPin
// and returns the id Go uses to reference it. The function survives until
// Close (the registry lives for the runtime's lifetime). Caller holds nothing.
// pinCallable stores a JS function value in the Go-owned holder under a fresh
// id and returns the id. Go callbacks (fired from inside InvokeFactory's Eval,
// which already holds j.mu) invoke this when an extension registers a
// handler/tool/command.
//
// CRITICAL: this method assumes the caller already holds j.mu — it does NOT
// lock (locking would self-deadlock, since InvokeFactory holds the lock while
// the factory's Eval fires these callbacks). The id returned is what
// CallHandler/CallToolExecute/etc. use to fire the function later via __piFire.
//
// Uses SetPropertyStr (non-Eval) only — Eval re-entrancy from a Go callback
// deadlocks qjs, so this path MUST NOT call Eval.
func (j *JSRuntime) pinCallable(fn *qjs.Value) (int64, error) {
	if j.rt == nil {
		return 0, fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return 0, errPoisonedRuntime
	}
	var id int64
	if err := j.safeCall(func() error {
		// Read-and-increment the JS-side counter via GetPropertyStr/String.
		g := j.ctx.Global()
		cur := g.GetPropertyStr("__piNextId")
		curStr := cur.String()
		cur.Free()
		var curInt int64
		_, _ = fmt.Sscanf(curStr, "%d", &curInt)
		if curInt <= 0 {
			curInt = 1 // first pin
		}
		id = curInt
		// Write the incremented counter back.
		nextVal := j.ctx.NewInt64(curInt + 1)
		g.SetPropertyStr("__piNextId", nextVal)
		nextVal.Free()
		// Store fn in the holder under its id key.
		j.holder.SetPropertyStr(fmt.Sprintf("%d", id), fn)
		return nil
	}); err != nil {
		return 0, fmt.Errorf("pin callable: %w", err)
	}
	j.nextID = id
	return id, nil
}

// EvalModule evaluates a (transpiled, CJS) extension module and returns the
// factory function (its default export) as a pinned callable handle.
//
// The CJS source is wrapped so its `module.exports.default` (or just
// `module.exports` if it's a function) is extracted, then pinned via __piPin.
func (j *JSRuntime) EvalModule(absPath string, js []byte) (factoryHandle int64, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return 0, fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return 0, errPoisonedRuntime
	}
	// CJS shim: provide module/exports, run the source, pin the default export.
	// __piPin returns the id, which becomes this function's return value.
	wrapped := fmt.Sprintf(`(function(){
		var module = { exports: {} };
		var exports = module.exports;
		%s
		var fn = (module.exports && typeof module.exports.default === "function")
			? module.exports.default
			: (typeof module.exports === "function" ? module.exports : null);
		if (typeof fn !== "function") return -1;
		return __piPin(fn);
	})()`, string(js))

	var v *qjs.Value
	if err := j.safeCall(func() error {
		var evalErr error
		v, evalErr = j.ctx.Eval(filepath_base(absPath), qjs.Code(wrapped), qjs.TypeGlobal())
		return evalErr
	}); err != nil {
		return 0, fmt.Errorf("eval %s: %w", absPath, err)
	}
	if v == nil {
		return 0, fmt.Errorf("extension %s: no result", absPath)
	}
	var handle int64
	if err := j.safeCall(func() error {
		handle = int64(v.Int32())
		v.Free()
		return nil
	}); err != nil {
		return 0, fmt.Errorf("eval %s: %w", absPath, err)
	}
	if handle < 0 {
		return 0, fmt.Errorf("extension %s: default export is not a function", absPath)
	}
	j.nextID = handle
	return handle, nil
}

// InvokeFactory calls the pinned factory function, passing it a JS `pi` object
// (built by js_api.go). The factory is invoked via __piFire.
func (j *JSRuntime) InvokeFactory(factoryHandle int64, piValue *qjs.Value) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return errPoisonedRuntime
	}
	// Stash pi on global, then call the factory directly via the holder:
	//   __piHolder[handle](__pi)
	// We do NOT use __piFire here because __piFire's signature is (id, argsArray)
	// for handler dispatch; the factory takes one positional arg (the pi object),
	// so a direct holder lookup + call is the correct shape.
	var v *qjs.Value
	var evalErr error
	if err := j.safeCall(func() error {
		g := j.ctx.Global()
		g.SetPropertyStr("__pi", piValue)
		v, evalErr = j.ctx.Eval("firefactory.js",
			qjs.Code(fmt.Sprintf("__piHolder[%d](__pi)", factoryHandle)),
			qjs.TypeGlobal())
		g.SetPropertyStr("__pi", j.ctx.NewUndefined())
		return evalErr
	}); err != nil {
		return fmt.Errorf("invoke factory: %w", err)
	}
	if v != nil {
		var msg string
		var isError bool
		if err := j.safeCall(func() error {
			isError = v.IsError()
			if isError {
				msg = v.String()
			}
			v.Free()
			return nil
		}); err != nil {
			return fmt.Errorf("invoke factory: %w", err)
		}
		if isError {
			if msg == "" {
				msg = "factory threw"
			}
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// fireJSON invokes a pinned callable, passing the args as a JSON array which
// the JS side spreads into positional arguments. Returns the raw JS return as
// Go any (via JSON round-trip), or nil if the callable returned undefined.
// Caller holds j.mu.
func (j *JSRuntime) fireJSON(handle int64, argsJSON string) (any, error) {
	if j.poisoned {
		return nil, errPoisonedRuntime
	}
	// Parse the args JSON onto globalThis.__fireArgs, then call
	// __piFire(handle, __fireArgs) and JSON.stringify the return. Stashing
	// args on a global avoids building a variadic .apply expression inline
	// (qjs v0.0.6 has a WASM OOB bug with fn.apply across eval boundaries).
	expr := fmt.Sprintf(`(function(){
		globalThis.__fireArgs = %s;
		var ret = __piFire(%d, globalThis.__fireArgs);
		globalThis.__fireArgs = undefined;
		return JSON.stringify(ret);
	})()`, argsJSON, handle)
	var v *qjs.Value
	var evalErr error
	if err := j.safeCall(func() error {
		v, evalErr = j.ctx.Eval("fire.js", qjs.Code(expr), qjs.TypeGlobal())
		return evalErr
	}); err != nil {
		return nil, fmt.Errorf("fire: %w", err)
	}
	if v == nil {
		return nil, nil
	}
	// All subsequent v.* calls happen under safeCall so a poisoned runtime
	// can't crash us during result extraction.
	var isError bool
	var s string
	if err := j.safeCall(func() error {
		defer func() { _ = recover() }() // belt-and-suspenders for v.Free
		isError = v.IsError()
		if isError {
			s = v.String()
		} else {
			s = v.String()
		}
		v.Free()
		return nil
	}); err != nil {
		return nil, fmt.Errorf("fire: %w", err)
	}
	if isError {
		if s == "" {
			s = "JS handler threw"
		}
		return nil, fmt.Errorf("%s", s)
	}
	if s == "undefined" || s == "" {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return s, nil // fall back to the raw string
	}
	return out, nil
}

// CallHandler invokes a pinned JS handler with (eventObj, ctxObj) — the two
// positional args every pi event handler receives. The JS handler may return
// a result object (e.g. `{block: true}` for tool_call) or void/undefined.
// The return is marshaled to `any` via JSON.
func (j *JSRuntime) CallHandler(handlerHandle int64, eventObj, ctxObj any) (any, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return nil, fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return nil, errPoisonedRuntime
	}
	args, err := json.Marshal([]any{eventObj, ctxObj})
	if err != nil {
		return nil, fmt.Errorf("marshal handler args: %w", err)
	}
	return j.fireJSON(handlerHandle, string(args))
}

// CallHandlerWithJSCtx is like CallHandler but takes a pre-built JS *qjs.Value
// for the ctx argument (the event is still JSON-marshaled). Used by js_api.go
// when the ctx needs to carry callable JS methods (e.g. ctx.ui.notify) that
// can't survive a JSON round-trip.
//
// Caller holds nothing — this method locks j.mu. The ctxValue must outlive
// the call (it's read inside the eval); the caller frees it.
func (j *JSRuntime) CallHandlerWithJSCtx(handlerHandle int64, eventObj any, ctxValue *qjs.Value) (any, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return nil, fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return nil, errPoisonedRuntime
	}
	// Stash ctx on global, build a fire expression that references it via
	// globalThis.__jscall_ctx (NOT a bare identifier — qjs doesn't reliably
	// resolve bare identifiers set via SetPropertyStr as globals in all
	// eval scopes; globalThis.X always works).
	g := j.ctx.Global()
	g.SetPropertyStr("__jscall_ctx", ctxValue)
	eventJSON, err := json.Marshal(eventObj)
	if err != nil {
		g.SetPropertyStr("__jscall_ctx", j.ctx.NewUndefined())
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	argsJSON := fmt.Sprintf(`[%s, globalThis.__jscall_ctx]`, string(eventJSON))
	out, err := j.fireJSON(handlerHandle, argsJSON)
	g.SetPropertyStr("__jscall_ctx", j.ctx.NewUndefined())
	return out, err
}

// CallToolExecute invokes a pinned JS tool-execute callable with
// (toolCallId, params, signal, onUpdate, ctx) — the 5 positional args of the
// reference execute signature. signal/onUpdate are passed as null in v1
// (handlers that ignore trailing args work fine, which covers the examples).
// The return must be an AgentToolResult-shape object; it's marshaled to any.
func (j *JSRuntime) CallToolExecute(executeHandle int64, params any, ctxObj any) (any, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return nil, fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return nil, errPoisonedRuntime
	}
	args, err := json.Marshal([]any{"", params, nil, nil, ctxObj})
	if err != nil {
		return nil, fmt.Errorf("marshal tool args: %w", err)
	}
	return j.fireJSON(executeHandle, string(args))
}

// CallCommandHandler invokes a pinned JS command handler with (args, ctx).
func (j *JSRuntime) CallCommandHandler(handlerHandle int64, args string, ctxObj any) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return errPoisonedRuntime
	}
	jsArgs, err := json.Marshal([]any{args, ctxObj})
	if err != nil {
		return fmt.Errorf("marshal command args: %w", err)
	}
	_, err = j.fireJSON(handlerHandle, string(jsArgs))
	return err
}

// NewObject builds an empty JS object on this runtime's context. Used by
// js_api.go to build the `pi` object and its methods.
func (j *JSRuntime) NewObject() *qjs.Value {
	return j.ctx.NewObject()
}

// SafeFree releases a qjs Value, recovering from any panic. Callers that hold
// raw *qjs.Value across operations that can poison the runtime (factory
// invocation, handler dispatch) MUST free those values via SafeFree rather
// than v.Free() directly — otherwise a poisoned runtime will crash the host
// on the deferred free. SafeFree is a no-op on a poisoned runtime.
func (j *JSRuntime) SafeFree(v *qjs.Value) {
	if v == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.poisoned || j.rt == nil {
		return // runtime is dead; values were reaped by qjs already
	}
	_ = j.safeCall(func() error { v.Free(); return nil })
}

// Context exposes the underlying *qjs.Context for js_api.go to install
// Go-backed functions on the global `pi` object.
func (j *JSRuntime) Context() *qjs.Context {
	return j.ctx
}

// IsPoisoned reports whether the runtime has entered the unrecoverable state
// (a WASM panic from a JS exception). Once true, the JSRuntime must be Closed
// and recreated; no operations will succeed.
func (j *JSRuntime) IsPoisoned() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.poisoned
}

// SetCWD records the working directory the extensions were loaded against, so
// handlers/tools receive it via their ctx. Called once by the loader before
// any factory is invoked.
func (j *JSRuntime) SetCWD(cwd string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cwd = cwd
}

// cwdLabel returns the working directory, read-locked.
func (j *JSRuntime) cwdLabel() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cwd
}

// filepath_base is filepath.Base without importing path/filepath (kept out to
// avoid an extra import in this file). The qjs Eval uses it only as a label.
func filepath_base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}
