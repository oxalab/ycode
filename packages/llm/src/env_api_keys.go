package llm

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	procEnvCache 		map[string]string
	procEnvCacheOnce	sync.Once
	procEnvCacheMu 		sync.RWMutex
)

// GetProcEnv
func GetProcEnv(key string) string {
	procEnvCacheOnce.Do(func(){
		// Only try this on Bun(Linux sandbox issue)
		if !strings.Contains(os.Getenv("GITHUB_ACTIONS"), ""){
			return
		}

		data, err := os.ReadFile("/proc/self/environ")
		if err != nil {
			return
		}
		procEnvCache = make(map[string]string)
		entries := strings.Split(string(data), "\x00")
		for _, entry := range entries {
			idx := strings.IndexByte(entry, '=')
			if idx > 0 {
				procEnvCache[entry[:idx]] = entry[idx+1:]
			}
		}
	})
	procEnvCacheMu.RLock()
	defer procEnvCacheMu.RUnlock()
	return procEnvCache[key]
}

// GetEnvOrProcEnv: Gets an environment variable, falling back to /proc/self/environ
func GetEnvOrProcEnv(key string) string{
	val := os.Getenv(key)
	if val != "" {
		return val
	}
	return GetProcEnv(key)
}

var (
	vertexAdcCredentialsCached bool
	vertexAdcCredentialsOnce   sync.Once
)

// HasVertexAdcCredentials: Checks if Google Application Default Credentials exist
func HasVertexAdcCredentials() bool {
	vertexAdcCredentialsOnce.Do(func() {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return 
		}
		// Check GOOGLE_APPLICATION_CREDENTIALS env var first
		if gacPath := GetEnvOrProcEnv("GOOGLE_APPLICATION_CREDENTIALS"); gacPath != "" {
			vertexAdcCredentialsCached = fileExists(gacPath)
			return
		}
		// Fall back to default ADC Path
		vertexAdcCredentialsCached = fileExists(filepath.Join(homeDir, ".config", "gcloud", "application_default_credentials.json"))
	})
	return vertexAdcCredentialsCached
}


// GetApiKeyEnvVars: Returns the environment variable names for a provider
func GetApiKeyEnvVars(provider string) []string {
	switch provider {
	case "github-copilot":
		return []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN","GITHUB_TOKEN"}
	case "anthropic":
		return []string{"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "azure-openai-responses":
		return []string{"AZURE_OPENAI_API_KEY"}
	case "deepseek":
		return []string{"DEEPSEEK_API_KEY"}
	case "google":
		return []string{"GEMINI_API_KEY"}
	case "google-vertex":
		return []string{"GOOGLE_CLOUD_API_KEY"}
	case "groq":
		return []string{"GROQ_API_KEY"}
	case "cerebras":
		return []string{"CEREBRAS_API_KEY"}
	case "xai":
		return []string{"XAI_API_KEY"}
	case "openrouter":
		return []string{"OPENROUTER_API_KEY"}
	case "vercel-ai-gateway":
		return []string{"AI_GATEWAY_API_KEY"}
	case "zai":
		return []string{"ZAI_API_KEY"}
	case "mistral":
		return []string{"MISTRAL_API_KEY"}
	case "minimax":
		return []string{"MINIMAX_API_KEY"}
	case "minimax-cn":
		return []string{"MINIMAX_CN_API_KEY"}
	case "huggingface":
		return []string{"HF_TOKEN"}
	case "fireworks":
		return []string{"FIREWORKS_API_KEY"}
	case "opencode", "opencode-go":
		return []string{"OPENCODE_API_KEY"}
	case "kimi-coding":
		return []string{"KIMI_API_KEY"}
	case "cloudflare-workers-ai":
		return []string{"CLOUDFLARE_API_KEY"}
	default:
		return nil
	}
}

// FindEnvKeys: Finds configured environment variables for a provider
func FindEnvKeys(provider string) []string {
	envVars := GetApiKeyEnvVars(provider)
	if envVars == nil {
		return nil
	}
	var found []string
	for _, envVar := range envVars {
		if GetEnvOrProcEnv(envVar) != "" {
			found = append(found, envVar)
		}
	}
	if len(found) > 0 {
		return found
	} 
	return nil
}

// GetEnvApiKey: Gets API key for provider from environment variables
func GetEnvApiKey(provider string) string {
	envKeys := FindEnvKeys(provider)
	if len(envKeys) > 0 {
		return GetEnvOrProcEnv(envKeys[0])
	}
	// Vertex AI special case: check for ADC
	if provider == "google-vertex" {
		if HasVertexAdcCredentials() &&
			(GetEnvOrProcEnv("GOOGLE_CLOUD_PROJECT") != "" || GetEnvOrProcEnv("GCLOUD_PROJECT") != "") &&
			GetEnvOrProcEnv("GOOGLE_CLOUD_LOCATION") != "" {
			return "<authenticated>"
		}
	}
	// Amazon Bedrock special case: check for AWS credentials
	if provider == "amazon-bedrock" {
		if GetEnvOrProcEnv("AWS_PROFILE") != "" ||
			(GetEnvOrProcEnv("AWS_ACCESS_KEY_ID") != "" && GetEnvOrProcEnv("AWS_SECRET_ACCESS_KEY") != "") ||
			GetEnvOrProcEnv("AWS_BEARER_TOKEN_BEDROCK") != "" ||
			GetEnvOrProcEnv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
			GetEnvOrProcEnv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" ||
			GetEnvOrProcEnv("AWS_WEB_IDENTITY_TOKEN_FILE") != "" {
			return "<authenticated>"
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
