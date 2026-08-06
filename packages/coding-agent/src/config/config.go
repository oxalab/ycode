// Package config resolves the on-disk locations ycode reads from and writes to:
// the per-user agent directory (auth, sessions, settings, models, logs), the
// per-project resource directory, and the package-asset layout shipped next to
// the executable (docs, README, export templates).
//
// This is the Go port of reference/coding-agent/src/config.ts. It is the
// canonical home for these paths; the legacy core package re-exports the same
// names via thin wrappers (see packages/coding-agent/src/core/configuration.go)
// so existing call sites are unchanged.
//
// ============================================================================
// Faithful ports vs. honest deferrals
// ============================================================================
//
// config.ts is ~500 lines, but roughly the first 280 are Node/Bun-specific
// install-method detection and self-update machinery:
//
//   - detectInstallMethod()          — sniffs the install manager (npm/pnpm/
//     yarn/bun/bun-binary) from import.meta.url and process.execPath.
//   - getSelfUpdateCommand() and friends — spawn `npm root -g` / `pnpm root -g`
//     to detect global roots, check writability, and build the update command.
//
// None of that applies to a single compiled Go binary, which is not installed
// via an npm-family package manager. Those functions are ported here as
// well-documented stubs that return the Go-honest answer ("unknown" / none) so
// callers compile and behave correctly; the full machinery can be ported later
// if ycode is ever distributed through an npm-family registry.
//
// Package assets (themes, export-html, docs, README) are also handled
// differently: in the TS build they live on disk next to the entry point; in
// the Go build the built-in themes are embedded (see core/tui/theme/schema.go's
// //go:embed directives). The asset-path functions below resolve the
// shipped-aside locations for parity and for assets that are genuinely file-based
// (export templates, docs), but theme loading does not depend on GetThemesDir.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// =============================================================================
// App identity constants
// =============================================================================
//
// In the TS reference these are read from package.json at startup
// (pkg.name, pkg.piConfig.{name,configDir}, pkg.version). A Go binary has no
// package.json, so they are compile-time constants here. Version is the natural
// -ldflags injection target:
//
//	go build -ldflags "-X ycode/packages/coding-agent/src/config.Version=1.2.3"
var (
	// PackageName is the distribution package identifier (TS pkg.name).
	PackageName = "ycode-coding-agent"
	// AppName is the short application name used to derive env-var prefixes and
	// the debug-log filename. Corresponds to TS piConfig.name (default "pi").
	AppName = "ycode"
	// ConfigDirName is the per-project dot-directory name (TS
	// piConfig.configDir, default ".pi").
	ConfigDirName = ".ycode"
	// Version is the application version string. Defaults to "0.0.0"; override
	// at build time via -ldflags.
	Version = "0.1.0"
)

// =============================================================================
// Environment variable names
// =============================================================================
//
// Derived from AppName, matching the TS reference:
//
//	export const ENV_AGENT_DIR = `${APP_NAME.toUpperCase()}_CODING_AGENT_DIR`;
//
// NOTE (reconciliation): the legacy core package previously read the lowercase
// "ycode_CODING_AGENT_DIR". The TS reference — and the execution plan's own
// example, PI_CODING_AGENT_DIR — use the UPPER-CASED app name. This canonical
// package uses the uppercase form; the legacy lowercase form is no longer read.

// EnvAgentDir overrides the agent config directory (TS ENV_AGENT_DIR).
var EnvAgentDir = strings.ToUpper(AppName) + "_CODING_AGENT_DIR"

// EnvSessionDir overrides the session directory (TS ENV_SESSION_DIR).
var EnvSessionDir = strings.ToUpper(AppName) + "_CODING_AGENT_SESSION_DIR"

// EnvPackageDir overrides the package/asset root (TS PI_PACKAGE_DIR). This env
// var is intentionally NOT app-namespaced in the reference (useful for
// Nix/Guix store paths), so it stays "PI_PACKAGE_DIR" verbatim.
const EnvPackageDir = "PI_PACKAGE_DIR"

