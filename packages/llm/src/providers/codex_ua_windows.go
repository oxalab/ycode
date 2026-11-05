//go:build windows

package providers

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

// codexUserAgent mirrors openai-codex-responses.ts:
// `pi (${os.platform()} ${os.release()}; ${os.arch()})`.
func codexUserAgent() string {
	return fmt.Sprintf("pi (%s %s; %s)", codexPlatform(), codexOSRelease(), codexArch())
}

// codexOSRelease returns the Windows version number as node's os.release()
// does (e.g. "10.0.26200").
func codexOSRelease() string {
	v := windows.RtlGetVersion()
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

func codexPlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func codexArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}
