package tui

import (
	"reflect"
	"testing"
)

func TestKeybindingsRebindDoesNotEvict(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		"tui.input.submit": {"enter", "ctrl+enter"},
	})
	if got := m.GetKeys("tui.input.submit"); !reflect.DeepEqual(got, []KeyID{"enter", "ctrl+enter"}) {
		t.Errorf("submit=%v", got)
	}
	if got := m.GetKeys("tui.select.confirm"); !reflect.DeepEqual(got, []KeyID{"enter"}) {
		t.Errorf("select.confirm=%v (default should be kept)", got)
	}
}

func TestKeybindingsRebindSameKeyKeepsDefaults(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		"tui.select.up": {"up", "ctrl+p"},
	})
	if got := m.GetKeys("tui.select.up"); !reflect.DeepEqual(got, []KeyID{"up", "ctrl+p"}) {
		t.Errorf("select.up=%v", got)
	}
	if got := m.GetKeys("tui.editor.cursorUp"); !reflect.DeepEqual(got, []KeyID{"up"}) {
		t.Errorf("cursorUp=%v (default kept)", got)
	}
}

func TestKeybindingsReportsUserConflicts(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		"tui.input.submit":   {"ctrl+x"},
		"tui.select.confirm": {"ctrl+x"},
	})
	want := []KeybindingConflict{
		{Key: "ctrl+x", Keybindings: []Keybinding{"tui.input.submit", "tui.select.confirm"}},
	}
	if got := m.GetConflicts(); !reflect.DeepEqual(got, want) {
		t.Errorf("conflicts=%v want %v", got, want)
	}
	if got := m.GetKeys("tui.editor.cursorLeft"); !reflect.DeepEqual(got, []KeyID{"left", "ctrl+b"}) {
		t.Errorf("cursorLeft=%v (unrelated default kept)", got)
	}
}

func TestKeybindingsMatchesDelegates(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, nil)
	if !m.Matches("\x1b[A", "tui.editor.cursorUp") {
		t.Error("up arrow should match cursorUp")
	}
	if !m.Matches("\r", "tui.input.submit") {
		t.Error("enter should match submit")
	}
	if m.Matches("x", "tui.input.submit") {
		t.Error("x should not match submit")
	}
}

func TestKeybindingsUnknownUserBindingIgnored(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		"tui.bogus.action": {"up"},
	})
	if len(m.GetConflicts()) != 0 {
		t.Errorf("unknown binding should not create conflicts: %v", m.GetConflicts())
	}
}
