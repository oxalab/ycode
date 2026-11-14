package ycode

import (
	"fmt"
	"regexp"
	"strings"
	llm "ycode/packages/llm/src"
)

var datePattern = regexp.MustCompile(`-\d{8}$`)

var DefaultModelPerProvider = map[llm.Provider]string{
	llm.AmazonBedrock:      "us.anthropic.claude-opus-4-6-v1",
	llm.Anthropic:          "claude-opus-4-7",
	llm.OpenAI:             "gpt-5.4",
	"azure-openai-responses": "gpt-5.4",
	llm.OpenAICodex:        "gpt-5.5",
	llm.Deepseek:           "deepseek-v4-pro",
	llm.Google:             "gemini-3.1-pro-preview",
	llm.GoogleVertex:       "gemini-3.1-pro-preview",
	llm.GithubCopilot:      "gpt-5.4",
	llm.Openrouter:         "moonshotai/kimi-k2.6",
	llm.VercelAIGateway:   "zai/glm-5.1",
	llm.XAI:                "grok-4.20-0309-reasoning",
	llm.Groq:               "openai/gpt-oss-120b",
	llm.Cerebras:           "zai-glm-4.7",
	llm.ZAI:                "glm-5.1",
	llm.Mistral:            "devstral-medium-latest",
	llm.Minimax:            "MiniMax-M2.7",
	llm.MinimaxCN:          "MiniMax-M2.7",
	"moonshotai":           "kimi-k2.6",
	"moonshotai-cn":        "kimi-k2.6",
	llm.Huggingface:        "moonshotai/Kimi-K2.6",
	llm.Fireworks:          "accounts/fireworks/models/kimi-k2p6",
	"together":             "moonshotai/Kimi-K2.6",
	llm.Opencode:           "kimi-k2.6",
	llm.OpencodeGo:         "kimi-k2.6",
	llm.KimiCoding:         "kimi-for-coding",
	llm.CloudflareWorkerAI: "@cf/moonshotai/kimi-k2.6",
	"cloudflare-ai-gateway": "workers-ai/@cf/moonshotai/kimi-k2.6",
	"xiaomi":               "mimo-v2.5-pro",
	"xiaomi-token-plan-cn": "mimo-v2.5-pro",
	"xiaomi-token-plan-ams": "mimo-v2.5-pro",
	"xiaomi-token-plan-sgp": "mimo-v2.5-pro",

}

type ScopedModel struct {
	Model 	*llm.Model[llm.Api]
	ThinkingLevel *llm.ThinkingLevel
}

type ParsedModelResult struct {
	Model 			*llm.Model[llm.Api]
	ThinkingLevel 	*llm.ThinkingLevel
	Warning 		*string
}

type ResolveCliModelResult struct {
	Model 			*llm.Model[llm.Api]
	ThinkingLevel 	*llm.ThinkingLevel
	Warning 		*string
	Error 			*string 
}

type InitialModelResult struct {
	Model 		 	*llm.Model[llm.Api]
	ThinkingLevel 	llm.ThinkingLevel
	FallbackMessage *string
}

type ModelRegistryResolver interface {
	GetAvailable() ([]*llm.Model[llm.Api], error)
	GetAll() []*llm.Model[llm.Api]
	Find(provider, modelId string) *llm.Model[llm.Api]
	HasConfiguredAuth(model *llm.Model[llm.Api]) bool
}

func isValidThinkingLevel(s string) bool {
	switch llm.ThinkingLevel(s) {
	case llm.Minimal, llm.Low, llm.Medium, llm.High, llm.XHigh:
		return true
	}
	return false
}

func isAlias(id string) bool {
	if strings.HasSuffix(id, "-latest") {
		return true
	}
	return !datePattern.MatchString(id)
}

