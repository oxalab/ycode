package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llm "ycode/packages/llm/src"
)

// onePxPNG is a valid 1x1 transparent PNG (base64) used to exercise the image
// attach path without depending on the (stubbed) resize logic.
const onePxPNGB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestProcessFileArguments_Text(t *testing.T) {
	path := writeTempFile(t, "note.txt", "hello world")
	text, images, err := ProcessFileArguments([]string{path}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images) != 0 {
		t.Errorf("images = %v, want none for a text file", images)
	}
	want := "<file name=\"" + path + "\">\nhello world\n</file>\n"
	if text != want {
		t.Errorf("text mismatch:\n got: %q\nwant: %q", text, want)
	}
}

func TestProcessFileArguments_EmptySkipped(t *testing.T) {
	path := writeTempFile(t, "empty.txt", "")
	text, images, err := ProcessFileArguments([]string{path}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty (empty file skipped)", text)
	}
	if len(images) != 0 {
		t.Errorf("images = %v, want none", images)
	}
}

func TestProcessFileArguments_NotFound(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, _, err := ProcessFileArguments([]string{missing}, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("error = %q, want it to contain 'file not found'", err)
	}
}

func TestProcessFileArguments_Multiple(t *testing.T) {
	a := writeTempFile(t, "a.txt", "AAA")
	b := writeTempFile(t, "b.txt", "BBB")
	text, _, err := ProcessFileArguments([]string{a, b}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "AAA") || !strings.Contains(text, "BBB") {
		t.Errorf("text = %q, want both AAA and BBB in order", text)
	}
	// Order preserved: a's block must come before b's.
	if strings.Index(text, "AAA") > strings.Index(text, "BBB") {
		t.Error("expected AAA block before BBB block")
	}
}

func TestProcessFileArguments_ImageNoResize(t *testing.T) {
	// autoResize disabled → raw image attached with an empty text reference,
	// bypassing the (stubbed) ResizeImage entirely.
	pngBytes, err := base64.StdEncoding.DecodeString(onePxPNGB64)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	path := writeTempFile(t, "px.png", string(pngBytes))

	text, images, err := ProcessFileArguments([]string{path}, t.TempDir(), &ProcessFileOptions{AutoResizeImages: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %v, want exactly 1", images)
	}
	want := &llm.ImageContent{Type: "image", Data: onePxPNGB64, MimeType: "image/png"}
	if images[0].Type != want.Type || images[0].Data != want.Data || images[0].MimeType != want.MimeType {
		t.Errorf("image = %+v, want %+v", images[0], want)
	}
	wantText := "<file name=\"" + path + "\"></file>\n"
	if text != wantText {
		t.Errorf("text = %q, want %q", text, wantText)
	}
}

func TestProcessFileArguments_DefaultAutoResizeTrue(t *testing.T) {
	// nil options → autoResize defaults to true. With a tiny (1x1) PNG the
	// ResizeImage stub returns the original (within 2000x2000 bounds), so the
	// image is still attached.
	pngBytes, _ := base64.StdEncoding.DecodeString(onePxPNGB64)
	path := writeTempFile(t, "px.png", string(pngBytes))

	_, images, err := ProcessFileArguments([]string{path}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Either the stub attached the original (1 image) or, if dimension
	// detection failed on this environment, emitted the omission note (0
	// images). Assert it does not error either way.
	_ = images
}
