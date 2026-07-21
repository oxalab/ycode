package extensions

// js_runtime_testsupport.go provides helpers used by js_runtime_test.go.
// They build a minimal JS-side `pi` object and track handlers registered via
// pi.on(), so tests can exercise the runtime without the full API surface
// from js_api.go (which is tested separately).
//
// This file is compiled into the package but only referenced from tests.

import (
	"fmt"

	"github.com/fastschema/qjs"
)

// testRegisteredHandlers tracks handler ids registered via the fake pi.on(),
// keyed by event name. Populated by the JS sink.
var testRegisteredHandlers = map[string][]int64{}

// buildFakePi builds a JS object with an `on(event, handler)` method that
// pins the handler via __piPin and records the id in
// testRegisteredHandlers. Returns the object (must be Freed by caller).
func (j *JSRuntime) buildFakePi() (*qjs.Value, error) {
	// Reset tracker for a clean test.
	testRegisteredHandlers = map[string][]int64{}

	// Install a Go function __test_pi_on(eventName, handler) that pins the
	// handler via the runtime's registry and records the id.
	c := j.ctx
	c.SetFunc("__test_pi_on", func(t *qjs.This) (*qjs.Value, error) {
		args := t.Args()
		if len(args) < 2 {
			return c.NewUndefined(), nil
		}
		eventName := args[0].String()
		// Pin the handler function via __piPin and capture the returned id.
		g := c.Global()
		g.SetPropertyStr("__toPin", args[1])
		idVal, err := c.Eval("pin.js", qjs.Code("__piPin(__toPin)"), qjs.TypeGlobal())
		g.SetPropertyStr("__toPin", c.NewUndefined())
		if err != nil || idVal == nil {
			return c.NewUndefined(), nil
		}
		id := int64(idVal.Int32())
		idVal.Free()
		testRegisteredHandlers[eventName] = append(testRegisteredHandlers[eventName], id)
		return c.NewUndefined(), nil
	})

	// Build { on: __test_pi_on } as the pi object.
	pi, err := c.Eval("fakepi.js", qjs.Code("(function(){ return { on: __test_pi_on }; })()"), qjs.TypeGlobal())
	if err != nil {
		return nil, fmt.Errorf("build fake pi: %w", err)
	}
	if pi == nil {
		return nil, fmt.Errorf("build fake pi: nil result")
	}
	return pi, nil
}

// lastRegisteredHandler returns the handle of the most-recently-registered
// handler across all event types, for tests that register exactly one.
func (j *JSRuntime) lastRegisteredHandler() (int64, error) {
	for _, handles := range testRegisteredHandlers {
		if len(handles) > 0 {
			return handles[len(handles)-1], nil
		}
	}
	return 0, fmt.Errorf("no handlers registered")
}