// EnvShareViewerURL overrides the share-viewer base URL (TS PI_SHARE_VIEWER_URL).
const EnvShareViewerURL = "PI_SHARE_VIEWER_URL"

// =============================================================================
// Path expansion
// =============================================================================

// ExpandTildePath expands a leading "~" to the user's home directory, matching
// the TS expandTildePath: "~" alone → home, "~/x" → home + "/x", anything else
// unchanged. On Windows a backslash separator ("~\x") is also accepted.
func ExpandTildePath(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if len(path) > 1 && path[0] == '~' && (path[1] == '/' || path[1] == '\\') {
		home, _ := os.UserHomeDir()
		return home + path[1:]
	}
	return path
}

// =============================================================================
// Package / asset paths (shipped next to the executable)
// =============================================================================

// GetPackageDir returns the base directory for resolving package assets
// (themes, docs, README, CHANGELOG).
//
// In the TS build this walks up from __dirname to find package.json (Node) or
// returns the directory of process.execPath (Bun binary). A compiled Go binary
// has no package.json to walk to, so the Go analogue is: honour the
// PI_PACKAGE_DIR override, otherwise return the directory containing the
// running executable (os.Executable). This matches the Bun-binary branch, which
// is the closest equivalent to a compiled binary.
func GetPackageDir() string {
	if envDir := os.Getenv(EnvPackageDir); envDir != "" {
		return ExpandTildePath(envDir)
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
			return filepath.Dir(resolved)
		}
		return filepath.Dir(exe)
	}
	// Fallback (os.Executable failed): the home directory.
	home, _ := os.UserHomeDir()
	return home
}

// GetThemesDir returns the built-in themes directory next to the executable
// (TS getThemesDir → <packageDir>/theme for the Bun-binary case).
//
// NOTE: the built-in themes are embedded into the Go binary
// (core/tui/theme/schema.go's //go:embed dark.json light.json), so theme
// loading does NOT read this directory. This path exists for parity with the
// reference and for any themes shipped alongside the binary. User-supplied
// themes live under GetCustomThemesDir().
func GetThemesDir() string {
	return filepath.Join(GetPackageDir(), "theme")
}

// GetExportTemplateDir returns the HTML-export template directory (TS
// getExportTemplateDir → <packageDir>/export-html).
func GetExportTemplateDir() string {
	return filepath.Join(GetPackageDir(), "export-html")
}

// GetPackageJsonPath returns the package.json path (TS getPackageJsonPath).
//
// NOTE: a compiled Go binary has no package.json; this is retained for parity.
// Callers that need the app identity should read the PackageName/AppName/Version
// constants rather than parsing this file.
func GetPackageJsonPath() string {
	return filepath.Join(GetPackageDir(), "package.json")
}

// GetReadmePath returns the absolute README.md path (TS getReadmePath).
func GetReadmePath() string {
	p, _ := filepath.Abs(filepath.Join(GetPackageDir(), "README.md"))
	return p
}

// GetDocsPath returns the absolute docs directory (TS getDocsPath →
// <packageDir>/docs). Shipped documentation lives here.
func GetDocsPath() string {
	p, _ := filepath.Abs(filepath.Join(GetPackageDir(), "docs"))
	return p
}

// GetExamplesPath returns the absolute examples directory (TS getExamplesPath).
func GetExamplesPath() string {
	p, _ := filepath.Abs(filepath.Join(GetPackageDir(), "examples"))
	return p
}

// GetChangelogPath returns the absolute CHANGELOG.md path (TS getChangelogPath).
func GetChangelogPath() string {
	p, _ := filepath.Abs(filepath.Join(GetPackageDir(), "CHANGELOG.md"))
	return p
}

// GetInteractiveAssetsDir returns the built-in interactive-assets directory
// (TS getInteractiveAssetsDir → <packageDir>/assets for the Bun-binary case).
func GetInteractiveAssetsDir() string {
	return filepath.Join(GetPackageDir(), "assets")
}

