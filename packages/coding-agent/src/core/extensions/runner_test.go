package extensions

// runner_test.go guards the BindCore nil-safety fix. The real caller
// (agent_session_extensions.go) builds an ExtensionContextActions with only IsIdle
// + Shutdown set; before the fix, BindCore clobbered the constructor defaults for
// the unset fields (HasPendingMessages, Abort, GetContextUsage, Compact,
// GetSystemPrompt) with nil, and CreateContext then panicked calling
// hasPendingMessagesFn() — crashing every /model selection via
// SetModel → SetThinkingLevel → Emit.

import (
	"context"
	"testing"
)

func TestBindCoreKeepsDefaultsForNilContextActions(t *testing.T) {
	runner := NewExtensionRunner(nil, &ExtensionRuntime{}, "", nil, nil)

	// Mirrors the real caller: only IsIdle + Shutdown provided, the rest nil.
	runner.BindCore(&ExtensionActions{}, &ExtensionContextActions{
		IsIdle:   func() bool { return true },
		Shutdown: func() {},
	}, &struct {
		RegisterProvider   func(name string, config *ProviderConfig)
		UnregisterProvider func(name string)
	}{})

	// Before the fix this panicked (nil hasPendingMessagesFn). It must now return
	// the constructor default and not panic.
	ctx := runner.CreateContext()
	if v, ok := ctx["hasPendingMessages"].(bool); !ok || v != false {
		t.Errorf("hasPendingMessages = %v, want false (default preserved)", ctx["hasPendingMessages"])
	}
	if v, ok := ctx["isIdle"].(bool); !ok || v != true {
		t.Errorf("isIdle = %v, want true (provided callback used)", ctx["isIdle"])
	}
}

// Story 42 (C16): binding ALL nine ctx actions must surface each one in
// CreateContext — including model (the old runner.getModel was dead code that
// returned nil unconditionally) and signal (previously absent from the map).
func TestBindCoreFullContextActions(t *testing.T) {
	runner := NewExtensionRunner(nil, &ExtensionRuntime{}, "/proj", "sm", "mr")
	abortFired := false
	sig, cancel := context.WithCancel(context.Background())
	cancel() // aborted state
	cu := &ContextUsage{ContextWindow: 200000}
	runner.BindCore(&ExtensionActions{}, &ExtensionContextActions{
		GetModel:           func() any { return map[string]any{"id": "m1"} },
		GetSignal:          func() context.Context { return sig },
		IsIdle:             func() bool { return false },
		Abort:              func() { abortFired = true },
		HasPendingMessages: func() bool { return true },
		Shutdown:           func() {},
		GetContextUsage:    func() *ContextUsage { return cu },
		Compact:            func(*CompactOptions) error { return nil },
		GetSystemPrompt:    func() string { return "sys prompt" },
	}, &struct {
		RegisterProvider   func(name string, config *ProviderConfig)
		UnregisterProvider func(name string)
	}{})

	ctx := runner.CreateContext()
	if m, ok := ctx["model"].(map[string]any); !ok || m["id"] != "m1" {
		t.Errorf("model = %v, want the GetModel result (dead getModel fixed)", ctx["model"])
	}
	if s, ok := ctx["signal"].(context.Context); !ok || s != sig {
		t.Errorf("signal = %v, want the GetSignal result (new member)", ctx["signal"])
	}
	if v, ok := ctx["isIdle"].(bool); !ok || v != false {
		t.Errorf("isIdle = %v, want false (bound callback)", ctx["isIdle"])
	}
	if v, ok := ctx["hasPendingMessages"].(bool); !ok || v != true {
		t.Errorf("hasPendingMessages = %v, want true (bound callback)", ctx["hasPendingMessages"])
	}
	getCU, ok := ctx["getContextUsage"].(func() *ContextUsage)
	if !ok || getCU() != cu {
		t.Errorf("getContextUsage = %T, want the bound func returning cu", ctx["getContextUsage"])
	}
	getSP, ok := ctx["getSystemPrompt"].(func() string)
	if !ok || getSP() != "sys prompt" {
		t.Errorf("getSystemPrompt = %T, want bound func", ctx["getSystemPrompt"])
	}
	ctx["abort"].(func())()
	if !abortFired {
		t.Error("abort not routed to contextActions.Abort")
	}
	_ = cancel
}

// GetModel/GetSignal left nil must keep the constructor defaults (nil model,
// nil signal) without panicking — the nil-clobber guards must cover them too.
func TestBindCoreNilModelAndSignalKeepDefaults(t *testing.T) {
	runner := NewExtensionRunner(nil, &ExtensionRuntime{}, "", nil, nil)
	runner.BindCore(&ExtensionActions{}, &ExtensionContextActions{
		IsIdle: func() bool { return true },
	}, &struct {
		RegisterProvider   func(name string, config *ProviderConfig)
		UnregisterProvider func(name string)
	}{})

	ctx := runner.CreateContext()
	if ctx["model"] != nil {
		t.Errorf("model = %v, want nil (default preserved)", ctx["model"])
	}
	if ctx["signal"] != nil {
		t.Errorf("signal = %v, want nil (default preserved)", ctx["signal"])
	}
}
