package extensions

// js_ui_async_test.go verifies the awaitable JS-side ui methods (input, select,
// confirm, editor) actually return Promises that resolve with the values
// produced by the underlying Go ExtensionUIContext functions.

import (
	"testing"

	"github.com/fastschema/qjs"
)

// evalUI builds a JS `ui` object from a fake ExtensionUIContext, assigns it to
// the global `ui`, and returns the context so each test can eval expression
// against it.
func evalUI(t *testing.T, uiCtx ExtensionUIContext) (*JSRuntime, *qjs.Context) {
	t.Helper()
	jrt, err := NewJSRuntime()
	if err != nil {
		t.Fatalf("NewJSRuntime: %v", err)
	}
	ctx := jrt.Context()
	ui := buildJSUI(ctx, &uiCtx)
	ctx.Global().SetPropertyStr("ui", ui)
	return jrt, ctx
}

// awaitPromise evals an expression (with FlagAsync so top-level await works)
// and returns the resolved value's string form.
func awaitPromise(t *testing.T, ctx *qjs.Context, expr string) string {
	t.Helper()
	result, err := ctx.Eval("t.js", qjs.Code(expr), qjs.FlagAsync())
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	defer result.Free()
	return result.String()
}

func TestJSUI_InputResolvesWithString(t *testing.T) {
	var gotTitle, gotPlaceholder string
	var gotTimeoutMs int
	jrt, ctx := evalUI(t, ExtensionUIContext{
		Input: func(title, placeholder string, timeoutMs int) (string, error) {
			gotTitle, gotPlaceholder, gotTimeoutMs = title, placeholder, timeoutMs
			return "alice", nil
		},
	})
	defer jrt.Close()

	got := awaitPromise(t, ctx, `await ui.input("Your name?", "name", {timeout: 5000})`)
	if got != "alice" {
		t.Errorf("ui.input resolved with %q, want \"alice\"", got)
	}
	if gotTitle != "Your name?" {
		t.Errorf("Input title = %q", gotTitle)
	}
	if gotPlaceholder != "name" {
		t.Errorf("Input placeholder = %q", gotPlaceholder)
	}
	if gotTimeoutMs != 5000 {
		t.Errorf("Input timeoutMs = %d, want 5000", gotTimeoutMs)
	}
}

func TestJSUI_InputDefaultArgs(t *testing.T) {
	// Calling with just the title — placeholder and timeout default.
	var gotTitle, gotPlaceholder string
	var gotTimeoutMs int
	jrt, ctx := evalUI(t, ExtensionUIContext{
		Input: func(title, placeholder string, timeoutMs int) (string, error) {
			gotTitle, gotPlaceholder, gotTimeoutMs = title, placeholder, timeoutMs
			return "", nil
		},
	})
	defer jrt.Close()

	_ = awaitPromise(t, ctx, `await ui.input("Just a message")`)
	if gotTitle != "Just a message" {
		t.Errorf("Input title = %q", gotTitle)
	}
	if gotPlaceholder != "" {
		t.Errorf("default placeholder = %q, want \"\"", gotPlaceholder)
	}
	if gotTimeoutMs != 0 {
		t.Errorf("default timeoutMs = %d, want 0", gotTimeoutMs)
	}
}

func TestJSUI_SelectResolvesWithIndex(t *testing.T) {
	var gotTitle string
	var gotOptions []string
	var gotDefault int
	jrt, ctx := evalUI(t, ExtensionUIContext{
		Select: func(title string, options []string, defaultIdx, timeoutMs int) (int, error) {
			gotTitle, gotOptions, gotDefault = title, options, defaultIdx
			return 2, nil // user picks the third option
		},
	})
	defer jrt.Close()

	got := awaitPromise(t, ctx, `await ui.select("Pick a color", ["red", "green", "blue"], {defaultIdx: 1})`)
	if got != "2" {
		t.Errorf("ui.select resolved with %q, want \"2\"", got)
	}
	if gotTitle != "Pick a color" {
		t.Errorf("Select title = %q", gotTitle)
	}
	if len(gotOptions) != 3 || gotOptions[0] != "red" || gotOptions[2] != "blue" {
		t.Errorf("Select options = %v, want [red green blue]", gotOptions)
	}
	if gotDefault != 1 {
		t.Errorf("Select defaultIdx = %d, want 1", gotDefault)
	}
}