// GetBundledInteractiveAssetPath returns the path to a bundled interactive
// asset by name (TS getBundledInteractiveAssetPath).
func GetBundledInteractiveAssetPath(name string) string {
	return filepath.Join(GetInteractiveAssetsDir(), name)
}

// =============================================================================
// Share viewer URL
// =============================================================================

// DefaultShareViewerURL is the fallback base URL for shared sessions
// (TS DEFAULT_SHARE_VIEWER_URL).
//
// This is the reference default ("https://pi.dev/session/"); override it at
// runtime via the EnvShareViewerURL environment variable for a ycode-specific
// share host.
const DefaultShareViewerURL = "https://pi.dev/session/"

// GetShareViewerURL returns the share-viewer URL for a gist ID (TS
// getShareViewerUrl). The base URL is the EnvShareViewerURL env var when set,
// otherwise DefaultShareViewerURL; the gist ID is appended as a URL fragment.
func GetShareViewerUrl(gistID string) string {
	baseURL := os.Getenv(EnvShareViewerURL)
	if baseURL == "" {
		baseURL = DefaultShareViewerURL
	}
	return baseURL + "#" + gistID
}

// =============================================================================
// User config paths (under the agent directory)
// =============================================================================

// GetAgentDir returns the per-user agent config directory (TS getAgentDir).
//
// Honours the EnvAgentDir override (with ~ expansion); otherwise it is
// <home>/<ConfigDirName>/agent — e.g. ~/.ycode/agent.
func GetAgentDir() string {
	if envDir := os.Getenv(EnvAgentDir); envDir != "" {
		return ExpandTildePath(envDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ConfigDirName, "agent")
}

// GetCustomThemesDir returns the user's custom themes directory (TS
// getCustomThemesDir → <agentDir>/themes).
func GetCustomThemesDir() string {
	return filepath.Join(GetAgentDir(), "themes")
}

// GetModelsPath returns the path to models.json (TS getModelsPath).
func GetModelsPath() string {
	return filepath.Join(GetAgentDir(), "models.json")
}

// GetAuthPath returns the path to auth.json (TS getAuthPath).
func GetAuthPath() string {
	return filepath.Join(GetAgentDir(), "auth.json")
}

// GetSettingsPath returns the path to settings.json (TS getSettingsPath).
func GetSettingsPath() string {
	return filepath.Join(GetAgentDir(), "settings.json")
}

// GetToolsDir returns the tools directory (TS getToolsDir).
func GetToolsDir() string {
	return filepath.Join(GetAgentDir(), "tools")
}

// GetBinDir returns the managed-binaries directory (fd, rg) (TS getBinDir).
func GetBinDir() string {
	return filepath.Join(GetAgentDir(), "bin")
}

// GetPromptsDir returns the prompt-templates directory (TS getPromptsDir).
func GetPromptsDir() string {
	return filepath.Join(GetAgentDir(), "prompts")
}

// GetSessionDir returns the sessions directory (TS getSessionsDir →
// <agentDir>/sessions). Named singular here to match the existing Go API and
// the execution plan; the TS reference name is the plural getSessionsDir.
func GetSessionDir() string {
	return filepath.Join(GetAgentDir(), "sessions")
}

// GetDebugLogPath returns the debug-log path (TS getDebugLogPath →
// <agentDir>/<AppName>-debug.log).
func GetDebugLogPath() string {
	return filepath.Join(GetAgentDir(), AppName+"-debug.log")
}

// GetShellEnv returns the process environment for spawned shells, with the
// managed-binaries directory (GetBinDir) prepended to PATH so bundled tools
// (fd, rg) are found first.
//
// This is a Go-only addition (no direct TS analogue); it lives here because it
// derives from GetBinDir and is config-adjacent. On Windows the PATH/Path/path
// variable key is detected case-insensitively so the existing casing is
// preserved when rewriting it.
func GetShellEnv() []string {
	binDir := GetBinDir()
	pathKey := "PATH"
	if runtime.GOOS == "windows" {
		for _, kv := range os.Environ() {
			if len(kv) >= 5 && (kv[:5] == "PATH=" || kv[:5] == "Path=" || kv[:5] == "path=") {
				pathKey = kv[:4]
				break
			}
		}
	}
	currentPath := os.Getenv(pathKey)
	sep := string(os.PathListSeparator)
	return append(os.Environ(), pathKey+"="+binDir+sep+currentPath)
}

// =============================================================================
// Install-method detection + self-update (DEFERRED)
// =============================================================================
//
// The TS reference detects the install manager (npm/pnpm/yarn/bun/bun-binary)
// from import.meta.url / process.execPath and builds a self-update command by
// shelling out to that manager (`npm install -g …`, etc.). A single compiled Go
// binary is installed outside any npm-family registry, so there is no manager to
// detect and no self-update command to build. These stubs return the Go-honest
// answer so callers compile and behave correctly. Port the full machinery only
// if ycode is later distributed via an npm-family registry.

// InstallMethod is the package manager that installed the binary (TS
// InstallMethod). For a compiled Go binary this is always InstallMethodUnknown.
type InstallMethod string

const (
	// InstallMethodBunBinary is a Bun-compiled binary.
	InstallMethodBunBinary InstallMethod = "bun-binary"
	// InstallMethodNpm is an npm global install.
	InstallMethodNpm InstallMethod = "npm"
	// InstallMethodPnpm is a pnpm global install.
	InstallMethodPnpm InstallMethod = "pnpm"
	// InstallMethodYarn is a yarn global install.
	InstallMethodYarn InstallMethod = "yarn"
	// InstallMethodBun is a bun global install.
	InstallMethodBun InstallMethod = "bun"
	// InstallMethodUnknown means the install method could not be determined.
	InstallMethodUnknown InstallMethod = "unknown"
)

// IsBunBinary reports whether the process is a Bun-compiled binary (TS
// isBunBinary). Always false for a Go binary.
func IsBunBinary() bool { return false }

// IsBunRuntime reports whether Bun is the runtime (TS isBunRuntime). Always
// false for a Go binary.
func IsBunRuntime() bool { return false }

// DetectInstallMethod returns the install method (TS detectInstallMethod).
// Always InstallMethodUnknown for a compiled Go binary.
func DetectInstallMethod() InstallMethod { return InstallMethodUnknown }

// SelfUpdateCommand is a self-update command (or ordered sequence of steps) to
// run (TS SelfUpdateCommand). Always nil for a compiled Go binary.
type SelfUpdateCommand struct {
	Command string
	Args    []string
	Display string
	Steps   []SelfUpdateCommand
}

// GetSelfUpdateCommand returns the self-update command for the installed binary
// (TS getSelfUpdateCommand), or nil when none applies. Always nil for a
// compiled Go binary.
func GetSelfUpdateCommand(packageName string, npmCommand []string, updatePackageName ...string) *SelfUpdateCommand {
	_ = packageName
	_ = npmCommand
	_ = updatePackageName
	return nil
}

// GetSelfUpdateUnavailableInstruction returns a human-readable instruction for
// why self-update is unavailable and how to update manually (TS
// getSelfUpdateUnavailableInstruction).
func GetSelfUpdateUnavailableInstruction(packageName string, npmCommand []string, updatePackageName ...string) string {
	_ = npmCommand
	name := packageName
	if len(updatePackageName) > 0 && updatePackageName[0] != "" {
		name = updatePackageName[0]
	}
	return "Update " + name + " using the package manager, wrapper, or source checkout that provides this installation."
}

// GetUpdateInstruction returns the update instruction for the installed package
// (TS getUpdateInstruction). For a compiled Go binary this is the
// unavailable-instruction fallback.
func GetUpdateInstruction(packageName string) string {
	if cmd := GetSelfUpdateCommand(packageName, nil); cmd != nil {
		return "Run: " + cmd.Display
	}
	return GetSelfUpdateUnavailableInstruction(packageName, nil)
}
