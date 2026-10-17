package tools

// Audit B3: the read tool's access gate must probe readability (the Go
// equivalent of TS fsAccess(path, R_OK)) and surface the raw fs error — the
// old os.Stat + "cannot read file: %s" hid whether the file was missing or
// unreadable, which the reference's model-visible error always shows.

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

func TestReadAccessOpProbesReadability(t *testing.T) {
	ops := defaultReadOps{}
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ops.Access(file); err != nil {
		t.Errorf("readable file should pass the access gate: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing.txt")
	err := ops.Access(missing)
	if err == nil {
		t.Fatal("missing file must fail the access gate")
	}
	// The raw fs error carries the path (TS model-visible parity: the message
	// distinguishes not-found from other failures).
	if !strings.Contains(err.Error(), "missing.txt") {
		t.Errorf("access error should mention the path, got: %v", err)
	}
}

func TestReadExecuteMissingFileSurfacesFsError(t *testing.T) {
	def := CreateReadToolDefinition(t.TempDir(), nil)
	_, err := def.Execute(context.Background(), json.RawMessage(`{"path":"gone.txt"}`))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	msg := err.Error()
	if strings.Contains(msg, "cannot read file") {
		t.Errorf("generic wrapper is gone; got: %q", msg)
	}
	if !strings.Contains(msg, "gone.txt") {
		t.Errorf("error should carry the real fs message with the path, got: %q", msg)
	}
}

func TestReadExecuteReadsSmallTextFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(file, []byte("hello b3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := CreateReadToolDefinition(dir, nil)
	res, err := def.Execute(context.Background(), json.RawMessage(`{"path":"hello.txt"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	toolRes, ok := res.(*ToolExecuteResult)
	if !ok || len(toolRes.Content) == 0 {
		t.Fatalf("unexpected result shape: %T %+v", res, res)
	}
	tc, ok := toolRes.Content[0].(*llm.TextContent)
	if !ok || !strings.Contains(tc.Text, "hello b3") {
		t.Errorf("file text missing from result: %+v", toolRes.Content[0])
	}
}

func TestReadExecuteDirectoryPathErrors(t *testing.T) {
	dir := t.TempDir()
	def := CreateReadToolDefinition(dir, nil)
	_, err := def.Execute(context.Background(), json.RawMessage(`{"path":"."}`))
	if err == nil {
		t.Fatal("reading a directory should error")
	}
	if !strings.Contains(err.Error(), filepath.Base(dir)) && !strings.Contains(err.Error(), ".") {
		t.Errorf("error should be informative, got: %q", err.Error())
	}
}

// --- B4/B5: image notes (reference read.ts:90-95, 248-277) ---

// tinyPNG is a valid 1x1 transparent PNG.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// pngWithDims rewrites the IHDR width/height of tinyPNG (CRC recomputed) so
// image.DecodeConfig reports the wanted dimensions without shipping huge
// fixtures — used to trigger the resize-failure degrade path.
func pngWithDims(t *testing.T, width, height int) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatalf("decode tinyPNG: %v", err)
	}
	binary.BigEndian.PutUint32(raw[16:20], uint32(width))
	binary.BigEndian.PutUint32(raw[20:24], uint32(height))
	// PNG chunk layout: [len:8-12][type:12-16][data:16-29 (13 bytes)][crc:29-33];
	// the CRC covers type+data.
	sum := crc32.ChecksumIEEE(raw[12:29])
	binary.BigEndian.PutUint32(raw[29:33], sum)
	return raw
}

func visionModel(withImages bool) llm.Model[llm.Api] {
	input := []llm.InputType{llm.InputText}
	if withImages {
		input = append(input, llm.InputImage)
	}
	return llm.Model[llm.Api]{ID: "model-x", Provider: "test", Input: input}
}

func TestGetNonVisionImageNote(t *testing.T) {
	if got := getNonVisionImageNote(nil); got != "" {
		t.Errorf("nil model must stay silent, got %q", got)
	}
	zero := llm.Model[llm.Api]{}
	if got := getNonVisionImageNote(&zero); got != "" {
		t.Errorf("zero-valued (unknown) model must stay silent, got %q", got)
	}
	if got := getNonVisionImageNote(&llm.Model[llm.Api]{ID: "m", Input: []llm.InputType{llm.InputText, llm.InputImage}}); got != "" {
		t.Errorf("image-capable model must stay silent, got %q", got)
	}
	want := "[Current model does not support images. The image will be omitted from this request.]"
	textOnly := visionModel(false)
	if got := getNonVisionImageNote(&textOnly); got != want {
		t.Errorf("text-only model note = %q, want %q", got, want)
	}
}

func readImageFixture(t *testing.T, width, height int) (dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), pngWithDims(t, width, height), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadExecuteContextNonVisionNote(t *testing.T) {
	dir := readImageFixture(t, 1, 1)
	def := CreateReadToolDefinition(dir, nil)
	args := json.RawMessage(`{"path":"pic.png"}`)

	res, err := def.ExecuteContext(context.Background(), args, extensions.ExtensionContext{Model: visionModel(false)})
	if err != nil {
		t.Fatalf("ExecuteContext (text-only model): %v", err)
	}
	toolRes := res.(*ToolExecuteResult)
	tc := toolRes.Content[0].(*llm.TextContent)
	if !strings.Contains(tc.Text, "Read image file [image/png]") {
		t.Errorf("mime header line missing, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "does not support images") {
		t.Errorf("non-vision note missing, got %q", tc.Text)
	}

	res, err = def.ExecuteContext(context.Background(), args, extensions.ExtensionContext{Model: visionModel(true)})
	if err != nil {
		t.Fatalf("ExecuteContext (vision model): %v", err)
	}
	toolRes = res.(*ToolExecuteResult)
	tc = toolRes.Content[0].(*llm.TextContent)
	if strings.Contains(tc.Text, "does not support images") {
		t.Errorf("vision model must not get the note, got %q", tc.Text)
	}
	if len(toolRes.Content) != 2 {
		t.Fatalf("vision read should attach the image block, got %d blocks", len(toolRes.Content))
	}
	if _, ok := toolRes.Content[1].(*llm.ImageContent); !ok {
		t.Errorf("second block should be an image, got %T", toolRes.Content[1])
	}
}

func TestReadImageOmittedDegradeCarriesMimeLine(t *testing.T) {
	// 3000x3000 exceeds the 2000x2000 resize cap; ResizeImage can't downscale,
	// so the tool degrades to the text-only note (B4: the mime line must lead).
	dir := readImageFixture(t, 3000, 3000)
	def := CreateReadToolDefinition(dir, nil)

	res, err := def.ExecuteContext(context.Background(), json.RawMessage(`{"path":"pic.png"}`), extensions.ExtensionContext{Model: visionModel(false)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	toolRes := res.(*ToolExecuteResult)
	if len(toolRes.Content) != 1 {
		t.Fatalf("degraded result should be text-only, got %d blocks", len(toolRes.Content))
	}
	tc := toolRes.Content[0].(*llm.TextContent)
	if !strings.HasPrefix(tc.Text, "Read image file [image/png]\n[Image omitted:") {
		t.Errorf("degrade text must lead with the mime line, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "does not support images") {
		t.Errorf("degrade text must carry the non-vision note, got %q", tc.Text)
	}
}
