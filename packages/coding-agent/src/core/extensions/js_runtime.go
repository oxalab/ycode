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

	// toolCtx is the late-bound provider for the runner ctx map handed to JS
	// tool/command/shortcut callables. The session wires it in buildRuntime —
	// after the ExtensionRunner exists — mirroring TS's
	// ctxFactory = () => runner.createContext(): the map is resolved at
	// execute time, so later BindCore updates are reflected. nil (pre-bind, or
	// in unit tests that call Definition.Execute directly) falls back to
	// {"cwd": cwd} — the same no-op-default shape TS's runner uses.
	toolCtx func() map[string]any

	// commandCtx is the late-bound provider for the COMMAND-ctx map (TS
	// createCommandContext: the base ctx + the six session-control actions).
	// Only the pi.registerCommand bridge consults it — TS gives command
	// handlers an ExtensionCommandContext while event handlers, tools, and
	// shortcuts get the plain ExtensionContext (types.ts:1066 vs 1079).
	// Wired alongside toolCtx by the session's runtime. nil → toolCtx →
	// {"cwd": cwd}.
	commandCtx func() map[string]any

	// --- F1: deferred action bridge (waitForIdle/newSession/fork/navigateTree/
	// switchSession/reload). The switch actions emit extension events, so they
	// can NEVER run inside the submitting handler's bridge call: qjs forbids
	// Eval re-entrancy (js_runtime.go:124-133) and j.mu is non-reentrant — a
	// nested emit would deadlock on both. Instead the shim returns a native
	// Promise, __piSubmitAction only enqueues, and the dispatcher goroutine
	// executes the request OUTSIDE j.mu (event emits re-enter the runtime via
	// normal lock contention), then settles the promise from a fresh Eval
	// entry (__piCompleteAction; the qjs eval pumps pending jobs across
	// entries — probe-verified 2026-11-25, so `await ctx.newSession()`
	// continuations resume).
	actionQ    []*jsActionRequest
	actionQMu  sync.Mutex
	actionWake chan struct{}
	quit       chan struct{}
	closeOnce  sync.Once

	// actionExec executes one queued action on the dispatcher goroutine.
	// Wired by the session's runtime (the SetToolCtxProvider seam — the
	// executor needs the AgentSessionRuntime/AgentSession, which the
	// extensions package cannot import). nil → every action rejects.
	actionExec func(name string, payload map[string]any, withSessionID int64) (map[string]any, error)

	// lastCompleteErr: the completion eval's error when one fails (normally
	// empty). Kept as a field so tests and diagnostics can surface it — a
	// silently-failing completion would strand the shim promise forever.
	lastCompleteErr string
}

