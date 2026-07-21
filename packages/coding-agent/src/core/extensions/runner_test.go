package extensions

// runner_test.go guards the BindCore nil-safety fix. The real caller
// (agent_session_extensions.go) builds an ExtensionContextActions with only IsIdle
// + Shutdown set; before the fix, BindCore clobbered the constructor defaults for
// the unset fields (HasPendingMessages, Abort, GetContextUsage, Compact,
// GetSystemPrompt) with nil, and CreateContext then panicked calling
// hasPendingMessagesFn() — crashing every /model selection via
// SetModel → SetThinkingLevel → Emit.

import "testing"

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
