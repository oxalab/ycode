package ycode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ============================================================================
// NPM version helpers
// ============================================================================

func (pm *DefaultPackageManager) installedNpmMatchesPinnedVersion(source NpmSource, installedPath string) (bool, error) {
	installedVersion := pm.getInstalledNpmVersion(installedPath)
	if installedVersion == "" {
		return false, nil
	}
	_, pinnedVersion := pm.parseNpmSpec(source.Spec)
	if pinnedVersion == "" {
		return true, nil
	}
	return installedVersion == pinnedVersion, nil
}

func (pm *DefaultPackageManager) npmHasAvailableUpdate(source NpmSource, installedPath string) (bool, error) {
	if isOfflineModeEnabled() {
		return false, nil
	}
	installedVersion := pm.getInstalledNpmVersion(installedPath)
	if installedVersion == "" {
		return false, nil
	}
	latestVersion, err := pm.getLatestNpmVersion(source.Name)
	if err != nil {
		return false, nil
	}
	return latestVersion != installedVersion, nil
}

func (pm *DefaultPackageManager) getInstalledNpmVersion(installedPath string) string {
	packageJsonPath := filepath.Join(installedPath, "package.json")
	data, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	return pkg.Version
}

func (pm *DefaultPackageManager) getLatestNpmVersion(packageName string) (string, error) {
	npmCommand := pm.getNpmCommand()
	args := append([]string{}, npmCommand.args...)
	args = append(args, "view", packageName, "version", "--json")
	stdout, err := pm.runCommandCapture(npmCommand.command, args, commandOptions{
		Cwd: 	pm.cwd,
		TimeoutMs: networkTimeoutMs,
	})
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(stdout)
	if raw == "" {
		return "", fmt.Errorf("empty response from npm view")
	}
	var version string
	if err := json.Unmarshal([]byte(raw), &version); err != nil {
		return "", err
	}
	return version, nil
}

// ============================================================================
// NPM command runner
// ============================================================================

type npmCmd struct {
	command string
	args 	[]string
}

func (pm *DefaultPackageManager) getNpmCommand() npmCmd {
	configured := pm.settingsManager.GetNpmCommand()
	if len(configured) == 0 {
		return npmCmd{command: "npm", args: []string{}}
	}
	command := configured[0]
	if command == "" {
		panic("invalid npmCommand: first array entry must be a non-empty command")
	}
	return npmCmd{command: command, args: configured[1:]}
}

func (pm *DefaultPackageManager) runNpmCommand(args []string, cwd string) error {
	npmCommand := pm.getNpmCommand()
	fullArgs := append([]string{}, npmCommand.args...)
	fullArgs = append(fullArgs, args...)
	return pm.runCommand(npmCommand.command, fullArgs, commandOptions{Cwd: cwd})
}

func (pm *DefaultPackageManager) runNpmCommandSync(args []string) string {
	npmCommand := pm.getNpmCommand()
	fullArgs := append([]string{}, npmCommand.args...)
	fullArgs = append(fullArgs, args...)
	return pm.runCommandSync(npmCommand.command, fullArgs)
}

func (pm *DefaultPackageManager) getGitDependencyInstallArgs() []string {
	configured := pm.settingsManager.GetNpmCommand()
	if len(configured) > 0 {
		return []string{"install"}
	}
	return []string{"install", "--omi=dev"}
}

// ============================================================================
// Install / Uninstall
// ============================================================================

func (pm *DefaultPackageManager) installNpm(source NpmSource, scope SourceScope, temporary bool) error {
	if scope == ScopeUser && !temporary {
		return pm.runNpmCommand([]string{"install", "-g", source.Spec}, "")
	}
	installRoot := pm.getNpmInstallRoot(scope, temporary)
	pm.ensureNpmProject(installRoot)
	args := []string{"install", source.Spec, "--prefix", installRoot}
	return pm.runNpmCommand(args, "")
}

func (pm *DefaultPackageManager) uninstallNpm(source NpmSource, scope SourceScope) error {
	if scope == ScopeUser {
		return pm.runNpmCommand([]string{"uninstall", "-g", source.Name}, "")
	}
	installRoot := pm.getNpmInstallRoot(scope, false)
	if _, err := os.Stat(installRoot); os.IsNotExist(err) {
		return nil
	}
	args := []string{"uninstall", source.Name, "--prefix", installRoot}
	return pm.runNpmCommand(args, "")
}

// ============================================================================
// Install path computation
// ============================================================================

func (pm *DefaultPackageManager) getNpmInstallRoot(scope SourceScope, temporary bool) string {
	if temporary {
		return pm.getTemporaryDir("npm", "")
	}
	if scope == ScopeProject {
		return filepath.Join(pm.cwd, ConfigDirName, "npm")
	}
	globalRoot := pm.getGlobalNpmRoot()
	return filepath.Dir(globalRoot)
}

func (pm *DefaultPackageManager) getGlobalNpmRoot() string {
	npmCommand := pm.getNpmCommand()
	commandKey := strings.Join(append([]string{npmCommand.command}, npmCommand.args...), "\x00")
	if pm.globalNpmRoot != "" && pm.globalNpmRootKey == commandKey {
		return pm.globalNpmRoot
	}

	var globalRoot string
	if npmCommand.command == "bun" {
		binDir := strings.TrimSpace(pm.runNpmCommandSync([]string{"pm", "bin", "-g"}))
		globalRoot = filepath.Join(filepath.Dir(binDir), "install", "global", "node_modules")
	} else {
		globalRoot = strings.TrimSpace(pm.runNpmCommandSync([]string{"root", "-g"}))
	}

	pm.globalNpmRoot = globalRoot
	pm.globalNpmRootKey = commandKey
	return globalRoot
}

func (pm *DefaultPackageManager) getNpmInstallPath(source NpmSource, scope SourceScope) string {
	if scope == ScopeTemporary {
		return filepath.Join(pm.getTemporaryDir("npm", ""), "node_modules", source.Name)
	}
	if scope == ScopeProject {
		return filepath.Join(pm.cwd, ConfigDirName, "npm", "node_modules", source.Name)
	}
	return filepath.Join(pm.getGlobalNpmRoot(), source.Name)
}

// ============================================================================
// Project scaffolding
// ============================================================================

func (pm *DefaultPackageManager) ensureNpmProject(installRoot string) {
	if _, err := os.Stat(installRoot); os.IsNotExist(err) {
		os.MkdirAll(installRoot, 0o755)
	}
	pm.ensureGitIgnore(installRoot)
	packageJsonPath := filepath.Join(installRoot, "package.json")
	if _, err := os.Stat(packageJsonPath); os.IsNotExist(err) {
		// Minimal package.json
		content := `{
 "name": "ycode-extensions",
 "private": true
}`
		os.WriteFile(packageJsonPath, []byte(content), 0o644)
	} 
}

func (pm *DefaultPackageManager) ensureGitIgnore(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		os.MkdirAll(dir, 0o755)
	}
	ignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		os.WriteFile(ignorePath, []byte("*\n!.gitignore\n"), 0o644)
	}
}

/* KEYFLAGS:
 1. runNpmCommandSync is needed by getGlobalNpmRoot: This means npm install 
 path resolution needs sync exec — important to keep.

 2. Bun support preserved: bun pm bin -g is used instead of npm root -g if 
 the user has configured bun as their package manager.

 3. getNpmCommand() panics on invalid config: Mirrors TS behavior — 
 invalid npmCommand throws. Callers should validate config first.
*/