package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func setupAutocompleteDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// files
	mustWrite(t, filepath.Join(dir, "update.sh"), "#!/bin/bash")
	mustWrite(t, filepath.Join(dir, "utils.ts"), "export {}")
	mustWrite(t, filepath.Join(dir, "README.md"), "readme")
	// dir with spaces
	mustMkdir(t, filepath.Join(dir, "my folder"))
	mustWrite(t, filepath.Join(dir, "my folder", "test.txt"), "content")
	mustWrite(t, filepath.Join(dir, "my folder", "other.txt"), "content")
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func suggestionValues(s *AutocompleteSuggestions) []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, item := range s.Items {
		out = append(out, item.Value)
	}
	sort.Strings(out)
	return out
}

func containsValue(values []string, v string) bool {
	for _, val := range values {
		if val == v {
			return true
		}
	}
	return false
}

func TestAutocompleteSlashCommand(t *testing.T) {
	provider := NewCombinedAutocompleteProvider([]CommandSource{
		{Slash: &SlashCommand{Name: "help", Description: "Show help"}},
		{Slash: &SlashCommand{Name: "model", Description: "Switch model"}},
	}, "/tmp", "")
	s, ok := provider.GetSuggestions([]string{"/he"}, 0, 3, context.Background(), false)
	if !ok || s == nil {
		t.Fatal("should return suggestions for /he")
	}
	if len(s.Items) != 1 || s.Items[0].Value != "help" {
		t.Errorf("expected [help], got %+v", s.Items)
	}
}

func TestAutocompleteNoSlashCommandForce(t *testing.T) {
	provider := NewCombinedAutocompleteProvider(nil, "/tmp", "")
	// "/model" with force should NOT trigger file completion (it's a slash command at line start)
	_, ok := provider.GetSuggestions([]string{"/model"}, 0, 6, context.Background(), true)
	if ok {
		t.Error("should not trigger for slash commands")
	}
}

func TestAutocompleteForceSlashPath(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "alpha.txt"), "")
	mustWrite(t, filepath.Join(dir, "beta.txt"), "")
	provider := NewCombinedAutocompleteProvider(nil, dir, "")
	// "hey /" → forced, prefix "/" → root listing
	s, ok := provider.GetSuggestions([]string{"hey /"}, 0, 6, context.Background(), true)
	if !ok || s == nil {
		t.Fatal("should return suggestions for forced /")
	}
	if s.Prefix != "/" {
		t.Errorf("prefix=%q want /", s.Prefix)
	}
}

func TestAutocompleteDotSlash(t *testing.T) {
	dir := setupAutocompleteDir(t)
	provider := NewCombinedAutocompleteProvider(nil, dir, "")
	s, ok := provider.GetSuggestions([]string{"./up"}, 0, 4, context.Background(), true)
	if !ok || s == nil {
		t.Fatal("should return suggestions for ./up")
	}
	if !containsValue(suggestionValues(s), "./update.sh") {
		t.Errorf("expected ./update.sh in %v", suggestionValues(s))
	}
}

func TestAutocompleteDotSlashDir(t *testing.T) {
	dir := setupAutocompleteDir(t)
	provider := NewCombinedAutocompleteProvider(nil, dir, "")
	s, ok := provider.GetSuggestions([]string{"./sr"}, 0, 4, context.Background(), true)
	if !ok || s == nil {
		t.Skip("no src dir in test fixture")
	}
}

func TestAutocompleteQuotedPath(t *testing.T) {
	dir := setupAutocompleteDir(t)
	provider := NewCombinedAutocompleteProvider(nil, dir, "")
	// "my" forced → should suggest "my folder/" with quotes
	s, ok := provider.GetSuggestions([]string{"my"}, 0, 2, context.Background(), true)
	if !ok || s == nil {
		t.Fatal("should return suggestions for 'my'")
	}
	if !containsValue(suggestionValues(s), "\"my folder/\"") {
		t.Errorf("expected quoted 'my folder/' in %v", suggestionValues(s))
	}
}

