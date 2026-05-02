package llm

import (
	"context"
	"fmt"
	"sync"
)

// ApiStreamFunction is a non-generic stream function type for the registry.
type ApiStreamFunction func(
	ctx context.Context,
	model Model[Api],
	chatCtx ChatContext,
	options *StreamOptions,
) <-chan AssistantMessageEvent

// ApiStreamSimpleFunction is a non-generic simple stream function type for the registry.
type ApiStreamSimpleFunction func(
	ctx context.Context,
	model Model[Api],
	chatCtx ChatContext,
	options *SimpleStreamOptions,
) <-chan AssistantMessageEvent

// ApiProvider holds an API's stream functions for registration.
type ApiProvider struct {
	Api          Api
	Stream       ApiStreamFunction
	StreamSimple ApiStreamSimpleFunction
}

type registeredApiProvider struct {
	Provider ApiProvider
	SourceID string
}

var (
	apiProviderRegistry = make(map[string]registeredApiProvider)
	registryMu         sync.RWMutex
)

func wrapStream(api Api, stream ApiStreamFunction) ApiStreamFunction {
	return func(ctx context.Context, model Model[Api], chatCtx ChatContext, options *StreamOptions) <-chan AssistantMessageEvent {
		if model.Api != api {
			panic(fmt.Sprintf("mismatched api: %s expected %s", model.Api, api))
		}
		return stream(ctx, model, chatCtx, options)
	}
}

func wrapStreamSimple(api Api, streamSimple ApiStreamSimpleFunction) ApiStreamSimpleFunction {
	return func(ctx context.Context, model Model[Api], chatCtx ChatContext, options *SimpleStreamOptions) <-chan AssistantMessageEvent {
		if model.Api != api {
			panic(fmt.Sprintf("mismatched api: %s expected %s", model.Api, api))
		}
		return streamSimple(ctx, model, chatCtx, options)
	}
}

func RegisterApiProvider(provider ApiProvider, sourceID string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	apiProviderRegistry[string(provider.Api)] = registeredApiProvider{
		Provider: ApiProvider{
			Api:          provider.Api,
			Stream:       wrapStream(provider.Api, provider.Stream),
			StreamSimple: wrapStreamSimple(provider.Api, provider.StreamSimple),
		},
		SourceID: sourceID,
	}
}

func GetApiProvider(api Api) *ApiProvider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	if entry, ok := apiProviderRegistry[string(api)]; ok {
		return &entry.Provider
	}
	return nil
}

func GetApiProviders() []ApiProvider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	providers := make([]ApiProvider, 0, len(apiProviderRegistry))
	for _, entry := range apiProviderRegistry {
		providers = append(providers, entry.Provider)
	}
	return providers
}

func UnregisterApiProviders(sourceID string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for key, entry := range apiProviderRegistry {
		if entry.SourceID == sourceID {
			delete(apiProviderRegistry, key)
		}
	}
}

func ClearApiProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()
	clear(apiProviderRegistry)
}
