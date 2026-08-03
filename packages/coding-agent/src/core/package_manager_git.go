package ycode

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"regexp"
)

var (
	gitHeadRegex     = regexp.MustCompile(`^([0-9a-f]{40})\s+`)
	gitHeadHeadRegex = regexp.MustCompile(`(?m)^([0-9a-f]{40})\s+HEAD$`)
)

// ============================================================================
// Install / Update / Remove
// ============================================================================

func (pm *DefaultPackageManager) installGit(source GitSource, scope SourceScope) error {
	targetDir := pm.getGitInstallPath(source, scope)
	if _, err := os.Stat(targetDir); err == nil {
		return nil
	}
	gitRoot := pm.getGitInstallRoot(scope)
	if gitRoot != "" {
		pm.ensureGitIgnore(gitRoot)
	}
	os.MkdirAll(filepath.Dir(targetDir), 0o755)
	if err := pm.runCommand("git", []string{"clone", source.Repo, targetDir}, commandOptions{}); err != nil {
		return err
	}
	if source.Ref != "" {
		if err := pm.runCommand("git", []string{"checkout", source.Ref}, commandOptions{Cwd: targetDir}); err != nil {
			return err
		}
	}

	packageJsonPath := filepath.Join(targetDir, "package.json")
	if _, err := os.Stat(packageJsonPath); err == nil {
		if err := pm.runNpmCommand(pm.getGitDependencyInstallArgs(), targetDir); err != nil {
			return err
		}
	}
	return nil
}

func (pm *DefaultPackageManager) updateGit(source GitSource, scope SourceScope) error {
	targetDir := pm.getGitInstallPath(source, scope)
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		return pm.installGit(source, scope)
	}

	target, err := pm.getLocalGitUpdateTarget(targetDir)
	if err != nil {
		return err
	}

	if err := pm.runCommand("git", target.fetchArgs, commandOptions{Cwd: targetDir}); err != nil {
		return err
	}

	localHead, err := pm.runCommandCapture("git", []string{"rev-parse", "HEAD"}, commandOptions{
		Cwd:       targetDir,
		TimeoutMs: networkTimeoutMs,
	})
	if err != nil {
		return err
	}
	refreshedTargetHead, err := pm.runCommandCapture("git", []string{"rev-parse", target.ref}, commandOptions{
		Cwd:       targetDir,
		TimeoutMs: networkTimeoutMs,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(localHead) == strings.TrimSpace(refreshedTargetHead) {
		return nil
	}

	if err := pm.runCommand("git", []string{"reset", "--hard", target.ref}, commandOptions{Cwd: targetDir}); err != nil {
		return err
	}
	if err := pm.runCommand("git", []string{"clean", "-fdx"}, commandOptions{Cwd: targetDir}); err != nil {
		return err
	}

	packageJsonPath := filepath.Join(targetDir, "package.json")
	if _, err := os.Stat(packageJsonPath); err == nil {
		if err := pm.runNpmCommand(pm.getGitDependencyInstallArgs(), targetDir); err != nil {
			return err
		}
	}
	return nil
}

func (pm *DefaultPackageManager) removeGit(source GitSource, scope SourceScope) error {
	targetDir := pm.getGitInstallPath(source, scope)
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	pm.pruneEmptyGitParents(targetDir, pm.getGitInstallRoot(scope))
	return nil
}

func (pm *DefaultPackageManager) refreshTemporaryGitSource(source GitSource, sourceStr string) {
	if isOfflineModeEnabled() {
		return
	}
	pm.withProgress(ActionPull, sourceStr, fmt.Sprintf("Refreshing %s...", sourceStr), func() error {
		return pm.updateGit(source, ScopeTemporary)
	})
}

// ============================================================================
// Git update check
// ============================================================================

func (pm *DefaultPackageManager) gitHasAvailableUpdate(installedPath string) (bool, error) {
	if isOfflineModeEnabled() {
		return false, nil
	}

	localHead, err := pm.runCommandCapture("git", []string{"rev-parse", "HEAD"}, commandOptions{
		Cwd:       installedPath,
		TimeoutMs: networkTimeoutMs,
	})
	if err != nil {
		return false, nil
	}
	remoteHead, err := pm.getRemoteGitHead(installedPath)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(localHead) != strings.TrimSpace(remoteHead), nil
}

func (pm *DefaultPackageManager) getRemoteGitHead(installedPath string) (string, error) {
	upstreamRef, err := pm.getGitUpstreamRef(installedPath)
	if err == nil && upstreamRef != "" {
		remoteHead, err := pm.runGitRemoteCommand(installedPath, []string{"ls-remote", "origin", upstreamRef})
		if err == nil {
			if match := gitHeadRegex.FindStringSubmatch(remoteHead); len(match) > 1 {
				return match[1], nil
			}
		}
	}

	remoteHead, err := pm.runGitRemoteCommand(installedPath, []string{"ls-remote", "origin", "HEAD"})
	if err != nil {
		return "", err
	}
	match := gitHeadHeadRegex.FindStringSubmatch(remoteHead)
	if len(match) < 2 {
		return "", fmt.Errorf("failed to determine remote HEAD")
	}
	return match[1], nil
}

type gitUpdateTarget struct {
	ref       string
	head      string
	fetchArgs []string
}

func (pm *DefaultPackageManager) getLocalGitUpdateTarget(installedPath string) (*gitUpdateTarget, error) {
	upstream, err := pm.runCommandCapture("git", []string{"rev-parse", "--abbrev-ref", "@{upstream}"}, commandOptions{
		Cwd:       installedPath,
		TimeoutMs: networkTimeoutMs,
	})
	if err == nil {
		trimmedUpstream := strings.TrimSpace(upstream)
		if strings.HasPrefix(trimmedUpstream, "origin/") {
			branch := trimmedUpstream[len("origin/"):]
			if branch != "" {
				head, err := pm.runCommandCapture("git", []string{"rev-parse", "@{upstream}"}, commandOptions{
					Cwd:       installedPath,
					TimeoutMs: networkTimeoutMs,
				})
				if err != nil {
					return nil, err
				}
				return &gitUpdateTarget{
					ref:  "@{upstream}",
					head: head,
					fetchArgs: []string{
						"fetch", "--prune", "--no-tags", "origin",
						fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch),
					},
				}, nil
			}
		}
	}

	// Fallback: use origin/HEAD
	pm.runCommand("git", []string{"remote", "set-head", "origin", "-a"}, commandOptions{Cwd: installedPath})
	head, err := pm.runCommandCapture("git", []string{"rev-parse", "origin/HEAD"}, commandOptions{
		Cwd:       installedPath,
		TimeoutMs: networkTimeoutMs,
	})
	if err != nil {
		return nil, err
	}
	originHeadRef, _ := pm.runCommandCapture("git", []string{"symbolic-ref", "refs/remotes/origin/HEAD"}, commandOptions{
		Cwd:       installedPath,
		TimeoutMs: networkTimeoutMs,
	})
	branch := strings.TrimPrefix(strings.TrimSpace(originHeadRef), "refs/remotes/origin/")
	if branch != "" {
		return &gitUpdateTarget{
			ref:  "origin/HEAD",
			head: head,
			fetchArgs: []string{
				"fetch", "--prune", "--no-tags", "origin",
				fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch),
			},
		}, nil
	}
	return &gitUpdateTarget{
		ref:       "origin/HEAD",
		head:      head,
		fetchArgs: []string{"fetch", "--prune", "--no-tags", "origin", "+HEAD:refs/remotes/origin/HEAD"},
	}, nil
}

