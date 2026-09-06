package migrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildMigratedAuth verifies oauth precedence + apiKey gap-fill + the entry
// shapes (oauth spreads cred; apiKey yields {type:api_key, key}).
func TestBuildMigratedAuth(t *testing.T) {
	oauth := map[string]any{
		"anthropic": map[string]any{"token": "t", "refresh": "r"},
		"openai":    "not-a-map", // tolerated: entry is just {type:oauth}
	}
	apiKeys := map[string]any{
		"openai":    "sk-123", // gap-filled (openai not yet migrated by oauth object)
		"anthropic": "sk-9",   // skipped: oauth already covers anthropic
		"google":    12345,    // skipped: non-string key value
		"deepseek":  "sk-d",   // gap-filled
	}

	migrated, providers := buildMigratedAuth(oauth, apiKeys)

	// Entry shapes.
	ao, ok := migrated["anthropic"].(map[string]any)
	if !ok || ao["type"] != "oauth" || ao["token"] != "t" || ao["refresh"] != "r" {
		t.Errorf("anthropic entry = %+v, want oauth spread", migrated["anthropic"])
	}
	oo, ok := migrated["openai"].(map[string]any)
	if !ok || oo["type"] != "oauth" {
		t.Errorf("openai entry = %+v, want {type:oauth} (non-map cred)", migrated["openai"])
	}
	// openai is NOT the api_key form — oauth object entry (even non-map) wins.
	if _, hasKey := oo["key"]; hasKey {
		t.Errorf("openai got api_key form despite oauth presence: %+v", oo)
	}
	dk, ok := migrated["deepseek"].(map[string]any)
	if !ok || dk["type"] != "api_key" || dk["key"] != "sk-d" {
		t.Errorf("deepseek entry = %+v, want {type:api_key key:sk-d}", migrated["deepseek"])
	}
	// google (non-string) and anthropic-apiKey (covered) are absent from api_key form.
	if _, has := migrated["google"]; has {
		t.Errorf("google (non-string key) should be absent: %+v", migrated["google"])
	}

	// Provider set: anthropic, openai (oauth) + openai is covered so not
	// re-added, deepseek (apiKey). google excluded.
	want := map[string]bool{"anthropic": true, "openai": true, "deepseek": true}
	if len(providers) != len(want) {
		t.Fatalf("providers = %v, want %d entries", providers, len(want))
	}
	for _, p := range providers {
		if !want[p] {
			t.Errorf("unexpected provider %q in %v", p, providers)
		}
	}
}

// TestBuildMigratedAuth_Empty verifies nil-safety for empty inputs.
func TestBuildMigratedAuth_Empty(t *testing.T) {
	migrated, providers := buildMigratedAuth(nil, nil)
	if len(migrated) != 0 || len(providers) != 0 {
		t.Errorf("empty inputs → %+v / %v, want empty", migrated, providers)
	}
}

// TestFilterCustomTools verifies fd/rg (any case/.exe) and dotfiles are dropped,
// and real custom entries are kept.
func TestFilterCustomTools(t *testing.T) {
	entries := []string{"fd", "rg", "FD.EXE", "Rg.exe", ".DS_Store", ".hidden", "my-tool", "build.sh"}
	got := filterCustomTools(entries)
	want := map[string]bool{"my-tool": true, "build.sh": true}
	if len(got) != len(want) {
		t.Fatalf("filterCustomTools = %v, want %d custom entries", got, len(want))
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected entry %q in %v", e, got)
		}
	}
}

// TestKeybindingsChanged verifies no-change → false and a rename → true.
func TestKeybindingsChanged(t *testing.T) {
	before := map[string]string{"old_name": "ctrl+a", "stable": "ctrl+b"}
	same := map[string]string{"old_name": "ctrl+a", "stable": "ctrl+b"}
	if keybindingsChanged(before, same) {
		t.Error("identical configs → changed=true, want false")
	}
	renamed := map[string]string{"new_name": "ctrl+a", "stable": "ctrl+b"}
	if !keybindingsChanged(before, renamed) {
		t.Error("renamed config → changed=false, want true")
	}
}

