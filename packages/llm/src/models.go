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