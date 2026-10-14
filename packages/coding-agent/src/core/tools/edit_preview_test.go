package tools

// ComputeEditPreviewDiff backs the TUI's edit preview (audit A9): the same
// prepare → parse pipeline as Execute, read-only, surfacing apply errors as
// text so the pending tool box can show the diff (or why it failed) while the
// tool runs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePreviewFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestComputeEditPreviewDiffHappyPath(t *testing.T) {
	path := writePreviewFixture(t, "aaa\nbbb\nccc\n")
	diff, errText := ComputeEditPreviewDiff(map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"oldText": "bbb", "newText": "BBB"},
		},
	}, ".")
	if errText != "" {
		t.Fatalf("unexpected error: %q", errText)
	}
	if !strings.Contains(diff, "-2 bbb") || !strings.Contains(diff, "+2 BBB") {
		t.Errorf("diff missing edit lines, got:\n%s", diff)
	}
}

func TestComputeEditPreviewDiffRepairsStringEdits(t *testing.T) {
	path := writePreviewFixture(t, "hello\n")
	diff, errText := ComputeEditPreviewDiff(map[string]any{
		"path":  path,
		"edits": `[{"oldText":"hello","newText":"world"}]`,
	}, ".")
	if errText != "" {
		t.Fatalf("string edits should be repaired, got error: %q", errText)
	}
	if !strings.Contains(diff, "+1 world") {
		t.Errorf("diff missing applied edit, got:\n%s", diff)
	}
}

func TestComputeEditPreviewDiffLegacyTopLevelFields(t *testing.T) {
	path := writePreviewFixture(t, "legacy\n")
	diff, errText := ComputeEditPreviewDiff(map[string]any{
		"path":    path,
		"oldText": "legacy",
		"newText": "modern",
	}, ".")
	if errText != "" {
		t.Fatalf("legacy fields should work, got error: %q", errText)
	}
	if !strings.Contains(diff, "+1 modern") {
		t.Errorf("diff missing applied edit, got:\n%s", diff)
	}
}

func TestComputeEditPreviewDiffApplyErrorBecomesText(t *testing.T) {
	path := writePreviewFixture(t, "aaa\n")
	_, errText := ComputeEditPreviewDiff(map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"oldText": "not in file", "newText": "x"},
		},
	}, ".")
	if errText == "" {
		t.Fatal("expected apply error text, got none")
	}
}

func TestComputeEditPreviewDiffInvalidArguments(t *testing.T) {
	if _, errText := ComputeEditPreviewDiff(map[string]any{"edits": []any{}}, "."); errText == "" {
		t.Error("missing path should yield an error text")
	}
	if _, errText := ComputeEditPreviewDiff("not-a-map", "."); errText == "" {
		t.Error("unparseable args should yield an error text")
	}
}