// TestFormatDeprecationWarnings verifies the warning block text (warnings, both
// URLs, the move prompt, and the Enter prompt).
func TestFormatDeprecationWarnings(t *testing.T) {
	out := formatDeprecationWarnings([]string{"Global hooks/ directory found. Hooks have been renamed to extensions."})
	for _, want := range []string{
		"Warning: Global hooks/ directory found",
		migrationGuideURL,
		extensionsDocURL,
		"Move your extensions to the extensions/ directory.",
		"Press Enter to continue",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestMigrateCommandsToPrompts verifies the FS rename + return value on a temp
// dir, including the no-op when prompts/ already exists.
func TestMigrateCommandsToPrompts(t *testing.T) {
	base := t.TempDir()
	commandsDir := filepath.Join(base, "commands")
	if err := os.Mkdir(commandsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok := migrateCommandsToPrompts(base, "Global"); !ok {
		t.Error("first call → false, want true (migrated)")
	}
	if !fileExists(filepath.Join(base, "prompts")) {
		t.Error("prompts/ not created")
	}
	if fileExists(commandsDir) {
		t.Error("commands/ should have been renamed away")
	}
	// Neither dir present now → no-op (returns false, does not recreate commands).
	if ok := migrateCommandsToPrompts(base, "Global"); ok {
		t.Error("no commands/ → true, want false")
	}
}

// TestMigrateAuthToAuthJsonIn verifies the full auth migration on a temp agent
// dir: oauth.json + settings.json apiKeys → auth.json, with oauth.json retired
// and apiKeys stripped from settings.json. Skipped cleanly when auth.json exists.
func TestMigrateAuthToAuthJsonIn(t *testing.T) {
	agentDir := t.TempDir()

	// oauth.json with one provider.
	oauth := `{"anthropic":{"token":"t","refresh":"r"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "oauth.json"), []byte(oauth), 0o600); err != nil {
		t.Fatal(err)
	}
	// settings.json with apiKeys (one new, one overlapping oauth).
	settings := `{"apiKeys":{"openai":"sk-o","anthropic":"sk-ignored"},"other":true}`
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	providers := migrateAuthToAuthJsonIn(agentDir)

	authPath := filepath.Join(agentDir, "auth.json")
	if !fileExists(authPath) {
		t.Fatal("auth.json was not created")
	}
	// oauth.json retired to .migrated.
	if !fileExists(filepath.Join(agentDir, "oauth.json.migrated")) {
		t.Error("oauth.json was not renamed to .migrated")
	}

	// auth.json contains both providers; anthropic is the oauth form.
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "\"anthropic\"") || !strings.Contains(body, "\"openai\"") {
		t.Errorf("auth.json missing providers:\n%s", body)
	}
	if !strings.Contains(body, "\"type\": \"oauth\"") {
		t.Errorf("auth.json missing oauth type:\n%s", body)
	}
	if !strings.Contains(body, "\"type\": \"api_key\"") {
		t.Errorf("auth.json missing api_key type:\n%s", body)
	}

	// settings.json had apiKeys stripped.
	rewritten, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "apiKeys") {
		t.Errorf("settings.json still has apiKeys:\n%s", rewritten)
	}
	if !strings.Contains(string(rewritten), "\"other\"") {
		t.Errorf("settings.json lost unrelated keys:\n%s", rewritten)
	}

	// Both providers reported (anthropic from oauth, openai from apiKey).
	if len(providers) != 2 {
		t.Errorf("providers = %v, want 2", providers)
	}

	// A second run is a no-op now that auth.json exists.
	if again := migrateAuthToAuthJsonIn(agentDir); len(again) != 0 {
		t.Errorf("second run → %v, want empty (auth.json exists)", again)
	}
}

// TestMigrateOneSessionFile verifies a stray session file is moved into the
// per-cwd session dir (reusing core's encoding) and skipped when the target
// already exists.
func TestMigrateOneSessionFile(t *testing.T) {
	agentDir := t.TempDir()
	targetCwd := t.TempDir() // a real existing cwd to encode

	// Stray session file directly under agentDir with a session header. The
	// header must be valid JSON — build it with json.Marshal so the Windows cwd
	// backslashes are escaped (a real session file is written the same way).
	headerBytes, err := json.Marshal(map[string]any{
		"type":    "session",
		"version": 3,
		"id":      "abc",
		"cwd":     targetCwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`
	stray := filepath.Join(agentDir, "2026-06-18T00-00-00-000Z_abc.jsonl")
	if err := os.WriteFile(stray, append(append(headerBytes, '\n'), []byte(msg+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateOneSessionFile(stray, agentDir)

	if fileExists(stray) {
		t.Fatal("stray session file was not moved out of the agent root")
	}
	// The canonical target dir exists and holds the file.
	targetDir := sessionDirForCwd(agentDir, targetCwd)
	moved := filepath.Join(targetDir, filepath.Base(stray))
	if !fileExists(moved) {
		t.Fatalf("session not moved into %s", targetDir)
	}

	// Re-creating the stray and running again is a no-op because the target now
	// exists (skip-if-exists guard).
	if err := os.WriteFile(stray, append(append(headerBytes, '\n'), []byte(msg+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateOneSessionFile(stray, agentDir)
	if !fileExists(stray) {
		t.Error("second run moved the file despite target existing; should have skipped")
	}
}

// sessionDirForCwd mirrors core.GetDefaultSessionDir's encoding for test
// assertions (kept local so the test does not depend on core internals beyond
// the contract migrateOneSessionFile relies on).
func sessionDirForCwd(agentDir, cwd string) string {
	safe := strings.TrimLeft(cwd, "/\\")
	safe = strings.ReplaceAll(safe, "/", "-")
	safe = strings.ReplaceAll(safe, "\\", "-")
	safe = strings.ReplaceAll(safe, ":", "-")
	return filepath.Join(agentDir, "sessions", "--"+safe+"--")
}
