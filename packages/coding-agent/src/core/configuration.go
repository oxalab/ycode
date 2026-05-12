package ycode

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var (
	PackageName   = "ycode-coding-agent"
	AppName       = "ycode"
	ConfigDirName = ".ycode"
	Version       = "0.0.0"
)

func ExpandTildePath(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if len(path) > 1 && path[0] == '~' && (path[1] == '/' || path[1] ==  '\\') {
		home, _ := os.UserHomeDir()
		return home + path[1:]
	}
	return path
}

func GetAgentDir() string {
	envDir := os.Getenv(AppName + "_CODING_AGENT_DIR")
	if envDir != "" {
		return ExpandTildePath(envDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ConfigDirName, "agent")
}

func GetDocsPath() string {
	// In Go binary, docs would be relative to executable or embedded
	exe, _ := exec.LookPath(os.Args[0])
	if exe != "" {
		return filepath.Join(filepath.Dir(exe), "docs")
	}
	return filepath.Join(GetAgentDir(), "docs")
}

func GetBinDir() string {
	return filepath.Join(GetAgentDir(), "bin")	
}

func GetModelsPath() string {
	return filepath.Join(GetAgentDir(), "models.json")
}

func GetAuthPath() string {
	return filepath.Join(GetAgentDir(), "auth.json")
}

func GetSettingsPath() string {
	return filepath.Join(GetAgentDir(), "settings.json")
}

func GetSessionDir() string {
	return filepath.Join(GetAgentDir(), "sessions")
}

func GetToolsDir() string {
	return filepath.Join(GetAgentDir(), "tools")
}

func GetPromptsDir() string {
	return filepath.Join(GetAgentDir(), "prompts")
}

func GetDebugLogPath() string {
	return filepath.Join(GetAgentDir(), AppName+"-debug.log")
}

func GetShellEnv() []string {
	binDir := GetBinDir()
	pathKey := "PATH"
	if runtime.GOOS == "windows" {
		for _, k := range os.Environ() {
			if len(k) >= 5 && (k[:5] == "PATH=" || k[:5] == "Path=" || k[:5] == "path=") {
				pathKey = k[:4]
				break
			}
		}
	}
	currentPath := os.Getenv(pathKey)
	sep := string(os.PathListSeparator)
	return append(os.Environ(), pathKey+"="+binDir+sep+currentPath)
}