// jsActionRequest is one deferred ctx-action invocation enqueued by the JS
// shim (ctx.newSession() & friends) and executed by the dispatcher goroutine.
type jsActionRequest struct {
	id            int64
	name          string
	payload       map[string]any // the shim's {arg, opts} JSON, parsed
	withSessionID int64          // pinned JS fn for options.withSession (0 = none)
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
	j.actionWake = make(chan struct{}, 1)
	j.quit = make(chan struct{})
	j.startActionDispatcher()
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
	j.closeOnce.Do(func() { close(j.quit) }) // stop the action dispatcher
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
	// __piSubmitAction: the enqueue half of the deferred action bridge. Called
	// from the __piMakeAction shims during a handler's Eval (j.mu held) — it
	// must NEVER block or touch qjs beyond reading its args: enqueue + wake.
	c.SetFunc("__piSubmitAction", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		var id int64
		var name, payload string
		var withSessionID int64
		if len(args) > 0 {
			id = int64(args[0].Int32())
		}
		if len(args) > 1 {
			name = args[1].String()
		}
		if len(args) > 2 {
			payload = args[2].String()
		}
		if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
			withSessionID = int64(args[3].Int32())
		}
		req := &jsActionRequest{id: id, name: name, withSessionID: withSessionID}
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &req.payload); err != nil {
				req.payload = nil
			}
		}
		j.enqueueAction(req)
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
		// --- Deferred action bridge (F1) ---
		// The bridge shims live in a SECOND eval below: appending them to
		// this eval triggered a qjs v0.0.6 eval-shape corruption (flag-default
		// property reads silently failed and later allocations OOB'd — same
		// family as the known .apply WASM bug; this binding's eval path is
		// size/shape-sensitive). A separate, small Eval is stable.
	})();`), qjs.TypeGlobal())
	if err != nil {
		return err
	}
	// Action-bridge globals (F1): ctx.newSession() & friends return native
	// Promises that the Go dispatcher settles later via __piCompleteAction (a
	// FRESH Eval entry — the switch actions emit extension events and can
	// never run inside the submitting handler's Eval; qjs forbids
	// re-entrancy). The eval pumps pending jobs across entries, so
	// "await ctx.newSession()" continuations resume (probe-verified
	// 2026-11-25). A separate, small Eval keeps the two bootstrap scripts
	// independent.
	_, err = j.ctx.Eval("actions.js", qjs.Code(`(function(){
		globalThis.__piPendingActions = {};
		globalThis.__piNextActionId = 1;
		globalThis.__piCompleteAction = function (id, ok, resultJSON, errMsg) {
			var p = globalThis.__piPendingActions[id];
			if (!p) return undefined;
			delete globalThis.__piPendingActions[id];
			if (ok) { p.resolve(JSON.parse(resultJSON)); }
			else { p.reject(new Error(errMsg)); }
			return undefined;
		};
		// __piMakeAction(name) → the ctx.xxx function for one action.
		// Args normalize: (optionsObj) | (stringArg) | (stringArg, optsObj).
		// options.withSession (a function) is pinned and passed by handle —
		// functions don't survive JSON.
		globalThis.__piMakeAction = function (name) {
			return function (a, b) {
				var arg = a;
				var opts = b;
				if (a && typeof a === "object" && b === undefined) { opts = a; arg = null; }
				if (arg === undefined) arg = null;
				if (opts === undefined || opts === null) opts = {};
				var withSessionId = null;
				var cleaned = {};
				for (var k in opts) {
					if (k === "withSession" && typeof opts[k] === "function") {
						withSessionId = __piPin(opts[k]);
					} else {
						cleaned[k] = opts[k];
					}
				}
				var payload = JSON.stringify({ arg: arg, opts: cleaned });
				return new Promise(function (resolve, reject) {
					var id = globalThis.__piNextActionId++;
					globalThis.__piPendingActions[id] = { resolve: resolve, reject: reject };
					__piSubmitAction(id, name, payload, withSessionId);
				});
			};
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
// OWNERSHIP (story 42): this method CONSUMES ctxValue — qjs SetPropertyStr
// takes the value's reference, so freeing the Go wrapper afterward is a
// double-free that corrupts the WASM heap (verified: it OOBs the next
// allocation). The caller must NOT SafeFree ctxValue after handing it here.
//
// Caller holds nothing — this method locks j.mu.
func (j *JSRuntime) CallHandlerWithJSCtx(handlerHandle int64, eventObj any, ctxValue *qjs.Value) (any, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil || j.poisoned {
		// Pre-consume failure: we still own the ref.
		if ctxValue != nil {
			j.safeCall(func() error { ctxValue.Free(); return nil })
		}
		if j.rt == nil {
			return nil, fmt.Errorf("JSRuntime closed")
		}
		return nil, errPoisonedRuntime
	}
	// Stash ctx on global, build a fire expression that references it via
	// globalThis.__jscall_ctx (NOT a bare identifier — qjs doesn't reliably
	// resolve bare identifiers set via SetPropertyStr as globals in all
	// eval scopes; globalThis.X always works).
	g := j.ctx.Global()
	g.SetPropertyStr("__jscall_ctx", ctxValue) // consumes ctxValue's ref
	eventJSON, err := json.Marshal(eventObj)
	if err != nil {
		g.SetPropertyStr("__jscall_ctx", j.ctx.NewUndefined())
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	argsJSON := fmt.Sprintf(`[%s, globalThis.__jscall_ctx]`, string(eventJSON))
	out, err := j.fireJSON(handlerHandle, argsJSON)
	g.SetPropertyStr("__jscall_ctx", j.ctx.NewUndefined()) // releases the ctx ref the property held
	return out, err
}

// SetToolCtxProvider wires the late-bound runner-ctx provider used by the JS
// tool/command/shortcut bridges. Called once by the session's buildRuntime,
// after the ExtensionRunner exists. A nil fn restores the {"cwd"} fallback.
func (j *JSRuntime) SetToolCtxProvider(fn func() map[string]any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.toolCtx = fn
}

// SetCommandCtxProvider wires the late-bound COMMAND-ctx provider (TS
// createCommandContext: base ctx + the six session-control actions). Only the
// pi.registerCommand bridge consults it. Called by the session's runtime; nil
// falls back to the tool provider, then {"cwd"}.
func (j *JSRuntime) SetCommandCtxProvider(fn func() map[string]any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.commandCtx = fn
}

// commandCtxMap resolves the command ctx at call time with the same
// stale-panic recovery as runnerCtxMap.
func (j *JSRuntime) commandCtxMap() (runnerCtx map[string]any) {
	j.mu.Lock()
	fn := j.commandCtx
	j.mu.Unlock()
	if fn == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			runnerCtx = nil
		}
	}()
	return fn()
}

// ctxProviderChain returns the effective ctx map for the command bridge:
// command provider → tool provider → nil (caller applies the {"cwd"} fallback).
func (j *JSRuntime) ctxProviderChain() map[string]any {
	if m := j.commandCtxMap(); m != nil {
		return m
	}
	return j.runnerCtxMap()
}

// actionShim returns a fresh JS function value implementing the named
// deferred action (ctx.newSession() & friends) via the __piMakeAction factory.
// The returned value holds one reference — the caller consumes it via
// SetPropertyStr (the story-42 ownership contract) and must NOT free it.
// Locks j.mu internally; BuildJSContext's call sites do not hold it.
func (j *JSRuntime) actionShim(name string) *qjs.Value {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil || j.poisoned {
		return nil
	}
	var v *qjs.Value
	_ = j.safeCall(func() error {
		var err error
		v, err = j.ctx.Eval("mkaction.js", qjs.Code(fmt.Sprintf(`__piMakeAction(%s)`, mustJSONString(name))), qjs.TypeGlobal())
		return err
	})
	if j.poisoned || v == nil {
		return nil // a poisoned runtime's value is dead; Close reaps it
	}
	return v
}

// SetActionExecutor wires the deferred-action executor (the dispatcher's
// backend). The executor runs OUTSIDE j.mu on the dispatcher goroutine: it
// may block (waitForIdle), emit extension events (session switches — the
// emits re-enter the runtime via normal j.mu contention), and must recover
// its own stale-session panics defensively (dispatchOne also recovers).
// Returns the action result (marshaled to the promise) and any error.
func (j *JSRuntime) SetActionExecutor(fn func(name string, payload map[string]any, withSessionID int64) (map[string]any, error)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.actionExec = fn
}

// getActionExecutor reads the executor under j.mu.
func (j *JSRuntime) getActionExecutor() func(name string, payload map[string]any, withSessionID int64) (map[string]any, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.actionExec
}

// enqueueAction appends a request and wakes the dispatcher. Safe to call from
// inside a qjs Go-callback (never blocks on j.mu — it only takes actionQMu).
func (j *JSRuntime) enqueueAction(req *jsActionRequest) {
	j.actionQMu.Lock()
	j.actionQ = append(j.actionQ, req)
	j.actionQMu.Unlock()
	select {
	case j.actionWake <- struct{}{}:
	default:
	}
}

// popAction dequeues the next request, or ok=false when the queue is empty.
func (j *JSRuntime) popAction() (*jsActionRequest, bool) {
	j.actionQMu.Lock()
	defer j.actionQMu.Unlock()
	if len(j.actionQ) == 0 {
		return nil, false
	}
	req := j.actionQ[0]
	j.actionQ[0] = nil
	j.actionQ = j.actionQ[1:]
	return req, true
}

// startActionDispatcher launches the goroutine that executes queued ctx
// actions. One request at a time (handlers are sequential by design, and two
// concurrent session switches would race the runtime teardown). Exits when
// Close closes j.quit; pending requests are then abandoned.
func (j *JSRuntime) startActionDispatcher() {
	go func() {
		for {
			select {
			case <-j.quit:
				return
			case <-j.actionWake:
			}
			for {
				req, ok := j.popAction()
				if !ok {
					break
				}
				if !j.dispatchAction(req) {
					return
				}
			}
		}
	}()
}

// dispatchAction executes one queued action and settles its promise.
// Returns false when the runtime is dead (closed/poisoned) so the dispatcher
// goroutine can exit.
func (j *JSRuntime) dispatchAction(req *jsActionRequest) bool {
	if j.isDead() {
		return false
	}
	exec := j.getActionExecutor()
	var result map[string]any
	var execErr error
	if exec == nil {
		execErr = fmt.Errorf("no action executor is wired for this JS runtime")
	} else {
		// Run OUTSIDE j.mu. A stale-session panic here must not kill the
		// dispatcher goroutine — recover into a rejection.
		func() {
			defer func() {
				if r := recover(); r != nil {
					execErr = fmt.Errorf("action %s panicked: %v", req.name, r)
				}
			}()
			result, execErr = exec(req.name, req.payload, req.withSessionID)
		}()
	}
	if j.isDead() {
		return false
	}
	j.completeAction(req.id, execErr == nil, result, errMsgForAction(execErr))
	return true
}

// completeAction settles the shim promise from a FRESH Eval entry (holds
// j.mu). The resolve schedules the await continuation as a pending job; the
// eval's job pump resumes it (probe-verified). A missing pending id (runtime
// closed before completion, or the promise dropped) is a no-op.
func (j *JSRuntime) completeAction(id int64, ok bool, result map[string]any, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lastCompleteErr = "entered"
	if j.rt == nil || j.poisoned {
		j.lastCompleteErr = "dead-runtime"
		return
	}
	resJSON := "null" // JS literal: JSON.parse(null) → null
	if ok && result != nil {
		if b, err := json.Marshal(result); err == nil {
			// The JS side does JSON.parse(resultJSON) — the result must cross
			// as a QUOTED string literal, not an inline object literal (which
			// would arrive as an object and stringify to "[object Object]").
			resJSON = mustJSONString(string(b))
		}
	}
	expr := fmt.Sprintf(`globalThis.__piCompleteAction(%d, %v, %s, %s); "done";`, id, ok, resJSON, mustJSONString(errMsg))
	if cErr := j.safeCall(func() error {
		v, err := j.ctx.Eval("action.js", qjs.Code(expr), qjs.TypeGlobal())
		if v != nil {
			v.Free()
		}
		return err
	}); cErr != nil {
		// A failed completion strands the shim promise forever — the error is
		// kept on the runtime (lastCompleteErr) rather than swallowed.
		j.lastCompleteErr = cErr.Error()
	}
}

// errMsgForAction renders an executor error for the promise rejection.
func errMsgForAction(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// isDead reports closed-or-poisoned without locking j.mu from the dispatcher
// (a plain read; Close's rt=nil write is ordered by the j.mu handoff in the
// worst case and this is only a liveness hint).
func (j *JSRuntime) isDead() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.rt == nil || j.poisoned
}

// DispatchPendingActions drains every queued ctx action synchronously (each
// one executed + settled). Production uses the dispatcher goroutine; tests
// call this for determinism instead of racing goroutine scheduling.
func (j *JSRuntime) DispatchPendingActions() {
	for {
		req, ok := j.popAction()
		if !ok {
			return
		}
		if !j.dispatchAction(req) {
			return
		}
	}
}

// runnerCtxMap resolves the runner ctx at call time (TS createContext's
// late-binding), or nil when no provider is wired. The provider is invoked
// outside any qjs call, so a stale-runner panic (CreateContext asserts active)
// is recovered into nil → the caller falls back to {"cwd"}.
func (j *JSRuntime) runnerCtxMap() (runnerCtx map[string]any) {
	j.mu.Lock()
	fn := j.toolCtx
	j.mu.Unlock()
	if fn == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			runnerCtx = nil
		}
	}()
	return fn()
}

