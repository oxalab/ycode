package extensions

// js_property.go — leak-free wrappers for the qjs binding's property/function
// installs. The binding's own Value.SetPropertyStr allocates the property-name
// handle with a raw WASM malloc (Context.NewStringHandle) and NEVER frees it —
// its GetPropertyStr sibling frees the identical handle with a defer. Every
// set thus leaked len(name)+1 bytes of WASM heap, and because the corruption
// threshold of the qjs v0.0.6 heap (the F1-incident family: "flag-default
// property reads silently failed", "later allocations OOB'd") is
// allocation-pattern sensitive, builds sat just below it until any added
// install crossed the line (F2's pi.exec bisect, 2026-11-27: a single extra
// install deterministically corrupted the NEXT factory compile). These
// wrappers make the exact same host call as the binding — identical
// reference semantics, the F1 ownership contract is unchanged (the set
// CONSUMES the value's reference; never Free a value after setting it) —
// plus the missing name-handle free.

import (
	"github.com/fastschema/qjs"
)

// setPropertyNoLeak is Value.SetPropertyStr with the name handle freed.
func setPropertyNoLeak(obj *qjs.Value, name string, val *qjs.Value) {
	if val == nil {
		return
	}
	ctx := obj.Context()
	nameVal := ctx.NewStringHandle(name)
	defer ctx.FreeHandle(nameVal.Raw())
	obj.Call("JS_SetPropertyStr", obj.Ctx(), obj.Raw(), nameVal.Raw(), val.Raw())
}

// setFuncNoLeak is Context.SetFunc with the name handle freed (the proxy
// value's reference is consumed by the set, as everywhere; the global object
// is the context's cached instance — never freed).
func setFuncNoLeak(ctx *qjs.Context, name string, fn qjs.Function) {
	setPropertyNoLeak(ctx.Global(), name, ctx.Function(fn))
}

// setAsyncFuncNoLeak is Context.SetAsyncFunc with the name handle freed.
func setAsyncFuncNoLeak(ctx *qjs.Context, name string, fn qjs.AsyncFunction) {
	setPropertyNoLeak(ctx.Global(), name, ctx.Function(func(this *qjs.This) (*qjs.Value, error) {
		fn(this)
		return ctx.NewUndefined(), nil
	}, true))
}
