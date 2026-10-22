package tools

// tools_manager.go ports pi packages/coding-agent/src/utils/tools-manager.ts:
// ensureTool — auto-download fd/rg from GitHub releases when neither the
// managed bin dir (~/.ycode/agent/bin, config.GetBinDir) nor the system PATH
// has them.
//
// Port decisions (user-locked, story 20):
//   - Bug-for-bug parity on integrity: no checksums, no version pinning
//     (always releases/latest) — HTTPS transport to github.com is the only
//     guarantee, exactly like the reference.
//   - Extraction uses Go stdlib (archive/tar + compress/gzip, archive/zip)
//     where the reference shells to system tar / npm extract-zip; same
//     archive layouts, no external dependency.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ycode/packages/coding-agent/src/config"
)

const (
	toolsNetworkTimeout  = 10 * time.Second
	toolsDownloadTimeout = 120 * time.Second
)

type toolConfig struct {
	name              string   // display name ("fd" / "ripgrep")
	repo              string   // GitHub repo
	binaryName        string   // binary name inside the archive
	systemBinaryNames []string // PATH probes before downloading (fd also tries fdfind)
	tagPrefix         string   // "v" for fd; ripgrep tags are bare numbers
}

var toolTable = map[string]toolConfig{
	"fd": {name: "fd", repo: "sharkdp/fd", binaryName: "fd", systemBinaryNames: []string{"fd", "fdfind"}, tagPrefix: "v"},
	"rg": {name: "ripgrep", repo: "BurntSushi/ripgrep", binaryName: "rg", systemBinaryNames: []string{"rg"}, tagPrefix: ""},
}

// assetName mirrors the reference getAssetName closures (tools-manager.ts:38-71):
// win = msvc zip, darwin = apple tar.gz, linux = gnu tar.gz except
// ripgrep x86_64 which is musl. Unknown platforms return "".
func (c toolConfig) assetName(version, platform, arch string) string {
	archStr := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[arch]
	if archStr == "" {
		return ""
	}
	switch c.name {
	case "fd":
		switch platform {
		case "darwin":
			return fmt.Sprintf("fd-v%s-%s-apple-darwin.tar.gz", version, archStr)
		case "linux":
			return fmt.Sprintf("fd-v%s-%s-unknown-linux-gnu.tar.gz", version, archStr)
		case "win32":
			return fmt.Sprintf("fd-v%s-%s-pc-windows-msvc.zip", version, archStr)
		}
	case "ripgrep":
		switch platform {
		case "darwin":
			return fmt.Sprintf("ripgrep-%s-%s-apple-darwin.tar.gz", version, archStr)
		case "linux":
			if arch == "arm64" {
				return fmt.Sprintf("ripgrep-%s-aarch64-unknown-linux-gnu.tar.gz", version)
			}
			return fmt.Sprintf("ripgrep-%s-x86_64-unknown-linux-musl.tar.gz", version)
		case "win32":
			return fmt.Sprintf("ripgrep-%s-%s-pc-windows-msvc.zip", version, archStr)
		}
	}
	return ""
}