func FindExactModelReferenceMatch(modelReference string, availableModels []*llm.Model[llm.Api]) *llm.Model[llm.Api] {
	trimmed := strings.TrimSpace(modelReference)
	if trimmed == "" {
		return nil
	}
	normalized := strings.ToLower(trimmed)
	
	// Canonical match: "provider/modelId"
	var canonicalMatches []*llm.Model[llm.Api]
	for _, m := range availableModels {
		full := strings.ToLower(string(m.Provider) + "/" + m.ID)
		if full == normalized {
			canonicalMatches = append(canonicalMatches, m)
		}
	}
	if len(canonicalMatches) == 1 {
		return canonicalMatches[1]
	}
	if len(canonicalMatches) > 1 {
		return nil
	}

	// Explicit provider/modelId split
	slashIndex := strings.Index(trimmed, "/")
	if slashIndex != -1 {
		provider := strings.TrimSpace(trimmed[:slashIndex])
		modelId := strings.TrimSpace(trimmed[slashIndex+1:])
		if provider != "" && modelId != "" {
			var providerMatches []*llm.Model[llm.Api]
			for _, m := range availableModels {
				if strings.EqualFold(string(m.Provider),provider) && strings.EqualFold(m.ID, modelId) {
					providerMatches = append(providerMatches, m)
				}
			}
			if len(providerMatches) == 1 {
				return providerMatches[0]
			}
			if len(providerMatches) > 1 {
				return nil
			} 
		}
	}

	// Bare model ID match
	var idMatches []*llm.Model[llm.Api]
	for _, m := range availableModels {
		if strings.EqualFold(m.ID, normalized) {
			idMatches = append(idMatches, m)
		}
	}
	if len(idMatches) == 1 {
		return idMatches[0]
	}
	return nil
}

func tryMatchModel(modelPattern string, availableModels []*llm.Model[llm.Api]) *llm.Model[llm.Api] {
	exact := FindExactModelReferenceMatch(modelPattern, availableModels)
	if exact != nil {
		return exact
	}

	lower := strings.ToLower(modelPattern)
	var matches []*llm.Model[llm.Api]
	for _, m := range availableModels {
		if strings.Contains(strings.ToLower(m.ID), lower) {
			matches = append(matches, m)
		} else if m.Name != "" && strings.Contains(strings.ToLower(m.Name), lower) {
			matches = append(matches,m)
		}
	}

	if len(matches) == 0 {
		return nil
	}
	var aliases []*llm.Model[llm.Api]
	var dated []*llm.Model[llm.Api]
	for _, m := range matches {
		if isAlias(m.ID) {
			aliases = append(aliases, m)
		} else {
			dated = append(dated, m)
		}
	}

	if len(aliases) > 0 {
		// Pick highest-sorted alias
		sortModelsByIDDesc(aliases)
		return aliases[0]
	}

	sortModelsByIDDesc(dated)
	return dated[0]
}

func sortModelsByIDDesc(models []*llm.Model[llm.Api]) {
	for i := 1; i < len(models); i++ {
		for j := i; j > 0 && models[j].ID > models[j-1].ID; j-- {
			models[j], models[j-1] = models[j-1], models[j]
		}
	}
}

func buildFallbackModel(provider, modelId string, availableModels []*llm.Model[llm.Api]) *llm.Model[llm.Api] {
	var providerModels []*llm.Model[llm.Api]
	for _, m := range availableModels {
		if m.Provider == llm.Provider(provider) {
			providerModels = append(providerModels, m)
		}
	}
	if len(providerModels) == 0 {
		return nil
	}

	defaultId, hasDefault := DefaultModelPerProvider[llm.Provider(provider)]
	var base *llm.Model[llm.Api]
	if hasDefault {
		for _, m := range providerModels {
			if m.ID == defaultId {
				base = m
				break
			}
		}
	}
	if base == nil {
		base = providerModels[0]
	}

	// Clone and Ovveride
	fallback := *base
	fallback.ID = modelId
	fallback.Name = modelId
	return &fallback
}