func (pm *DefaultPackageManager) getGitUpstreamRef(installedPath string) (string, error) {
	upstream, err := pm.runCommandCapture("git", []string{"rev-parse", "--abbrev-ref", "@{upstream}"}, commandOptions{
		Cwd:       installedPath,
		TimeoutMs: networkTimeoutMs,
	})
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(upstream)
	if !strings.HasPrefix(trimmed, "origin/") {
		return "", nil
	}
	branch := trimmed[len("origin/"):]
	if branch == "" {
		return "", nil
	}
	return "refs/heads/" + branch, nil
}

func (pm *DefaultPackageManager) runGitRemoteCommand(installedPath string, args []string) (string, error) {
	opts := commandOptions{
		Cwd:       installedPath,
		TimeoutMs: networkTimeoutMs,
		Env:       map[string]string{"GIT_TERMINAL_PROMPT": "0"},
	}
	return pm.runCommandCapture("git", args, opts)
}

// ============================================================================
// Install path computation
// ============================================================================

func (pm *DefaultPackageManager) getGitInstallPath(source GitSource, scope SourceScope) string {
	if scope == ScopeTemporary {
		return pm.getTemporaryDir("git-"+source.Host, source.Path)
	}
	if scope == ScopeProject {
		return filepath.Join(pm.cwd, ConfigDirName, "git", source.Host, source.Path)
	}
	return filepath.Join(pm.agentDir, "git", source.Host, source.Path)
}

func (pm *DefaultPackageManager) getGitInstallRoot(scope SourceScope) string {
	if scope == ScopeTemporary {
		return ""
	}
	if scope == ScopeProject {
		return filepath.Join(pm.cwd, ConfigDirName, "git")
	}
	return filepath.Join(pm.agentDir, "git")
}

func (pm *DefaultPackageManager) pruneEmptyGitParents(targetDir string, installRoot string) {
	if installRoot == "" {
		return
	}
	resolvedRoot, _ := filepath.Abs(installRoot)
	current := filepath.Dir(targetDir)
	for strings.HasPrefix(current, resolvedRoot) && current != resolvedRoot {
		if _, err := os.Stat(current); os.IsNotExist(err) {
			current = filepath.Dir(current)
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			break
		}
		if len(entries) > 0 {
			break
		}
		if err := os.RemoveAll(current); err != nil {
			break
		}
		current = filepath.Dir(current)
	}
}

// ============================================================================
// Temporary directory
// ============================================================================

func (pm *DefaultPackageManager) getTemporaryDir(prefix string, suffix string) string {
	input := prefix
	if suffix != "" {
		input = prefix + "-" + suffix
	}
	hash := sha256.Sum256([]byte(input))
	hexHash := fmt.Sprintf("%x", hash[:4]) // first 8 hex chars
	dir := filepath.Join(os.TempDir(), "ycode-extensions", prefix, hexHash)
	if suffix != "" {
		dir = filepath.Join(dir, suffix)
	}
	return dir
}