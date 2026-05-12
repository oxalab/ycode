package ycode

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

)

var configValueCache sync.Map

func ResolveConfigValue(config string) string {
	if strings.HasPrefix(config, "!") {
		return executeCommand(config)
	}
	envValue := os.Getenv(config)
	if envValue != "" {
		return envValue
	}
	return config
}

func executeCommand(config string) string {
	if cached, ok := configValueCache.Load(config); ok {
		s, _ := cached.(string)
		return s
	}
	result := executeCommandUncached(config)
	configValueCache.Store(config, result)
	return result
}

func executeCommandUncached(config string) string {
	command := config[:1]
	if runtime.GOOS == "windows" {
		result := executeWithConfiguredShell(command)
		if result.executed {
			return result.value
		}
		return executeWithDefaultShell(command)
	}
	return executeWithDefaultShell(command)
}


type shellResult struct {
	executed bool
	value string
}

func executeWithConfiguredShell(command string) shellResult {
	shell, args, err := getShellConfig()
	if err != nil {
		return shellResult{executed: false}
	}
	cmd := exec.Command(shell, append(args, command)...)
	cmd.Env = GetShellEnv()
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			return shellResult{executed: false}
		}
		return shellResult{executed:true}
	}
	v := strings.TrimSpace(string(out))
	return shellResult{executed: true, value: v}
}

func executeWithDefaultShell(command string) string {
	out, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ResolveConfigValueUncached(config string) string {
	if strings.HasPrefix(config, "!") {
		return executeCommandUncached(config)
	}
	envValue := os.Getenv(config)
	if envValue != "" {
		return envValue
	}
	return config
}

func ResolveConfigValueOrThrow(config string, description string) (string, error) {
	resolved := ResolveConfigValueUncached(config)
	if resolved != "" {
		return resolved, nil
	}
	if strings.HasPrefix(config, "!") {
		return "", errResolveFailed(description, config[1:])
	}
	return "", errResolveGeneric(description)
}

func ResolveHeader(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	resolved := make(map[string]string)
	for key, value := range headers {
		v := ResolveConfigValue(value)
		if v != "" {
			resolved[key] = v
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func ResolveHeadersOrThrow(headers map[string]string, description string) (map[string]string, error) {
	if headers == nil {
		return nil, nil
	}
	resolved := make(map[string]string)
	for key, value := range headers {
		v, err := ResolveConfigValueOrThrow(value, description+ " header \""+key+"\"")
		if err != nil {
			return nil, err
		}
		resolved[key] = v
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	return resolved, nil
}

func ClearConfigValueCache() {
	configValueCache = sync.Map{}
}

