package llm

import (
	"strings"
	"sync"
)

var (
	modelRegistry = make(map[string]map[string]*Model[Api])
	registryMutex sync.RWMutex
)

// Initialize the model registry from Models
func init() {
	for provider, models := range Models {
		modelRegistry[provider] = models
	}
}

// GetModel: Retrieves a model by provider and model ID
func GetModel(provider, modelID string) (*Model[Api], bool) {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	providerModels, ok := modelRegistry[provider]
	if !ok {
		return nil, false
	}
	model, ok := providerModels[modelID]
	return model, ok
}

// GetProviders: Returns all registered provider IDs
func GetProviders() []string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	providers := make([]string, 0, len(modelRegistry))
	for provider := range modelRegistry {
		providers = append(providers, provider)
	}
	return providers
}

// GetModels: Returns all models for a given provider
func GetModels(provider string) []*Model[Api]{
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	providerModels, ok := modelRegistry[provider]
	if !ok {
		return nil
	}
	models := make([]*Model[Api], 0, len(providerModels))
	for _, model := range providerModels {
		models = append(models, model)
	}
	return models
}

// CalculateCost computes the cost for a given usage 
func CalculateCost(model *Model[Api], usage *Usage) ModelCost {
	cost := ModelCost{
		Input: 		(model.Cost.Input/1000000) * float64(usage.Input),
		Output: 	(model.Cost.Output/1000000) * float64(usage.Output),
		CacheRead: 	(model.Cost.CacheRead/1000000) * float64(usage.CacheRead),
		CacheWrite: (model.Cost.CacheWrite/1000000) * float64(usage.CacheWrite),
	}
	cost.Total = cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite
	return cost
}

// SupportsXhigh: Checks if a model supports xhigh thinking level
func SupportsXhigh(model *Model[Api]) bool {
	id := model.ID
	return strings.Contains(id, "gpt-5.2") ||
			strings.Contains(id, "gpt-5.3") ||
			strings.Contains(id, "gpt-5.4") ||
			strings.Contains(id, "gpt-5.5") ||
			strings.Contains(id, "deepseek-v4-pro") ||
			strings.Contains(id, "opus-4-6") ||
			strings.Contains(id, "opus-4.6") ||
			strings.Contains(id, "opus-4.7") ||
			strings.Contains(id, "opus-4-7")
}

// ModelsAreEqual: Compares two models by ID and provider
func ModelsAreEqual(a,b *Model[Api]) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Provider == b.Provider
}

// Off is the "no thinking" level (ModelThinkingLevel "off" in types.ts).
const Off ThinkingLevel = "off"

// extendedThinkingLevels mirrors EXTENDED_THINKING_LEVELS in models.ts (the
// canonical level ordering used by the clamp search).
var extendedThinkingLevels = []ThinkingLevel{"off", "minimal", "low", "medium", "high", "xhigh"}

// ThinkingLevelPtr takes the address of a level literal for Model.ThinkingLevelMap
func ThinkingLevelPtr(level ThinkingLevel) *ThinkingLevel {
	return &level
}

// GetSupportedThinkingLevels ports getSupportedThinkingLevels: non-reasoning
// models only support "off"; reasoning models support every level except
// those mapped to null (unavailable) in thinkingLevelMap, and "xhigh" must be
// mapped explicitly.
func GetSupportedThinkingLevels(model *Model[Api]) []ThinkingLevel {
	if !model.Reasoning {
		return []ThinkingLevel{Off}
	}
	levels := []ThinkingLevel{}
	for _, level := range extendedThinkingLevels {
		mapped, has := model.ThinkingLevelMap[string(level)]
		if has && mapped == nil {
			// thinkingLevelMap[level] === null
			continue
		}
		if level == XHigh && (!has || mapped == nil) {
			// xhigh requires thinkingLevelMap[level] !== undefined
			continue
		}
		levels = append(levels, level)
	}
	return levels
}

// ClampThinkingLevel ports clampThinkingLevel: keep the requested level when
// supported, otherwise walk outward (up first, then down) to the nearest
// supported level.
func ClampThinkingLevel(model *Model[Api], level ThinkingLevel) ThinkingLevel {
	available := GetSupportedThinkingLevels(model)
	for _, l := range available {
		if l == level {
			return level
		}
	}

	requestedIndex := -1
	for i, l := range extendedThinkingLevels {
		if l == level {
			requestedIndex = i
			break
		}
	}
	if requestedIndex == -1 {
		if len(available) > 0 {
			return available[0]
		}
		return Off
	}

	for i := requestedIndex; i < len(extendedThinkingLevels); i++ {
		candidate := extendedThinkingLevels[i]
		for _, l := range available {
			if l == candidate {
				return candidate
			}
		}
	}
	for i := requestedIndex - 1; i >= 0; i-- {
		candidate := extendedThinkingLevels[i]
		for _, l := range available {
			if l == candidate {
				return candidate
			}
		}
	}
	if len(available) > 0 {
		return available[0]
	}
	return Off
}