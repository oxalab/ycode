// Package migrations implements the one-time startup migrations ported from
// reference/coding-agent/src/migrations.ts: legacy oauth.json/settings.json
// apiKeys → auth.json, stray session files → per-cwd session dirs, commands/ →
// prompts/, tools/ binaries → bin/, keybindings.json renames, and deprecated
// hooks//tools/ directory warnings.
//
// ============================================================================
// Placement + deviation from the execution-plan sketch
// ============================================================================
//
// The sketch placed this at packages/coding-agent/src/config/migrations.go. That
// is impossible without an import cycle: the package needs
// core.GetDefaultSessionDir (the canonical session-dir encoding, reused so
// migrated sessions land exactly where the manager looks) and
// core.MigrateKeybindingsConfig, but core already imports config
// (core/configuration.go). A config→core edge would close the loop. The TS file
// is a top-level module (src/migrations.ts) importing both ./config and
// ./core/…; the faithful Go analog is this dedicated top-level package, which
// imports core + config and is imported only by the entry point. Neither core
// nor config imports it, so there is no cycle.
//
// ============================================================================
// Testability
// ============================================================================
//
// The filesystem-touching migrations resolve their target directories from
// config.GetAgentDir()/GetBinDir(), which point at the real agent dir and must
// not be exercised by unit tests. Each therefore has an "...In(dir...)" seam that
// takes the directories explicitly and is covered by migrations_test.go (temp
// dirs); the exported wrappers resolve the dirs from config. The pure logic
// (auth merge, custom-tool filter, deprecation text, keybindings-change detect)
// is extracted into helpers with no FS dependency.

package migrations

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ycode/packages/coding-agent/src/config"
	core "ycode/packages/coding-agent/src/core"
)

// ============================================================================
// Constants + ANSI (warnings print before any TUI/theme)
// ============================================================================

const (
	migrationGuideURL = "https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration"
	extensionsDocURL  = "https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/extensions.md"
)

const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

// MigrationResult mirrors the TS runMigrations return.
type MigrationResult struct {
	MigratedAuthProviders []string
	DeprecationWarnings   []string
}

// ============================================================================
// RunMigrations — TS runMigrations
// ============================================================================

// RunMigrations runs all one-time startup migrations (TS runMigrations). It
// returns the migrated auth-provider names and the collected deprecation
// warnings. The session/tools/keybindings migrations are side-effecting only.
func RunMigrations(cwd string) MigrationResult {
	agentDir := config.GetAgentDir()
	migratedAuth := migrateAuthToAuthJsonIn(agentDir)
	migrateSessionsFromAgentRootIn(agentDir)
	migrateToolsToBinIn(agentDir, config.GetBinDir())
	migrateKeybindingsConfigFileIn(agentDir)
	deprecationWarnings := migrateExtensionSystemIn(cwd, agentDir)
	return MigrationResult{
		MigratedAuthProviders: migratedAuth,
		DeprecationWarnings:   deprecationWarnings,
	}
}

// ============================================================================
// Auth migration — TS migrateAuthToAuthJson
// ============================================================================

// MigrateAuthToAuthJson migrates legacy oauth.json + settings.json apiKeys into
// auth.json, returning the migrated provider names (TS migrateAuthToAuthJson).
func MigrateAuthToAuthJson() []string {
	return migrateAuthToAuthJsonIn(config.GetAgentDir())
}

// migrateAuthToAuthJsonIn is the dir-parametrized seam. It is a no-op when
// auth.json already exists. oauth.json (if any) is renamed to .migrated.
func migrateAuthToAuthJsonIn(agentDir string) []string {
	authPath := filepath.Join(agentDir, "auth.json")
	if fileExists(authPath) {
		return nil
	}

	oauthPath := filepath.Join(agentDir, "oauth.json")
	settingsPath := filepath.Join(agentDir, "settings.json")

	var oauth map[string]any
	if data, err := os.ReadFile(oauthPath); err == nil {
		// Parse errors are silently skipped (TS try/catch), but a successful parse
		// still retires oauth.json regardless of whether it contributed entries.
		if jsonErr := json.Unmarshal(data, &oauth); jsonErr == nil {
			_ = os.Rename(oauthPath, oauthPath+".migrated")
		} else {
			oauth = nil
		}
	}

	var apiKeys map[string]any
	settingsBytes, settingsErr := os.ReadFile(settingsPath)
	if settingsErr == nil {
		var settings map[string]any
		if jsonErr := json.Unmarshal(settingsBytes, &settings); jsonErr == nil {
			if keys, ok := settings["apiKeys"].(map[string]any); ok {
				apiKeys = keys
				delete(settings, "apiKeys")
				if rewritten, mErr := json.MarshalIndent(settings, "", "  "); mErr == nil {
					_ = os.WriteFile(settingsPath, rewritten, 0o644)
				}
			}
		}
	}

	migrated, providers := buildMigratedAuth(oauth, apiKeys)
	if len(migrated) == 0 {
		return providers
	}
	_ = os.MkdirAll(filepath.Dir(authPath), 0o755)
	if data, err := json.MarshalIndent(migrated, "", "  "); err == nil {
		_ = os.WriteFile(authPath, data, 0o600)
	}
	return providers
}

