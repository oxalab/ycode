package ycode

// settings_manager_test.go covers the images settings accessors (audit #27):
// the default, loading a TS-written settings.json ("images" key — the tag was
// wrongly "imageSettings"), and setter persistence.

import (
	"os"
	"path/filepath"
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
