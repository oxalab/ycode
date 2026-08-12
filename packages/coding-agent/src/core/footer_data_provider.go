package ycode

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// Types — mirrors TS footer-data-provider.ts
// ============================================================================

// GitPaths holds paths to git repository internals.
// Mirrors TS GitPaths (footer-data-provider.ts:6-10).
type GitPaths struct {
	RepoDir   string // .git directory (or .git/worktrees/<name>)
	CommonDir string // common git dir (for worktrees)
	HeadPath  string // path to HEAD file
}

// FooterData holds the current footer status information.
type FooterData struct {
	GitBranch       string
	GitDirty        bool
	ExtensionStatus string
	ProviderCount   int
}

// FooterDataChangeFunc is the callback signature for footer data updates.
type FooterDataChangeFunc func(data FooterData)

// ============================================================================
// Git path resolution — mirrors TS findGitPaths (footer-data-provider.ts:16-48)
// ============================================================================

// FindGitPaths walks up from cwd to find the git repository root.
// Returns nil if not inside a git repository.
func FindGitPaths(cwd string) *GitPaths {
	dir := cwd
	for {
		gitDir := filepath.Join(dir, ".git")
		info, err := os.Stat(gitDir)
		if err == nil {
			if info.IsDir() {
				headPath := filepath.Join(gitDir, "HEAD")
				return &GitPaths{
					RepoDir:   gitDir,
					CommonDir: gitDir,
					HeadPath:  headPath,
				}
			}
			// .git is a file — worktree pointing to common dir
			content, err := os.ReadFile(gitDir)
			if err == nil {
				line := strings.TrimSpace(string(content))
				if strings.HasPrefix(line, "gitdir: ") {
					commonDir := strings.TrimPrefix(line, "gitdir: ")
					commonDir = strings.TrimSpace(commonDir)
					if !filepath.IsAbs(commonDir) {
						commonDir = filepath.Join(dir, commonDir)
					}
					headPath := filepath.Join(commonDir, "HEAD")
					return &GitPaths{
						RepoDir:   gitDir,
						CommonDir: commonDir,
						HeadPath:  headPath,
					}
				}
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return nil
}

// ============================================================================
// Git branch resolution — mirrors TS resolveBranchWithGitSync/Async
// ============================================================================

// ResolveBranchSync returns the current git branch name synchronously.
// Mirrors TS resolveBranchWithGitSync (footer-data-provider.ts:51-59).
func ResolveBranchSync(cwd string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ResolveBranchAsync returns the current git branch name asynchronously.
// Mirrors TS resolveBranchWithGitAsync (footer-data-provider.ts:62-81).
func ResolveBranchAsync(ctx context.Context, cwd string, callback func(branch string)) {
	go func() {
		cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
		cmd.Dir = cwd
		out, err := cmd.Output()
		branch := ""
		if err == nil {
			branch = strings.TrimSpace(string(out))
		}
		select {
		case <-ctx.Done():
			return
		default:
			callback(branch)
		}
	}()
}

// ResolveHeadFromFiles reads the branch name directly from the .git/HEAD file.
// Falls back to reading packed-refs if HEAD is a detached hash.
func ResolveHeadFromFiles(gitPaths *GitPaths) string {
	if gitPaths == nil {
		return ""
	}
	content, err := os.ReadFile(gitPaths.HeadPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(content))
	if strings.HasPrefix(line, "ref: refs/heads/") {
		return strings.TrimPrefix(line, "ref: refs/heads/")
	}
	// Detached HEAD — try to find the branch in packed-refs
	packedRefs := filepath.Join(gitPaths.CommonDir, "packed-refs")
	f, err := os.Open(packedRefs)
	if err != nil {
		return "" // detached HEAD, no packed-refs
	}
	defer f.Close()

	hash := line
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		refLine := scanner.Text()
		if strings.HasPrefix(refLine, "#") || refLine == "" {
			continue
		}
		parts := strings.Fields(refLine)
		if len(parts) >= 2 && parts[0] == hash && strings.HasPrefix(parts[1], "refs/heads/") {
			return strings.TrimPrefix(parts[1], "refs/heads/")
		}
	}
	return "" // detached HEAD
}

// IsGitDirty checks if the git working directory has uncommitted changes.
func IsGitDirty(cwd string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// ============================================================================
// FooterDataProvider — mirrors TS FooterDataProvider (footer-data-provider.ts:87-348)
// ============================================================================

// FooterDataProvider provides real-time footer data (git branch, status, etc.)
// for the TUI footer bar. Supports file watching for automatic updates.
type FooterDataProvider struct {
	cwd      string
	gitPaths *GitPaths
	branch   atomic.Value // string
	dirty    atomic.Value // bool

	// extensionStatus holds the latest text set via extension ctx.ui.status.
	// Surfaced on FooterData.ExtensionStatus by GetFooterData. Empty by default.
	extensionStatus atomic.Value // string

	mu       sync.RWMutex
	onChange []FooterDataChangeFunc

	// Watching state
	cancelWatch context.CancelFunc
}

// NewFooterDataProvider creates a new footer data provider for the given cwd.
func NewFooterDataProvider(cwd string) *FooterDataProvider {
	p := &FooterDataProvider{
		cwd:      cwd,
		gitPaths: FindGitPaths(cwd),
	}
	// Initial branch resolution
	if p.gitPaths != nil {
		branch := ResolveHeadFromFiles(p.gitPaths)
		if branch == "" {
			branch = ResolveBranchSync(cwd)
		}
		p.branch.Store(branch)
	}
	return p
}

// GitBranch returns the current git branch name.
func (p *FooterDataProvider) GitBranch() string {
	v := p.branch.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// GitDirty returns whether the working directory has uncommitted changes.
func (p *FooterDataProvider) GitDirty() bool {
	v := p.dirty.Load()
	if v == nil {
		return false
	}
	return v.(bool)
}

// Refresh re-reads git status and notifies listeners.
func (p *FooterDataProvider) Refresh() {
	if p.gitPaths != nil {
		branch := ResolveHeadFromFiles(p.gitPaths)
		if branch == "" {
			branch = ResolveBranchSync(p.cwd)
		}
		p.branch.Store(branch)
	}
	dirty := IsGitDirty(p.cwd)
	p.dirty.Store(dirty)
	p.notify()
}

// OnChange registers a callback for footer data updates.
func (p *FooterDataProvider) OnChange(fn FooterDataChangeFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onChange = append(p.onChange, fn)
}

// Watch starts watching git HEAD and working directory for changes.
// Calls Refresh() on changes. Blocks until context is cancelled.
func (p *FooterDataProvider) Watch(ctx context.Context) {
	if p.gitPaths == nil {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancelWatch = cancel
	p.mu.Unlock()

	// Poll-based watching — can be replaced with fsnotify for efficiency
	for {
		select {
		case <-ctx.Done():
			return
		case <-watchHeadChange(ctx, p.gitPaths.HeadPath):
			p.Refresh()
		}
	}
}

// StopWatch stops any running watch goroutine.
func (p *FooterDataProvider) StopWatch() {
	p.mu.Lock()
	cancel := p.cancelWatch
	p.cancelWatch = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SetExtensionStatus stores the latest status text set by an extension via
// ctx.ui.status. Empty string clears it. Surfaces on FooterData.ExtensionStatus
// via GetFooterData. Used by the interactive ExtensionUIContext.
func (p *FooterDataProvider) SetExtensionStatus(text string) {
	p.extensionStatus.Store(text)
}

// GetExtensionStatus returns the latest extension-set status text ("" if none).
func (p *FooterDataProvider) GetExtensionStatus() string {
	if v := p.extensionStatus.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// notify calls all registered change callbacks.
func (p *FooterDataProvider) notify() {
	data := FooterData{
		GitBranch: p.GitBranch(),
		GitDirty:  p.GitDirty(),
	}
	p.mu.RLock()
	listeners := append([]FooterDataChangeFunc{}, p.onChange...)
	p.mu.RUnlock()

	for _, fn := range listeners {
		fn(data)
	}
}

// watchHeadChange polls the HEAD file for changes. Returns a channel
// that fires when the file content changes or the context is done.
// This is a simple polling implementation — can be replaced with fsnotify.
func watchHeadChange(ctx context.Context, headPath string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		defer close(ch)
		lastContent := ""
		content, _ := os.ReadFile(headPath)
		if content != nil {
			lastContent = string(content)
		}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			content, err := os.ReadFile(headPath)
			if err == nil && string(content) != lastContent {
				lastContent = string(content)
				select {
				case ch <- struct{}{}:
				case <-ctx.Done():
					return
				}
			}
			// Poll every 2 seconds
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return ch
}