// buildMigratedAuth merges oauth credentials and settings apiKeys into the
// migrated auth map (pure). oauth entries become {type:"oauth", ...cred};
// apiKey entries (string only) become {type:"api_key", key}; apiKeys fill only
// gaps not already covered by oauth. Returns the merged map and provider names.
func buildMigratedAuth(oauth, apiKeys map[string]any) (map[string]any, []string) {
	migrated := map[string]any{}
	var providers []string

	for provider, cred := range oauth {
		entry := map[string]any{"type": "oauth"}
		if credMap, ok := cred.(map[string]any); ok {
			for k, v := range credMap {
				entry[k] = v
			}
		}
		migrated[provider] = entry
		providers = append(providers, provider)
	}

	for provider, key := range apiKeys {
		if _, exists := migrated[provider]; exists {
			continue
		}
		keyStr, ok := key.(string)
		if !ok {
			continue
		}
		migrated[provider] = map[string]any{"type": "api_key", "key": keyStr}
		providers = append(providers, provider)
	}

	return migrated, providers
}

// ============================================================================
// Session migration — TS migrateSessionsFromAgentRoot
// ============================================================================

// MigrateSessionsFromAgentRoot moves stray .jsonl files from the agent root into
// the per-cwd session directory encoded in each file's header (TS
// migrateSessionsFromAgentRoot).
func MigrateSessionsFromAgentRoot() {
	migrateSessionsFromAgentRootIn(config.GetAgentDir())
}

// migrateSessionsFromAgentRootIn is the dir-parametrized seam.
func migrateSessionsFromAgentRootIn(agentDir string) {
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		migrateOneSessionFile(filepath.Join(agentDir, e.Name()), agentDir)
	}
}

// migrateOneSessionFile moves a single stray session file into its correct
// per-cwd directory if the target does not already exist (TS loop body).
func migrateOneSessionFile(file, agentDir string) {
	content, err := os.ReadFile(file)
	if err != nil {
		return
	}
	firstLine := strings.TrimSpace(strings.SplitN(string(content), "\n", 2)[0])
	if firstLine == "" {
		return
	}
	var header struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(firstLine), &header); err != nil {
		return
	}
	if header.Type != "session" || header.Cwd == "" {
		return
	}
	// Reuse the canonical encoding + dir creation so the file lands exactly where
	// the session manager expects (GetDefaultSessionDir also mkdirs the target).
	correctDir := core.GetDefaultSessionDir(header.Cwd, agentDir)
	newPath := filepath.Join(correctDir, filepath.Base(file))
	if fileExists(newPath) {
		return
	}
	_ = os.Rename(file, newPath)
}

// ============================================================================
// commands/ → prompts/ — TS migrateCommandsToPrompts
// ============================================================================

// migrateCommandsToPrompts renames commands/ to prompts/ when prompts/ does not
// already exist (TS migrateCommandsToPrompts). Returns whether it migrated.
func migrateCommandsToPrompts(baseDir, label string) bool {
	commandsDir := filepath.Join(baseDir, "commands")
	promptsDir := filepath.Join(baseDir, "prompts")
	if !fileExists(commandsDir) || fileExists(promptsDir) {
		return false
	}
	if err := os.Rename(commandsDir, promptsDir); err != nil {
		fmt.Printf("%sWarning: Could not migrate %s commands/ to prompts/: %s%s\n",
			ansiYellow, label, err.Error(), ansiReset)
		return false
	}
	fmt.Printf("%sMigrated %s commands/ → prompts/%s\n", ansiGreen, label, ansiReset)
	return true
}

// ============================================================================
// Keybindings migration — TS migrateKeybindingsConfigFile
// ============================================================================

