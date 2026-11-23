package ycode

// settings_manager_test.go covers the images settings accessors (audit #27):
// the default, loading a TS-written settings.json ("images" key — the tag was
// wrongly "imageSettings"), and setter persistence.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetImageAutoResizeDefaultsTrue(t *testing.T) {
	sm := NewSettingsManager(NewFileSettingsStorage(t.TempDir(), t.TempDir()))
	if !sm.GetImageAutoResize() {
		t.Fatal("default images.autoResize = false, want true")
	}
}

func TestGetImageAutoResizeReadsTSImagesKey(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"),
		[]byte(`{"images": {"autoResize": false}}`), 0644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(NewFileSettingsStorage(cwd, agentDir))
	if sm.GetImageAutoResize() {
		t.Fatal(`autoResize from {"images":{"autoResize":false}} = true, want false`)
	}
}

func TestSetImageAutoResizePersists(t *testing.T) {
	sm := NewSettingsManager(NewFileSettingsStorage(t.TempDir(), t.TempDir()))

	sm.SetImageAutoResize(false)
	if sm.GetImageAutoResize() {
		t.Fatal("after SetImageAutoResize(false), GetImageAutoResize = true")
	}
}

// SetEnabledModels persists the scoped-models set under "enabledModels" and a
// clear (nil/empty) drops the key — matching the reference setEnabledModels
// (all models enabled = undefined = no filter). The startup read path
// (--models ?? GetEnabledModels → ResolveModelScope) restores it.
func TestSetEnabledModelsPersistsAndClears(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sm := NewSettingsManager(NewFileSettingsStorage(cwd, agentDir))

	sm.SetEnabledModels([]string{"anthropic/claude-haiku-4.5", "openai/gpt-4.1"})
	if got := sm.GetEnabledModels(); len(got) != 2 || got[0] != "anthropic/claude-haiku-4.5" {
		t.Fatalf("GetEnabledModels = %v, want 2 patterns", got)
	}
	sm.Flush() // save() enqueues the file write

	// Survives a fresh manager (real settings.json round-trip).
	sm2 := NewSettingsManager(NewFileSettingsStorage(cwd, agentDir))
	if got := sm2.GetEnabledModels(); len(got) != 2 {
		t.Fatalf("persisted GetEnabledModels = %v, want 2 patterns", got)
	}

	// All-enabled clear: key dropped from the JSON file (omitempty + nil).
	sm2.SetEnabledModels(nil)
	sm2.Flush()
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "enabledModels") {
		t.Fatalf("settings.json still contains enabledModels after clear: %s", data)
	}

	// Empty slice clears too.
	sm2.SetEnabledModels([]string{"a/b"})
	sm2.SetEnabledModels([]string{})
	sm2.Flush() // drain the async write queue before TempDir cleanup
	if got := sm2.GetEnabledModels(); len(got) != 0 {
		t.Fatalf("GetEnabledModels after empty-slice clear = %v, want none", got)
	}
}