// CallToolExecuteWithJSCtx is the ctx-rich form of the tool-execute invoke: it
// passes (toolCallId, params, signal, null, ctx) with the ctx as a pre-built
// JS value (toolCallId/params/signal still cross as JSON). Mirrors
// CallHandlerWithJSCtx; used by the JS tool bridge so execute's 5th argument
// carries the full runner ctx (cwd/model/isIdle/ui/...), which can't survive a
// JSON round-trip.
//
// OWNERSHIP: CONSUMES ctxValue (qjs SetPropertyStr takes the value's
// reference — freeing it afterward is a double-free that corrupts the WASM
// heap). On a dead/poisoned runtime the value is left to Close's reap.
func (j *JSRuntime) CallToolExecuteWithJSCtx(executeHandle int64, toolCallID string, params, signalObj any, ctxValue *qjs.Value) (any, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return nil, fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return nil, errPoisonedRuntime
	}
	// Flat 5-element args array: the head pieces marshal individually and the
	// ctx rides as a live globalThis reference (NOT nested — a marshaled
	// []any{toolCallID, params, signal, nil} would arrive as ONE array arg).
	// Marshal before stashing so a failure leaves us owning ctxValue.
	idJSON, errID := json.Marshal(toolCallID)
	paramsJSON, errParams := json.Marshal(params)
	signalJSON, errSignal := json.Marshal(signalObj)
	if errID != nil || errParams != nil || errSignal != nil {
		j.safeCall(func() error { ctxValue.Free(); return nil })
		return nil, fmt.Errorf("marshal tool args: %v/%v/%v", errID, errParams, errSignal)
	}
	g := j.ctx.Global()
	g.SetPropertyStr("__jscall_ctx", ctxValue) // consumes ctxValue's ref
	argsJSON := fmt.Sprintf(`[%s, %s, %s, null, globalThis.__jscall_ctx]`, idJSON, paramsJSON, signalJSON)
	out, err := j.fireJSON(executeHandle, argsJSON)
	g.SetPropertyStr("__jscall_ctx", j.ctx.NewUndefined()) // releases the ctx ref the property held
	return out, err
}

