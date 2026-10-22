package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"ycode/packages/coding-agent/src/config"
)

// C8: the asset-name matrix must mirror the reference getAssetName closures
// exactly (tools-manager.ts:38-71) — win msvc zip, apple tar.gz, linux gnu
// tar.gz except ripgrep x86_64 which is musl; unknown platforms reject.
func TestToolAssetNameMatrix(t *testing.T) {
	cases := []struct {
		tool     string
		platform string
		arch     string
		want     string
	}{
		{"fd", "darwin", "amd64", "fd-v1.0.0-x86_64-apple-darwin.tar.gz"},
		{"fd", "darwin", "arm64", "fd-v1.0.0-aarch64-apple-darwin.tar.gz"},
		{"fd", "linux", "amd64", "fd-v1.0.0-x86_64-unknown-linux-gnu.tar.gz"},
		{"fd", "linux", "arm64", "fd-v1.0.0-aarch64-unknown-linux-gnu.tar.gz"},
		{"fd", "win32", "amd64", "fd-v1.0.0-x86_64-pc-windows-msvc.zip"},
		{"fd", "win32", "arm64", "fd-v1.0.0-aarch64-pc-windows-msvc.zip"},
		{"rg", "darwin", "amd64", "ripgrep-1.0.0-x86_64-apple-darwin.tar.gz"},
		{"rg", "darwin", "arm64", "ripgrep-1.0.0-aarch64-apple-darwin.tar.gz"},
		{"rg", "linux", "amd64", "ripgrep-1.0.0-x86_64-unknown-linux-musl.tar.gz"},
		{"rg", "linux", "arm64", "ripgrep-1.0.0-aarch64-unknown-linux-gnu.tar.gz"},
		{"rg", "win32", "amd64", "ripgrep-1.0.0-x86_64-pc-windows-msvc.zip"},
		{"rg", "win32", "arm64", "ripgrep-1.0.0-aarch64-pc-windows-msvc.zip"},
		{"fd", "freebsd", "amd64", ""}, // unsupported → download rejected
		{"rg", "linux", "386", ""},     // unsupported arch → download rejected
	}
	for _, tc := range cases {
		if got := toolTable[tc.tool].assetName("1.0.0", tc.platform, tc.arch); got != tc.want {
			t.Errorf("%s/%s/%s: got %q, want %q", tc.tool, tc.platform, tc.arch, got, tc.want)
		}
	}
}

func TestStripVTag(t *testing.T) {
	if got := stripVTag("v14.1.0"); got != "14.1.0" {
		t.Errorf("got %q, want 14.1.0", got)
	}
	if got := stripVTag("14.1.0"); got != "14.1.0" { // ripgrep tags are bare
		t.Errorf("got %q, want 14.1.0", got)
	}
}

// C8: extraction + the reference's binary-layout probe (asset dir → root →
// recursive DFS) against in-test-built archives — no network.
func TestExtractAndLocateBinary(t *testing.T) {
	binaryBytes := []byte("#!/bin/sh\necho fake-fd\n")
	binaryName := "fd"
	if runtime.GOOS == "windows" {
		binaryName = "fd.exe"
	}

	buildTarGz := func(t *testing.T, layout map[string][]byte) string {
		t.Helper()
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for name, data := range layout {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "asset.tar.gz")
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	buildZip := func(t *testing.T, layout map[string][]byte) string {
		t.Helper()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, data := range layout {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "asset.zip")
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("tar.gz with asset dir", func(t *testing.T) {
		archive := buildTarGz(t, map[string][]byte{
			"fd-v14.1.0-x86_64-unknown-linux-gnu/" + binaryName: binaryBytes,
			"fd-v14.1.0-x86_64-unknown-linux-gnu/README":        []byte("readme"),
		})
		dest := t.TempDir()
		if err := extractArchive(archive, dest); err != nil {
			t.Fatalf("extract: %v", err)
		}
		got, err := locateExtractedBinary(dest, "fd-v14.1.0-x86_64-unknown-linux-gnu", binaryName)
		if err != nil {
			t.Fatalf("locate: %v", err)
		}
		data, _ := os.ReadFile(got)
		if !bytes.Equal(data, binaryBytes) {
			t.Errorf("binary content corrupted")
		}
	})

	t.Run("zip with nested layout needs the DFS fallback", func(t *testing.T) {
		archive := buildZip(t, map[string][]byte{
			"nested/deep/" + binaryName: binaryBytes,
		})
		dest := t.TempDir()
		if err := extractArchive(archive, dest); err != nil {
			t.Fatalf("extract: %v", err)
		}
		got, err := locateExtractedBinary(dest, "fd-v14.1.0-x86_64-pc-windows-msvc", binaryName)
		if err != nil {
			t.Fatalf("locate (DFS fallback): %v", err)
		}
		data, _ := os.ReadFile(got)
		if !bytes.Equal(data, binaryBytes) {
			t.Errorf("binary content corrupted")
		}
	})

	t.Run("missing binary is an error", func(t *testing.T) {
		archive := buildZip(t, map[string][]byte{"only-a-readme.txt": []byte("x")})
		dest := t.TempDir()
		if err := extractArchive(archive, dest); err != nil {
			t.Fatalf("extract: %v", err)
		}
		if _, err := locateExtractedBinary(dest, "whatever", binaryName); err == nil {
			t.Fatal("expected Binary not found error")
		}
	})
}

// C8: resolution order — managed bin dir first, then system PATH (fd also
// tries fdfind, the Debian package name). Driven via the EnvAgentDir override
// so the test never touches the real ~/.ycode.
func TestGetToolPathOrder(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(config.EnvAgentDir, agentDir)
	binDir := filepath.Join(agentDir, "bin")

	// Nothing anywhere: not found.
	t.Setenv("PATH", t.TempDir()) // empty dir on PATH probes nothing
	if got := GetToolPath("fd"); got != "" {
		t.Fatalf("empty env: got %q, want empty", got)
	}

	// Bin dir wins over PATH.
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(binDir, "fd"+binaryExt(runtime.GOOS))
	if err := os.WriteFile(managed, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := GetToolPath("fd"); got != managed {
		t.Fatalf("bin dir first: got %q, want %q", got, managed)
	}

	// PATH probe used when the bin dir copy is gone.
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	pathDir := t.TempDir()
	var systemName string
	if runtime.GOOS == "windows" {
		systemName = filepath.Join(pathDir, "fd.bat")
		if err := os.WriteFile(systemName, []byte("@echo off\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		systemName = filepath.Join(pathDir, "fdfind")
		if err := os.WriteFile(systemName, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", pathDir)
	got := GetToolPath("fd")
	if got == "" {
		t.Fatal("PATH probe failed to find system binary")
	}
	if filepath.Base(got) != filepath.Base(systemName) {
		t.Errorf("PATH probe: got %q, want %q", got, systemName)
	}
}

// C8: the offline gate short-circuits before any network attempt.
func TestEnsureToolOfflineGate(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(config.EnvAgentDir, agentDir)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("YCODE_OFFLINE", "1")

	if got := EnsureTool("rg", true); got != "" {
		t.Fatalf("offline mode: got %q, want empty", got)
	}
	// And the value variants the reference accepts.
	t.Setenv("YCODE_OFFLINE", "true")
	if got := EnsureTool("rg", true); got != "" {
		t.Fatalf("offline mode (true): got %q, want empty", got)
	}
}