// migrateKeybindingsConfigFileIn migrates <agentDir>/keybindings.json in place,
// rewriting it only when a binding name was renamed (TS
// migrateKeybindingsConfigFile).
func migrateKeybindingsConfigFileIn(agentDir string) {
	configPath := filepath.Join(agentDir, "keybindings.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var parsed map[string]string
	if err := json.Unmarshal(content, &parsed); err != nil {
		return
	}
	migrated := core.MigrateKeybindingsConfig(parsed)
	if !keybindingsChanged(parsed, migrated) {
		return
	}
	out, err := json.MarshalIndent(migrated, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(configPath, append(out, '\n'), 0o644)
}

// keybindingsChanged reports whether migration altered the config (pure). Go's
// json.Marshal sorts map keys, so a byte comparison is deterministic.
func keybindingsChanged(before, after map[string]string) bool {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	return string(b) != string(a)
}

// ============================================================================
// tools/ → bin/ — TS migrateToolsToBin
// ============================================================================

// migrateToolsToBinIn moves the managed fd/rg binaries from tools/ to bin/
// (TS migrateToolsToBin). When the target already exists, the old copy is removed.
func migrateToolsToBinIn(agentDir, binDir string) {
	toolsDir := filepath.Join(agentDir, "tools")
	if !fileExists(toolsDir) {
		return
	}
	binaries := []string{"fd", "rg", "fd.exe", "rg.exe"}
	movedAny := false
	for _, bin := range binaries {
		oldPath := filepath.Join(toolsDir, bin)
		newPath := filepath.Join(binDir, bin)
		if !fileExists(oldPath) {
			continue
		}
		if !fileExists(binDir) {
			_ = os.MkdirAll(binDir, 0o755)
		}
		if !fileExists(newPath) {
			if err := os.Rename(oldPath, newPath); err == nil {
				movedAny = true
			}
		} else {
			_ = os.Remove(oldPath)
		}
	}
	if movedAny {
		fmt.Printf("%sMigrated managed binaries tools/ → bin/%s\n", ansiGreen, ansiReset)
	}
}

// ============================================================================
// Deprecated directory checks — TS checkDeprecatedExtensionDirs
// ============================================================================

// checkDeprecatedExtensionDirs returns warnings for a hooks/ directory or a
// tools/ directory holding anything beyond the auto-extracted fd/rg binaries
// (TS checkDeprecatedExtensionDirs).
func checkDeprecatedExtensionDirs(baseDir, label string) []string {
	var warnings []string
	if fileExists(filepath.Join(baseDir, "hooks")) {
		warnings = append(warnings, label+" hooks/ directory found. Hooks have been renamed to extensions.")
	}
	toolsDir := filepath.Join(baseDir, "tools")
	if fileExists(toolsDir) {
		if entries, err := os.ReadDir(toolsDir); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			if len(filterCustomTools(names)) > 0 {
				warnings = append(warnings, label+" tools/ directory contains custom tools. Custom tools have been merged into extensions.")
			}
		}
	}
	return warnings
}

// filterCustomTools drops the auto-extracted fd/rg binaries (any case / .exe)
// and dotfiles, returning the remaining custom entries (TS customTools filter).
// Pure for testability.
func filterCustomTools(entries []string) []string {
	var custom []string
	for _, e := range entries {
		lower := strings.ToLower(e)
		if lower == "fd" || lower == "rg" || lower == "fd.exe" || lower == "rg.exe" {
			continue
		}
		if strings.HasPrefix(e, ".") {
			continue
		}
		custom = append(custom, e)
	}
	return custom
}

// ============================================================================
// Extension system — TS migrateExtensionSystem
// ============================================================================

// migrateExtensionSystemIn runs the commands→prompts migration and collects
// deprecated-directory warnings for both the global agent dir and the project
// dir (TS migrateExtensionSystem).
func migrateExtensionSystemIn(cwd, agentDir string) []string {
	projectDir := filepath.Join(cwd, config.ConfigDirName)
	migrateCommandsToPrompts(agentDir, "Global")
	migrateCommandsToPrompts(projectDir, "Project")
	var warnings []string
	warnings = append(warnings, checkDeprecatedExtensionDirs(agentDir, "Global")...)
	warnings = append(warnings, checkDeprecatedExtensionDirs(projectDir, "Project")...)
	return warnings
}

// ============================================================================
// Deprecation warnings — TS showDeprecationWarnings
// ============================================================================

// ShowDeprecationWarnings prints the deprecation warnings and waits for the user
// to press Enter (TS showDeprecationWarnings). It is a no-op for an empty list.
//
// The TS waits for any keypress via raw-mode stdin; cooked-mode line input
// requires Enter, which is an acceptable approximation for the interactive-only
// call site (the entry point invokes this only in interactive mode, where stdin
// is a TTY and not consumed by piped-message reading).
func ShowDeprecationWarnings(warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Print(formatDeprecationWarnings(warnings))
	waitForEnter(os.Stdin)
	fmt.Println()
}

// formatDeprecationWarnings renders the warning block (pure): each warning in
// yellow, the move/links guidance, and a "Press Enter to continue..." prompt.
func formatDeprecationWarnings(warnings []string) string {
	var b strings.Builder
	for _, w := range warnings {
		b.WriteString(ansiYellow + "Warning: " + w + ansiReset + "\n")
	}
	b.WriteString(ansiYellow + "\nMove your extensions to the extensions/ directory." + ansiReset + "\n")
	b.WriteString(ansiYellow + "Migration guide: " + migrationGuideURL + ansiReset + "\n")
	b.WriteString(ansiYellow + "Documentation: " + extensionsDocURL + ansiReset + "\n")
	b.WriteString(ansiDim + "\nPress Enter to continue..." + ansiReset + "\n")
	return b.String()
}

// waitForEnter blocks until a newline is read from r (best-effort; errors/EOF
// return immediately).
func waitForEnter(r io.Reader) {
	bufio.NewReader(r).ReadString('\n')
}

// ============================================================================
// Small helpers
// ============================================================================

// fileExists reports whether path exists (any type). Mirrors the TS existsSync.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
