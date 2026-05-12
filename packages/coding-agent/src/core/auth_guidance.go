package ycode

import (
	"fmt"
	"path/filepath"
)

func GetProviderLoginHelp() string {
	return "Use /login to log into a provider via OAuth or API Key. See:\n" +
		" " + filepath.Join(GetDocsPath(), "providers.md") + "\n" +
		" " + filepath.Join(GetDocsPath(), "models.md")
}

func FormatNoModelIsAvailableMessage() string {
	return "No models available. " + GetProviderLoginHelp()
}

func FormatNoModelSelectedMessage() string {
	return "No model selected.\n\n" + GetProviderLoginHelp() + "\n\nThen use /model to select a model."
}

func FormatNoApiKeyFoundMessage(provider string) string {
	providerDisplay := provider
	if provider == "unknown" {
		providerDisplay = "the selected model"
	}
	return fmt.Sprintf("No API key found for %s.\n\n%s", providerDisplay, GetProviderLoginHelp())
}