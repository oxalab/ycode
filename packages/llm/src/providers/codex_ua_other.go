//go:build !windows

package providers

import (
	"fmt"
	"runtime"
)

// codexUserAgent mirrors openai-codex-responses.ts:
// `pi (${os.platform()} ${os.release()}; ${os.arch()})`. The OS release
// number is only resolved on Windows (node's os.release has no portable
// pure-Go equivalent elsewhere).
func codexUserAgent() string {
	return fmt.Sprintf("pi (%s; %s)", runtime.GOOS, codexArch())
}

func codexArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}
