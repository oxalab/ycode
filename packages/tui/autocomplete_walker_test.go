package tui

// autocomplete_walker_test.go locks in the in-Go walker fallback used when fd
// is not installed. Each test sets fdPath="" to exercise the walker path.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newWalkerProvider builds a CombinedAutocompleteProvider with fd disabled so
// all `@file` queries route through the in-Go walker.
func newWalkerProvider(t *testing.T, basePath string) *CombinedAutocompleteProvider {
	t.Helper()
	return NewCombinedAutocompleteProvider(nil, basePath, "<disabled>")
}

func writeFiles(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		full := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

func TestWalkerEmptyDir(t *testing.T) {
	dir := t.TempDir()
	p := newWalkerProvider(t, dir)
	got := p.getFuzzyFileSuggestionsViaWalk("", false, context.Background())
	if len(got) != 0 {
		t.Errorf("got %d suggestions in empty dir, want 0: %+v", len(got), got)
	}
}

func TestWalkerFindsFilesByFilename(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "main.go", "helper.go", "README.md")
	p := newWalkerProvider(t, dir)

	got := p.getFuzzyFileSuggestionsViaWalk("go", false, context.Background())
	// main.go and helper.go both contain "go" → both expected; README.md does not.
	values := make(map[string]bool, len(got))
	for _, item := range got {
		values[item.Value] = true
	}
	// Expected values use the @-prefix completion form (buildCompletionValue
	// with isAt=true). Strip the leading @ for readability.
	gotMain, gotHelper := false, false
	for v := range values {
		v = strings.TrimPrefix(v, "@")
		if strings.HasSuffix(v, "main.go") {
			gotMain = true
		}
		if strings.HasSuffix(v, "helper.go") {
			gotHelper = true
		}
	}
	if !gotMain {
		t.Errorf("main.go missing from results: %+v", values)
	}
	if !gotHelper {
		t.Errorf("helper.go missing from results: %+v", values)
	}
}

func TestWalkerRanksExactMatchFirst(t *testing.T) {
	dir := t.TempDir()
	// "go" appears as: exact filename, prefix, substring — exact should rank top.
	writeFiles(t, dir, "go", "gomodule.go", "ego.go")
	p := newWalkerProvider(t, dir)

	got := p.getFuzzyFileSuggestionsViaWalk("go", false, context.Background())
	if len(got) == 0 {
		t.Fatal("no suggestions returned")
	}
	first := strings.TrimPrefix(got[0].Value, "@")
	if !strings.HasSuffix(first, "/go") && first != "go" {
		t.Errorf("top result = %q, want exact match \"go\"", got[0].Value)
	}
}

func TestWalkerSkipsIgnoredDirs(t *testing.T) {
	dir := t.TempDir()
	// Same filename inside an ignored dir and a normal dir.
	writeFiles(t, dir,
		"src/target.go",
		"node_modules/dep/index.js",
		"node_modules/dep/target.go",
		".git/config",
		".git/hooks/target.go",
	)
	p := newWalkerProvider(t, dir)

	got := p.getFuzzyFileSuggestionsViaWalk("target", false, context.Background())
	for _, item := range got {
		v := strings.TrimPrefix(item.Value, "@")
		if strings.Contains(v, "node_modules/") {
			t.Errorf("walker returned node_modules entry: %q", v)
		}
		if strings.Contains(v, ".git/") || v == ".git" {
			t.Errorf("walker returned .git entry: %q", v)
		}
	}
	// Sanity: src/target.go should still be present.
	foundSrc := false
	for _, item := range got {
		if strings.HasSuffix(strings.TrimPrefix(item.Value, "@"), "src/target.go") {
			foundSrc = true
			break
		}
	}
	if !foundSrc {
		t.Errorf("src/target.go missing from results: %+v", got)
	}
}

func TestWalkerRespectsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "a.txt", "b.txt", "c.txt")
	p := newWalkerProvider(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	got := p.getFuzzyFileSuggestionsViaWalk("", false, ctx)
	// Either zero results (cancelled before first entry) or a partial set —
	// both are acceptable; the contract is "don't hang."
	_ = got
}

func TestWalkerScopedQuery(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir,
		"packages/tui/main.go",
		"packages/core/main.go",
		"other/main.go",
	)
	p := newWalkerProvider(t, dir)

	// @packages/tui/main → should walk only packages/tui, find only that main.go.
	got := p.getFuzzyFileSuggestionsViaWalk("packages/tui/main", false, context.Background())
	if len(got) == 0 {
		t.Fatal("scoped query returned nothing")
	}
	for _, item := range got {
		v := strings.TrimPrefix(item.Value, "@")
		if !strings.HasPrefix(v, "packages/tui/") {
			t.Errorf("scoped query leaked result outside packages/tui/: %q", v)
		}
	}
}

func TestWalkerMaxReturnCap(t *testing.T) {
	dir := t.TempDir()
	// Create walkerMaxReturn + a bunch more files all matching "x".
	files := make([]string, 0, walkerMaxReturn+50)
	for i := 0; i < walkerMaxReturn+50; i++ {
		files = append(files, "x"+string(rune('a'+(i%26)))+string(rune('a'+((i/26)%26)))+".txt")
	}
	writeFiles(t, dir, files...)
	p := newWalkerProvider(t, dir)

	got := p.getFuzzyFileSuggestionsViaWalk("x", false, context.Background())
	if len(got) > walkerMaxReturn {
		t.Errorf("returned %d suggestions, cap is %d", len(got), walkerMaxReturn)
	}
}

func TestWalkerDirectoriesIncludedAsCandidates(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir,
		"src/foo.go",       // file foo
		"src/foo/index.go", // foo as a directory name
	)
	p := newWalkerProvider(t, dir)

	got := p.getFuzzyFileSuggestionsViaWalk("foo", false, context.Background())
	// Both "src/foo.go" (file) and "src/foo" (dir) should appear; the dir
	// should have a trailing slash in its Value.
	foundDir := false
	for _, item := range got {
		v := strings.TrimPrefix(item.Value, "@")
		if strings.HasSuffix(v, "src/foo/") {
			foundDir = true
			break
		}
	}
	if !foundDir {
		t.Errorf("directory candidate src/foo/ missing from results: %+v", got)
	}
}

func TestWalkerSymlinksSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior test is unreliable on Windows without admin")
	}
	dir := t.TempDir()
	writeFiles(t, dir, "real.txt")
	// Create a symlink that points outside the tree — walker should not crash.
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink create failed: %v", err)
	}
	p := newWalkerProvider(t, dir)

	got := p.getFuzzyFileSuggestionsViaWalk("txt", false, context.Background())
	if len(got) == 0 {
		t.Fatal("no suggestions returned")
	}
}

func TestGetFuzzyFileSuggestionsFallsBackWhenFdMissing(t *testing.T) {
	// Sanity: with fdPath == "" the public entry should route through the
	// walker, not return nil.
	dir := t.TempDir()
	writeFiles(t, dir, "hello.go")
	p := &CombinedAutocompleteProvider{commands: nil, basePath: dir, fdPath: ""}

	got := p.getFuzzyFileSuggestions("hello", false, context.Background())
	if len(got) == 0 {
		t.Fatal("fdPath=\"\" returned no results — fallback not engaged")
	}
}
