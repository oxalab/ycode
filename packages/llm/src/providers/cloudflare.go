package providers

import (
	"fmt"
	"os"
	"regexp"

	src "ycode/packages/llm/src"
)

const CloudflareWorkersAIBaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"

// IsCloudflareProvider matches the reference (cloudflare.ts:19-21): Workers AI
// direct and the AI Gateway unified API.
func IsCloudflareProvider(provider string) bool {
	return provider == string(src.CloudflareWorkerAI) || provider == "cloudflare-ai-gateway"
}

var envPlaceholderRe = regexp.MustCompile(`\{([A-Z_][A-Z0-9_]*)\}`)

// ResolveCloudflareBaseUrl substitutes {VAR} placeholders from the environment.
// A missing variable is an error (the reference throws a catchable Error).
func ResolveCloudflareBaseUrl(model src.Model[src.Api]) (string, error) {
	u := model.BaseURL
	if !envPlaceholderRe.MatchString(u) {
		return u, nil
	}
	resolved := u
	var missingErr error
	resolved = envPlaceholderRe.ReplaceAllStringFunc(u, func(match string) string {
		name := envPlaceholderRe.FindStringSubmatch(match)[1]
		value := os.Getenv(name)
		if value == "" {
			missingErr = fmt.Errorf("%s is required for provider %s but is not set.", name, model.Provider)
			return match
		}
		return value
	})
	if missingErr != nil {
		return "", missingErr
	}
	return resolved, nil
}
