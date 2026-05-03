package providers

import (
	"context"

	src "pigo.dev/packages/llm/src"
)

// =============================================================================
// Built-in provider registration
// =============================================================================

func init() {
	RegisterBuiltInApiProviders()
}

// RegisterBuiltInApiProviders registers all built-in LLM API providers
func RegisterBuiltInApiProviders() {
	// Anthropic Messages API
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("anthropic-messages"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToAnthropic(options)
				sourceCh := StreamAnthropic(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleAnthropic(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// OpenAI Completions API
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("openai-completions"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToOpenAICompletions(options)
				sourceCh := StreamOpenAICompletions(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleOpenAICompletions(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// OpenAI Responses API
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("openai-responses"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToOpenAIResponses(options)
				sourceCh := StreamOpenAIResponses(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleOpenAIResponses(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// Note: Additional providers (mistral, google, vertex, bedrock, azure, codex, gemini-cli)
	// would be registered here following the same pattern once their implementations are complete.
}

// ResetApiProviders clears all providers and re-registers built-in providers
func ResetApiProviders() {
	src.ClearApiProviders()
	RegisterBuiltInApiProviders()
}

// =============================================================================
// Helper functions
// =============================================================================

// forwardEvents forwards events from source channel to destination channel
// with context cancellation support
func forwardEvents(ctx context.Context, dest chan<- src.AssistantMessageEvent, source <-chan src.AssistantMessageEvent) {
	for {
		select {
		case <-ctx.Done():
			// Context cancelled, stop forwarding
			return
		case event, ok := <-source:
			if !ok {
				// Source channel closed
				return
			}
			select {
			case <-ctx.Done():
				return
			case dest <- event:
			}
		}
	}
}

// convertStreamOptionsToAnthropic converts StreamOptions to AnthropicOptions
func convertStreamOptionsToAnthropic(options *src.StreamOptions) *AnthropicOptions {
	if options == nil {
		return nil
	}
	cacheRetention := options.CacheRetention
	return &AnthropicOptions{
		APIKey:            options.APIKey,
		MaxTokens:         options.MaxTokens,
		Temperature:       options.Temperature,
		Headers:           options.Headers,
		CacheRetention:    &cacheRetention,
	}
}

// convertStreamOptionsToOpenAICompletions converts StreamOptions to OpenAICompletionsOptions
func convertStreamOptionsToOpenAICompletions(options *src.StreamOptions) *OpenAICompletionsOptions {
	if options == nil {
		return nil
	}
	cacheRetention := options.CacheRetention
	return &OpenAICompletionsOptions{
		APIKey:         options.APIKey,
		MaxTokens:      options.MaxTokens,
		Temperature:    options.Temperature,
		Headers:        options.Headers,
		CacheRetention: &cacheRetention,
	}
}

// convertStreamOptionsToOpenAIResponses converts StreamOptions to OpenAIResponsesOptions
func convertStreamOptionsToOpenAIResponses(options *src.StreamOptions) *OpenAIResponsesOptions {
	if options == nil {
		return nil
	}
	cacheRetention := options.CacheRetention
	return &OpenAIResponsesOptions{
		APIKey:         options.APIKey,
		MaxTokens:      options.MaxTokens,
		Temperature:    options.Temperature,
		Headers:        options.Headers,
		CacheRetention: &cacheRetention,
	}
}
