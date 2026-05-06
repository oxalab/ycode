package providers

import (
	"fmt"
	"os"
	"regexp"

	src "ycode/packages/llm/src"
)

const CloudflareWorkersAIBaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"

func IsCloudflareProvider(provider string) bool {
	return provider == string(src.CloudflareWorkerAI)
}

var envPlaceholderRe = regexp.MustCompile(`\{([A-Z_][A-Z0-9_]*)\}`)

func ResolveCloudflareBaseUrl(model src.Model[src.Api]) string {
	u := model.BaseURL
	if !envPlaceholderRe.MatchString(u) {
		return u
	}
	return envPlaceholderRe.ReplaceAllStringFunc(u, func(match string) string {
		name := envPlaceholderRe.FindStringSubmatch(match)[1]
		value := os.Getenv(name)
		if value == "" {
			panic(fmt.Sprintf("%s is required for provider %s but is not set", name, model.Provider))
		}
		return value
	})
}
