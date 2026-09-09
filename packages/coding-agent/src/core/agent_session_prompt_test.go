package ycode

// agent_session_prompt_test.go covers the Steer/FollowUp extension-command
// guard (audit #13): queueing a registered extension command must return the
// TS error, not panic.

import (
	"testing"

	"ycode/packages/coding-agent/src/core/extensions"
)

func newGuardTestSession(t *testing.T) *AgentSession {
	t.Helper()
	runner := extensions.NewExtensionRunner([]*extensions.Extension{{
		Commands: map[string]*extensions.RegisteredCommand{
			"ext": {Name: "ext", Description: "test command"},
		},
	}}, nil, "", nil, nil)
	return &AgentSession{extensionRunner: runner}
}

func TestSteerExtensionCommandReturnsError(t *testing.T) {
	s := newGuardTestSession(t)

	err := s.Steer("/ext do things", nil)
	if err == nil {
		t.Fatal("Steer of an extension command must return an error")
	}
	want := `Extension command "/ext" cannot be queued. Use prompt() or execute the command when not streaming.`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	if err := s.FollowUp("/ext", nil); err == nil {
		t.Fatal("FollowUp of an extension command must return an error")
	}
}

func TestSteerNonExtensionCommandPasses(t *testing.T) {
	s := newGuardTestSession(t)

	// Guard-level assertions: the fixture's nil Agent embed can't be queued
	// into, so exercise the guard directly for the passing paths.
	if err := s.throwIfExtensionCommand("/builtin"); err != nil {
		t.Errorf("throwIfExtensionCommand(/builtin) = %v, want nil", err)
	}
	if err := s.throwIfExtensionCommand("plain text"); err != nil {
		t.Errorf("throwIfExtensionCommand(plain) = %v, want nil", err)
	}
	if err := s.throwIfExtensionCommand("/ext"); err == nil {
		t.Error("throwIfExtensionCommand(/ext) must error")
	}
}
