package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Tools re-audit B6: tryNFDVariant was a no-op TODO, so the NFD fallback in
// ResolveReadPath (and the combined NFD+curly variant) was dead code — NFC user
// input could never find NFD-decomposed on-disk names.
func TestResolveReadPath_NFDVariant(t *testing.T) {
	tempDir := t.TempDir()

	// NFD: e (U+0065) + combining acute accent (U+0301)
	nfdName := "file\u0065\u0301.txt"
	// NFC: é as a single character (U+00E9)
	nfcName := "file\u00e9.txt"

	if err := os.WriteFile(filepath.Join(tempDir, nfdName), []byte("content"), 0o644); err != nil {
		t.Fatalf("write NFD file: %v", err)
	}

	got := ResolveReadPath(nfcName, tempDir)
	wantNFD := filepath.Join(tempDir, nfdName)
	wantNFC := filepath.Join(tempDir, nfcName) // normalizing filesystems hit this directly

	if got != wantNFD && got != wantNFC {
		t.Fatalf("got %q, want NFD variant %q (or direct NFC hit %q)", got, wantNFD, wantNFC)
	}
	if !fileExists(got) {
		t.Fatalf("resolved path %q does not exist", got)
	}
}

// Tools re-audit B6: the combined NFD + curly-quote variant (path_utils.go
// tryCurlyQuoteVariant(nfdVariant)) must become live once tryNFDVariant works —
// only that chain step matches a curly-quote + decomposed-accent on-disk name.
func TestResolveReadPath_NFDCurlyCombined(t *testing.T) {
	tempDir := t.TempDir()

	// On disk: curly quote (U+2019) + NFD é (e + combining acute)
	onDiskName := "Capture d\u2019\u0065\u0301cran.txt"
	// User input: straight quote (U+0027) + NFC é (U+00E9)
	inputName := "Capture d'\u00e9cran.txt"

	if err := os.WriteFile(filepath.Join(tempDir, onDiskName), []byte("content"), 0o644); err != nil {
		t.Fatalf("write combined-variant file: %v", err)
	}

	got := ResolveReadPath(inputName, tempDir)
	want := filepath.Join(tempDir, onDiskName)
	if got != want {
		t.Fatalf("got %q, want combined NFD+curly variant %q", got, want)
	}
}

// Guard for the existing curly-quote-only step (straight U+0027 → curly U+2019).
func TestResolveReadPath_CurlyQuoteOnly(t *testing.T) {
	tempDir := t.TempDir()

	onDiskName := "it\u2019s here.txt"
	inputName := "it's here.txt"

	if err := os.WriteFile(filepath.Join(tempDir, onDiskName), []byte("content"), 0o644); err != nil {
		t.Fatalf("write curly-quote file: %v", err)
	}

	got := ResolveReadPath(inputName, tempDir)
	want := filepath.Join(tempDir, onDiskName)
	if got != want {
		t.Fatalf("got %q, want curly-quote variant %q", got, want)
	}
}

func TestResolveToCwd_RelativeAndAbsolute(t *testing.T) {
	cwd := t.TempDir()

	if got := ResolveToCwd("sub/file.txt", cwd); got != filepath.Join(cwd, "sub", "file.txt") {
		t.Errorf("relative: got %q, want %q", got, filepath.Join(cwd, "sub", "file.txt"))
	}

	abs := filepath.Join(cwd, "abs", "file.txt")
	if got := ResolveToCwd(abs, cwd); got != abs {
		t.Errorf("absolute: got %q, want unchanged %q", got, abs)
	}
}

// Tools re-audit B7: Windows "/foo"-style root-relative paths were joined under
// cwd (filepath.IsAbs("/foo") is false on Windows, unlike TS path.isAbsolute);
// they must anchor to the cwd's drive root like Node's path.resolve(cwd, "/foo").
func TestResolveToCwd_WindowsRootRelative(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("root-relative drive anchoring is Windows-only behavior")
	}
	cwd := t.TempDir()
	vol := filepath.VolumeName(cwd)

	if got := ResolveToCwd("/foo", cwd); got != vol+`\foo` {
		t.Errorf(`"/foo": got %q, want %q`, got, vol+`\foo`)
	}
	if got := ResolveToCwd(`\foo`, cwd); got != vol+`\foo` {
		t.Errorf(`"\foo": got %q, want %q`, got, vol+`\foo`)
	}
	// Guard against over-matching: plain relatives must stay cwd-joined.
	if got := ResolveToCwd("foo", cwd); got != filepath.Join(cwd, "foo") {
		t.Errorf("relative: got %q, want %q", got, filepath.Join(cwd, "foo"))
	}
}