func TestAutocompleteQuotedContinuation(t *testing.T) {
	dir := setupAutocompleteDir(t)
	provider := NewCombinedAutocompleteProvider(nil, dir, "")
	// Inside quoted path: "\"my folder/te\"" with cursor before closing quote
	s, ok := provider.GetSuggestions([]string{"\"my folder/te\""}, 0, len("\"my folder/te\"")-1, context.Background(), true)
	if !ok || s == nil {
		t.Fatal("should return suggestions inside quoted path")
	}
	values := suggestionValues(s)
	if !containsValue(values, "\"my folder/test.txt\"") {
		t.Errorf("expected 'my folder/test.txt' in %v", values)
	}
}

func TestAutocompleteQuotedNoDup(t *testing.T) {
	dir := setupAutocompleteDir(t)
	provider := NewCombinedAutocompleteProvider(nil, dir, "")
	s, ok := provider.GetSuggestions([]string{"\"my folder/te\""}, 0, len("\"my folder/te\"")-1, context.Background(), true)
	if !ok || s == nil {
		t.Fatal("should return suggestions")
	}
	var item *AutocompleteItem
	for i := range s.Items {
		if s.Items[i].Value == "\"my folder/test.txt\"" {
			item = &s.Items[i]
			break
		}
	}
	if item == nil {
		t.Fatalf("test.txt not found in %v", suggestionValues(s))
	}
	lines, _, col := provider.ApplyCompletion([]string{"\"my folder/te\""}, 0, len("\"my folder/te\"")-1, *item, s.Prefix)
	expected := "\"my folder/test.txt\""
	if lines[0] != expected {
		t.Errorf("applied=%q want %q", lines[0], expected)
	}
	_ = col
}

func TestAutocompleteSlashCommandArg(t *testing.T) {
	provider := NewCombinedAutocompleteProvider([]CommandSource{
		{Slash: &SlashCommand{
			Name: "load-skills",
			GetArgumentCompletions: func(prefix string) ([]AutocompleteItem, bool) {
				if strings.HasPrefix("skill-a", prefix) {
					return []AutocompleteItem{{Value: "skill-a", Label: "skill-a"}}, true
				}
				return nil, false
			},
		}},
	}, "/tmp", "")
	s, ok := provider.GetSuggestions([]string{"/load-skills s"}, 0, 14, context.Background(), false)
	if !ok || s == nil {
		t.Fatal("should return argument suggestions")
	}
	if len(s.Items) != 1 || s.Items[0].Value != "skill-a" {
		t.Errorf("expected [skill-a], got %+v", s.Items)
	}
}

// fd-gated tests (skip if fd not installed)

func fdInstalled() bool {
	_, err := exec.LookPath("fd")
	return err == nil
}

func TestAutocompleteFdAtFile(t *testing.T) {
	if !fdInstalled() {
		t.Skip("fd not installed")
	}
	dir := setupAutocompleteDir(t)
	provider := NewCombinedAutocompleteProvider(nil, dir, "")
	s, ok := provider.GetSuggestions([]string{"@README"}, 0, 7, context.Background(), false)
	if !ok || s == nil {
		t.Fatal("should return @ suggestions")
	}
	if !containsValue(suggestionValues(s), "@README.md") {
		t.Errorf("expected @README.md in %v", suggestionValues(s))
	}
}

func TestAutocompleteShouldTriggerFile(t *testing.T) {
	provider := NewCombinedAutocompleteProvider(nil, "/tmp", "")
	// Should NOT trigger for slash commands at line start
	if provider.ShouldTriggerFileCompletion([]string{"/model"}, 0, 6) {
		t.Error("should not trigger for /model")
	}
	// Should trigger for regular text
	if !provider.ShouldTriggerFileCompletion([]string{"hello world"}, 0, 11) {
		t.Error("should trigger for regular text")
	}
}