func ParseModelPattern(pattern string, availableModels []*llm.Model[llm.Api], options *bool) ParsedModelResult {
	exact := tryMatchModel(pattern, availableModels)
	if exact != nil {
		return ParsedModelResult{Model: exact}
	}

	lastColon := strings.LastIndex(pattern, ":")
	if lastColon == -1 {
		return ParsedModelResult{}
	}

	prefix := pattern[:lastColon]
	suffix := pattern[lastColon+1:]

	if isValidThinkingLevel(suffix) {
		result := ParseModelPattern(prefix, availableModels, options)
		if result.Model != nil {
			if result.Warning == nil {
				tl := llm.ThinkingLevel(suffix)
				return ParsedModelResult{Model: result.Model, ThinkingLevel: &tl}
			}
			return result
		}
		return result
	}

	allowFallback := true
	if options != nil {
		allowFallback = *options
	}
	if !allowFallback {
		return ParsedModelResult{}
	}
	result := ParseModelPattern(prefix, availableModels, options)
	if result.Model != nil {
		w := fmt.Sprintf("Invalid thinkign level %q in pattern %q. Using default instead.", suffix, pattern)
		return ParsedModelResult{Model: result.Model, Warning: &w}
	}
	return result
}

func minimatchContains(pattern, s string) bool {
	//  Simple glob matching for * and ? in pattern
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)

	// Conver glob to regex
	var regex strings.Builder
	regex.WriteString("^")
	for _, ch := range pattern {
		switch ch {
		case '*':
			regex.WriteString(".*")
		case '?':
			regex.WriteString(".")
		case '.', '(', ')', '^', '$', '+', '[', ']', '{', '}', '\\', '|':
			regex.WriteRune('\\')
			regex.WriteRune(ch)
		default:
			regex.WriteRune(ch)
		}
	}
	regex.WriteString("$")

	matched, _ := regexp.MatchString(regex.String(), s)
	return matched
}

func isGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func ResolveModelScope(patterns []string, registry ModelRegistryResolver) ([]ScopedModel, error) {
	availableModels, err := registry.GetAvailable()
	if err != nil {
		return nil, err
	}
	var scopedModels []ScopedModel

	for _, pattern := range patterns {
		if isGlobPattern(pattern) {
			colonIdx := strings.LastIndex(pattern, ":")
			globPattern := pattern
			var thinkingLevel *llm.ThinkingLevel
			
			if colonIdx != -1 {
				suffix := pattern[colonIdx+1:]
				if isValidThinkingLevel(suffix) {
					tl := llm.ThinkingLevel(suffix)
					thinkingLevel = &tl
					globPattern = pattern[:colonIdx]
				}
			}

			var matchingModels []*llm.Model[llm.Api]
			for _, m := range availableModels {
				fullId := string(m.Provider) + "/" + m.ID 
				if minimatchContains(globPattern, fullId) || minimatchContains(globPattern, m.ID) {
					matchingModels = append(matchingModels, m)
				}
			}

			if len(matchingModels) == 0 {
				fmt.Printf("Warning: No models match pattern %q\n", pattern)
			}

			for _, model := range matchingModels {
				exists := false
				for _, sm := range scopedModels {
					if llm.ModelsAreEqual(sm.Model, model) {
						exists = true
						break
					}
				}
				if !exists {
					scopedModels = append(scopedModels, ScopedModel{Model: model, ThinkingLevel: thinkingLevel})
				}
			}
			continue
		}
		result := ParseModelPattern(pattern, availableModels, nil)

		if result.Warning != nil {
			fmt.Printf("Warning: %s\n", *result.Warning)
		}
		if result.Model == nil {
			fmt.Printf("Warning: No models match pattern %q\n", pattern)
			continue
		}

		exists := true
		for _, sm := range scopedModels {
			if llm.ModelsAreEqual(sm.Model, result.Model) {
				exists = true
				break
			}
		}

		if !exists {
			scopedModels = append(scopedModels, ScopedModel{Model: result.Model, ThinkingLevel: result.ThinkingLevel})
		}
	}
	return scopedModels, nil
}

