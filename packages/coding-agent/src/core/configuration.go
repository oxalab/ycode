package ycode

// This file historically held the config-path resolution logic ported from
// reference/coding-agent/src/config.ts. As of Phase 19.1 that logic has a
// single canonical home: packages/coding-agent/src/config. The exported names
// below are retained as thin wrappers so existing call sites (~15 sites across
// package_manager, resource_loader, skills, slash_commands, and cmd/ycode) are
// unchanged, while there is exactly one source of truth.
//
// New code should import the config package directly.

import (
	"ycode/packages/coding-agent/src/config"
)

// Re-export the identity constants from the canonical config package. These are
// build-time constants; they are copied once at init and never mutated at
// runtime, so the copy semantics are safe. (Version remains the -ldflags
// injection target — set it on the config package.)
var (
	PackageName   = config.PackageName
	AppName       = config.AppName
	ConfigDirName = config.ConfigDirName
	Version       = config.Version
)

// ExpandTildePath expands a leading "~" to the user's home directory. Delegates
// to config.ExpandTildePath.
func ExpandTildePath(path string) string {
	return config.ExpandTildePath(path)
}

// GetAgentDir returns the per-user agent config directory. Delegates to
// config.GetAgentDir.
func GetAgentDir() string {
	return config.GetAgentDir()
}

// GetDocsPath returns the docs directory. Delegates to config.GetDocsPath.
func GetDocsPath() string {
	return config.GetDocsPath()
}

// GetBinDir returns the managed-binaries directory. Delegates to config.GetBinDir.
func GetBinDir() string {
	return config.GetBinDir()
}

// GetModelsPath returns the models.json path. Delegates to config.GetModelsPath.
func GetModelsPath() string {
	return config.GetModelsPath()
}

// GetAuthPath returns the auth.json path. Delegates to config.GetAuthPath.
func GetAuthPath() string {
	return config.GetAuthPath()
}

// GetSettingsPath returns the settings.json path. Delegates to
// config.GetSettingsPath.
func GetSettingsPath() string {
	return config.GetSettingsPath()
}

// GetSessionDir returns the sessions directory. Delegates to
// config.GetSessionDir.
func GetSessionDir() string {
	return config.GetSessionDir()
}

// GetToolsDir returns the tools directory. Delegates to config.GetToolsDir.
func GetToolsDir() string {
	return config.GetToolsDir()
}

// GetPromptsDir returns the prompt-templates directory. Delegates to
// config.GetPromptsDir.
func GetPromptsDir() string {
	return config.GetPromptsDir()
}

// GetDebugLogPath returns the debug-log path. Delegates to
// config.GetDebugLogPath.
func GetDebugLogPath() string {
	return config.GetDebugLogPath()
}

// GetShellEnv returns the process environment for spawned shells with the
// managed-binaries directory prepended to PATH. Delegates to config.GetShellEnv.
func GetShellEnv() []string {
	return config.GetShellEnv()
}