// CallCommandHandlerWithJSCtx is the ctx-rich form of the command invoke:
// (args, ctx) with the ctx as a pre-built JS value (TS command handlers get
// the ExtensionCommandContext shape).
//
// OWNERSHIP: CONSUMES ctxValue (see CallToolExecuteWithJSCtx).
func (j *JSRuntime) CallCommandHandlerWithJSCtx(handlerHandle int64, args string, ctxValue *qjs.Value) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return errPoisonedRuntime
	}
	g := j.ctx.Global()
	g.SetPropertyStr("__jscall_ctx", ctxValue) // consumes ctxValue's ref
	jsArgs := fmt.Sprintf(`[%s, globalThis.__jscall_ctx]`, mustJSONString(args))
	_, err := j.fireJSON(handlerHandle, jsArgs)
	g.SetPropertyStr("__jscall_ctx", j.ctx.NewUndefined()) // releases the ctx ref the property held
	return err
}

// mustJSONString marshals v to a JSON string literal; on failure returns the
// quoted raw value (args here is a plain string, so this can't realistically
// fail — kept total for safety).
func mustJSONString(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `"` + v + `"`
	}
	return string(b)
}

// CallShortcutHandler invokes a pinned JS shortcut handler with (ctx) — the
// single ExtensionContext argument of the reference signature (types.ts:1146:
// "handler: (ctx: ExtensionContext) => ..."; the old Go bridge passed a
// {"cwd"} event and a null ctx).
//
// OWNERSHIP: CONSUMES ctxValue (see CallToolExecuteWithJSCtx).
func (j *JSRuntime) CallShortcutHandler(handlerHandle int64, ctxValue *qjs.Value) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.rt == nil {
		return fmt.Errorf("JSRuntime closed")
	}
	if j.poisoned {
		return errPoisonedRuntime
	}
	g := j.ctx.Global()
	g.SetPropertyStr("__jscall_ctx", ctxValue) // consumes ctxValue's ref
	_, err := j.fireJSON(handlerHandle, `[globalThis.__jscall_ctx]`)
	g.SetPropertyStr("__jscall_ctx", j.ctx.NewUndefined()) // releases the ctx ref the property held
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