func TestJSUI_SelectRejectsOnNonArrayArg(t *testing.T) {
	// When Reject is called synchronously inside the body, qjs surfaces the
	// error directly to the Eval caller (rather than producing a rejected
	// Promise). User-facing behavior is still correct: a JS try/catch around
	// the call catches it, same as an async rejection. Verify the error is
	// at least visible somewhere.
	jrt, ctx := evalUI(t, ExtensionUIContext{})
	defer jrt.Close()

	_, err := ctx.Eval("t.js", qjs.Code(`ui.select("Pick", "not an array")`))
	if err == nil {
		t.Error("ui.select with non-array options should error; got nil")
	}
}

func TestJSUI_ConfirmResolvesWithBool(t *testing.T) {
	var gotTitle, gotMessage string
	var gotTimeoutMs int
	jrt, ctx := evalUI(t, ExtensionUIContext{
		Confirm: func(title, message string, timeoutMs int) (bool, error) {
			gotTitle, gotMessage, gotTimeoutMs = title, message, timeoutMs
			return true, nil
		},
	})
	defer jrt.Close()

	got := awaitPromise(t, ctx, `await ui.confirm("Confirm", "Proceed?", {timeout: 3000})`)
	if got != "true" {
		t.Errorf("ui.confirm resolved with %q, want \"true\"", got)
	}
	if gotTitle != "Confirm" {
		t.Errorf("Confirm title = %q", gotTitle)
	}
	if gotMessage != "Proceed?" {
		t.Errorf("Confirm message = %q", gotMessage)
	}
	if gotTimeoutMs != 3000 {
		t.Errorf("Confirm timeoutMs = %d, want 3000", gotTimeoutMs)
	}
}

func TestJSUI_EditorResolvesWithText(t *testing.T) {
	var gotTitle, gotPrefill string
	jrt, ctx := evalUI(t, ExtensionUIContext{
		Editor: func(title, prefill string) (string, error) {
			gotTitle, gotPrefill = title, prefill
			return "edited body", nil
		},
	})
	defer jrt.Close()

	got := awaitPromise(t, ctx, `await ui.editor("Commit message:", "feat: ")`)
	if got != "edited body" {
		t.Errorf("ui.editor resolved with %q, want \"edited body\"", got)
	}
	if gotTitle != "Commit message:" {
		t.Errorf("Editor title = %q", gotTitle)
	}
	if gotPrefill != "feat: " {
		t.Errorf("Editor prefill = %q", gotPrefill)
	}
}

func TestJSUI_AllMethodsExposed(t *testing.T) {
	// Sanity: every documented method exists on the ui object.
	jrt, ctx := evalUI(t, ExtensionUIContext{})
	defer jrt.Close()

	for _, method := range []string{"notify", "status", "input", "select", "confirm", "editor"} {
		has, err := ctx.Eval("t.js", qjs.Code("typeof ui." + method))
		if err != nil {
			t.Fatalf("Eval typeof ui.%s: %v", method, err)
		}
		had := has.String()
		has.Free()
		if had != "function" {
			t.Errorf("ui.%s = %q, want \"function\"", method, had)
		}
	}
}

func TestJSUI_ErrorPropagatesAsRejection(t *testing.T) {
	// If the Go function errors, the synchronous Reject surfaces to the Eval
	// caller. JS code wrapping the call in try/catch would catch it; verify
	// the error is at least visible.
	jrt, ctx := evalUI(t, ExtensionUIContext{
		Input: func(_, _ string, _ int) (string, error) {
			return "", errSentinel
		},
	})
	defer jrt.Close()

	_, err := ctx.Eval("t.js", qjs.Code(`ui.input("anything")`))
	if err == nil {
		t.Error("expected error from failing Input; got nil")
	}
}

func TestJSUI_ErrorCaughtByJSCatch(t *testing.T) {
	// A JS try/catch around the call should swallow the synchronous throw,
	// letting the surrounding code continue. This is the realistic
	// user-facing pattern.
	jrt, ctx := evalUI(t, ExtensionUIContext{
		Input: func(_, _ string, _ int) (string, error) {
			return "", errSentinel
		},
	})
	defer jrt.Close()

	// The try/catch returns "caught" if the throw is caught, "uncaught" if not.
	got := awaitPromise(t, ctx, `
		try { await ui.input("anything"); } catch (e) { "caught"; }
	`)
	if got != "caught" {
		t.Errorf("try/catch did not swallow the throw; got %q", got)
	}
}

var errSentinel = &testErr{"ui unavailable in this context"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