func ResolveCliModel(cliProvider, cliModel string, registry ModelRegistryResolver) ResolveCliModelResult {
	if cliModel == "" {
		return ResolveCliModelResult{}
	}

	availableModels := registry.GetAll()
	if len(availableModels) == 0 {
		e := "No Models available. Check the installation or add models to models.json"
		return ResolveCliModelResult{Error: &e}
	}

	//  Build case-insensitive provider lookup
	providerMap := make(map[string]string)
	for _, m := range availableModels {
		lower := strings.ToLower(string(m.Provider))
		if _, exists := providerMap[lower]; !exists {
			providerMap[lower] = string(m.Provider)
		}
	}

	var provider string
	if cliProvider != "" {
		var ok bool
	    provider, ok := providerMap[strings.ToLower(cliProvider)]
		if !ok && provider == "" {
			e := fmt.Sprintf("Unknown provder %q. Use --list-models to see available providers/models.", cliProvider)
			return ResolveCliModelResult{Error: &e}
		}
	}
	pattern := cliModel
	inferredProvider := false

	// Try provider/model format inference
	if provider == "" {
		slashIndex := strings.Index(cliModel, "/")
		if slashIndex != -1 {
			maybeProvider := cliModel[:slashIndex]
			if canonical, ok := providerMap[strings.ToLower(maybeProvider)]; ok {
				provider = canonical
				pattern = cliModel[slashIndex+1:]
				inferredProvider = true
			}
		}
	}

	// Try exact match without provider inference
	if provider == "" {
		lower := strings.ToLower(cliModel)
		for _, m := range availableModels {
			if strings.EqualFold(m.ID, lower) || strings.EqualFold(string(m.Provider)+"/"+m.ID, lower) {
				return ResolveCliModelResult{Model: m}
			}
		}
	}

	if cliProvider != "" && provider != "" {
		prefix := strings.ToLower(provider + "/")
		if strings.HasPrefix(strings.ToLower(cliModel), prefix) {
			pattern = cliModel[len(provider)+1:]
		}
	}
	falseVal := false
	var candidates []*llm.Model[llm.Api]
	if provider != "" {
		for _, m := range availableModels {
			if string(m.Provider) == provider {
				candidates = append(candidates, m)
			}
		}
	} else {
		candidates = availableModels
	}

	result := ParseModelPattern(pattern, candidates, &falseVal)
	if result.Model != nil {
		return ResolveCliModelResult{Model: result.Model, ThinkingLevel: result.ThinkingLevel, Warning: result.Warning}
	}

	// Fallback: try full input as raw model id
	if inferredProvider {
		lower := strings.ToLower(cliModel)
		for _, m := range availableModels {
			if strings.EqualFold(m.ID, lower) || strings.EqualFold(string(m.Provider)+"/"+m.ID, lower) {
				return ResolveCliModelResult{Model: m}
			}
		}
		fallback := ParseModelPattern(cliModel, availableModels, &falseVal)
		if fallback.Model != nil {
			return ResolveCliModelResult{
				Model: fallback.Model, ThinkingLevel: fallback.ThinkingLevel, Warning: fallback.Warning,
			}
		}
	}
	if provider != "" {
		fallbackModel := buildFallbackModel(provider, pattern, availableModels)
		if fallbackModel != nil {
			var w string
			if result.Warning != nil {
				w = fmt.Sprintf("%s Model %q not found for provider %q. Using custom model id.", *result.Warning, pattern, provider)
			} else {
				w = fmt.Sprintf("Model %q not found for provider %q. Using custom model id.", pattern, provider)
			}
			return ResolveCliModelResult{Model: fallbackModel, Warning: &w}
		}
	}

	display := cliModel
	if provider != "" {
		display = provider + "/" + pattern
	}
	e := fmt.Sprintf("Model %q not found. Use --list-models to see available models.", display)
	return ResolveCliModelResult{Error: &e, Warning: result.Warning}
}