func platformKey(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func binaryExt(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// offlineModeEnabled mirrors the reference's PI_OFFLINE gate (1/true/yes) with
// the Go env var YCODE_OFFLINE. (core.isOfflineModeEnabled serves the package
// manager; package tools cannot import core.)
func offlineModeEnabled() bool {
	value := os.Getenv("YCODE_OFFLINE")
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// GetToolPath returns the path to a tool binary: the managed bin dir first
// (flat layout, .exe suffix on Windows, existence check only — no versioned
// dirs, like the reference), then system PATH probes (fd also tries fdfind).
// Returns "" when unavailable.
func GetToolPath(tool string) string {
	cfg, ok := toolTable[tool]
	if !ok {
		return ""
	}
	localPath := filepath.Join(config.GetBinDir(), cfg.binaryName+binaryExt(runtime.GOOS))
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}
	for _, name := range cfg.systemBinaryNames {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// EnsureTool makes fd/rg available, downloading from GitHub releases when
// missing (reference ensureTool). Returns the binary path or "" when
// unavailable; never errors. silent=true (tool-execution paths) suppresses
// the console notices — interactive startup calls it non-silent before the
// TUI takes over the console.
func EnsureTool(tool string, silent bool) string {
	cfg, ok := toolTable[tool]
	if !ok {
		return ""
	}
	if path := GetToolPath(tool); path != "" {
		return path
	}

	// Offline and Termux gates apply regardless of silent (reference
	// ensureTool) — only the notices are silenced.
	if offlineModeEnabled() {
		if !silent {
			fmt.Printf("%s not found. Offline mode enabled, skipping download.\n", cfg.name)
		}
		return ""
	}
	if runtime.GOOS == "android" {
		// glibc binaries won't run under Bionic (reference TERMUX_PACKAGES).
		if !silent {
			fmt.Printf("%s not found. Install with: pkg install %s\n", cfg.name, cfg.name)
		}
		return ""
	}
	if !silent {
		fmt.Printf("%s not found. Downloading...\n", cfg.name)
	}

	path, err := downloadTool(tool)
	if err != nil {
		if !silent {
			fmt.Printf("Failed to download %s: %v\n", cfg.name, err)
		}
		return ""
	}
	if !silent {
		fmt.Printf("%s installed to %s\n", cfg.name, path)
	}
	return path
}

// stripVTag normalizes a release tag to a bare version (fd tags are v-prefixed,
// ripgrep tags are bare — same normalizer for both, like the reference).
func stripVTag(tag string) string {
	return strings.TrimPrefix(tag, "v")
}

func latestToolVersion(repo string) (string, error) {
	client := &http.Client{Timeout: toolsNetworkTimeout}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ycode-coding-agent")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}
	var data struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return stripVTag(data.TagName), nil
}

func downloadToolFile(url, dest string) error {
	client := &http.Client{Timeout: toolsDownloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Failed to download: %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func downloadTool(tool string) (string, error) {
	cfg, ok := toolTable[tool]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tool)
	}
	version, err := latestToolVersion(cfg.repo)
	if err != nil {
		return "", err
	}
	goos := platformKey(runtime.GOOS)
	asset := cfg.assetName(version, goos, runtime.GOARCH)
	if asset == "" {
		return "", fmt.Errorf("Unsupported platform: %s/%s", goos, runtime.GOARCH)
	}

	binDir := config.GetBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s%s/%s", cfg.repo, cfg.tagPrefix, version, asset)
	archivePath := filepath.Join(binDir, asset)
	binaryPath := filepath.Join(binDir, cfg.binaryName+binaryExt(runtime.GOOS))

	if err := downloadToolFile(downloadURL, archivePath); err != nil {
		return "", err
	}

	// Unique per-call extract dir: fd and rg downloads can run concurrently
	// during startup, so sharing a fixed directory causes races (reference
	// downloadTool).
	extractDir := filepath.Join(binDir, fmt.Sprintf("extract_tmp_%s_%d_%s", cfg.binaryName, os.Getpid(), randHex()))
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(extractDir)
	defer os.Remove(archivePath)

	if err := extractArchive(archivePath, extractDir); err != nil {
		return "", err
	}
	assetBase := strings.TrimSuffix(strings.TrimSuffix(asset, ".zip"), ".tar.gz")
	extracted, err := locateExtractedBinary(extractDir, assetBase, cfg.binaryName+binaryExt(runtime.GOOS))
	if err != nil {
		return "", err
	}
	if err := os.Rename(extracted, binaryPath); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			return "", err
		}
	}
	return binaryPath, nil
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func extractArchive(archivePath, destDir string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"):
		return extractTarGz(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZip(archivePath, destDir)
	}
	return fmt.Errorf("Unsupported archive format: %s", filepath.Base(archivePath))
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, ok := safeExtractPath(destDir, header.Name)
		if !ok {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, zf := range r.File {
		target, ok := safeExtractPath(destDir, zf.Name)
		if !ok {
			continue
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := zf.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			src.Close()
			return err
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			src.Close()
			return err
		}
		out.Close()
		src.Close()
	}
	return nil
}

// safeExtractPath joins an archive member name under destDir, rejecting paths
// that escape it (zip-slip guard; stdlib extraction detail the reference gets
// for free from tar/extract-zip).
func safeExtractPath(destDir, name string) (string, bool) {
	target := filepath.Join(destDir, filepath.Clean("/"+filepath.ToSlash(name)))
	rel, err := filepath.Rel(destDir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return target, true
}

// locateExtractedBinary mirrors the reference layout probe: under a directory
// named for the asset, at the extract root, then a recursive fallback.
func locateExtractedBinary(extractDir, assetBase, binaryFileName string) (string, error) {
	candidates := []string{
		filepath.Join(extractDir, assetBase, binaryFileName),
		filepath.Join(extractDir, binaryFileName),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	var found string
	_ = filepath.WalkDir(extractDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !d.IsDir() && d.Name() == binaryFileName {
			found = p
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("Binary not found in archive: expected %s under %s", binaryFileName, extractDir)
	}
	return found, nil
}