func FindInitialModel(options struct{
	CliProvider 	string
	CliModel 		string
	ScopedModels 	[]ScopedModel
	IsContinuing 	bool
	DefaultProvider string
	DefaultModelId 	string
	DefaultThinkingLevel *llm.ThinkingLevel
	Registry 		ModelRegistryResolver
}) (*InitialModelResult, error) {
	// 1. CLI Arguments take priority
	if options.CliProvider != "" && options.CliModel != "" {
		resolved := ResolveCliModel(options.CliProvider, options.CliModel, options.Registry)
		if resolved.Error != nil {
			return nil, fmt.Errorf("%s", *resolved.Error)
		}
		if resolved.Model != nil {
			return &InitialModelResult{Model: resolved.Model, ThinkingLevel: DefaultThinkingLevel}, nil
		}
	}

	// 2. Use first model from scoped models
	if len(options.ScopedModels) > 0  && !options.IsContinuing {
		tl := DefaultThinkingLevel
		if options.ScopedModels[0].ThinkingLevel != nil {
			tl = *options.ScopedModels[0].ThinkingLevel
		} else if options.DefaultThinkingLevel != nil {
			tl = *options.DefaultThinkingLevel
		}
		return &InitialModelResult{Model:options.ScopedModels[0].Model, ThinkingLevel: tl}, nil
	}

	// 3. Try saved default
	if options.DefaultProvider != "" && options.DefaultModelId != "" {
		found := options.Registry.Find(options.DefaultProvider, options.DefaultModelId)
		if found != nil {
			tl := DefaultThinkingLevel
			if options.DefaultThinkingLevel != nil {
				tl = *options.DefaultThinkingLevel
			}
			return &InitialModelResult{Model: found, ThinkingLevel: tl}, nil
		}
	}

	// 4. Try first available model
	availableModels, err := options.Registry.GetAvailable()
	if err != nil {
		return nil, err
	}
	
	if len(availableModels) > 0 {
		for prov, defaultId := range DefaultModelPerProvider {
			for _, m := range availableModels {
				if string(m.Provider) == string(prov) && m.ID == defaultId {
					return &InitialModelResult{Model: m, ThinkingLevel: DefaultThinkingLevel}, nil
				}
			}
		}
		return &InitialModelResult{Model: availableModels[0], ThinkingLevel: DefaultThinkingLevel}, nil
	}

	// 5. No Model Found
	return &InitialModelResult{ThinkingLevel: DefaultThinkingLevel}, nil
}

func RestoreModelFromSession(
	savedProvider, savedModelId string,
	currentModel *llm.Model[llm.Api],
	shouldPrintMessages bool,
	registry ModelRegistryResolver,
) (*InitialModelResult, error) {
	restored := registry.Find(savedProvider, savedModelId)
	hasAuth := false
	if restored != nil {
		hasAuth = registry.HasConfiguredAuth(restored)
	}

	if restored != nil && hasAuth {
		if shouldPrintMessages {
			fmt.Printf("Restored model: %s/%s\n", savedProvider, savedModelId)
		}
		return &InitialModelResult{Model: restored, ThinkingLevel: DefaultThinkingLevel}, nil
	}

	reason := "model no longer exists"
	if restored != nil {
		reason = "no auth configured"
	}

	if shouldPrintMessages {
		fmt.Printf("Warning: Could not restore model %s/%s (%s).\n", savedProvider, savedModelId, reason)
	}

	if currentModel != nil {
		if shouldPrintMessages {
			fmt.Printf("Falling back to: %s/%s\n", string(currentModel.Provider), currentModel.ID)
		}
		msg := fmt.Sprintf("Could not restore model %s/%s (%s). Using %s/%s.",
			savedProvider, savedModelId, reason, string(currentModel.Provider), currentModel.ID)
		return &InitialModelResult{Model: currentModel, ThinkingLevel: DefaultThinkingLevel, FallbackMessage: &msg}, nil
	}

	availableModels, err := registry.GetAvailable()
	if err != nil {
		return nil, err
	}

	if len(availableModels) > 0 {
		var fallback *llm.Model[llm.Api]
		for prov, defaultId := range DefaultModelPerProvider {
			for _, m := range availableModels {
				if string(m.Provider) == string(prov) && m.ID == defaultId {
					fallback = m
					break
				}
			}
			if fallback != nil {
				break
			}
		}
		if fallback == nil {
			fallback = availableModels[0]
		}

		if shouldPrintMessages {
			fmt.Printf("Falling back to: %s/%s\n", string(fallback.Provider), fallback.ID)
		}
		msg := fmt.Sprintf("Could not restore model %s/%s (%s). Using %s/%s.",
			savedProvider, savedModelId, reason, string(fallback.Provider), fallback.ID)
		return &InitialModelResult{Model: fallback, ThinkingLevel: DefaultThinkingLevel, FallbackMessage: &msg}, nil
	}

	return &InitialModelResult{ThinkingLevel: DefaultThinkingLevel}, nil
}