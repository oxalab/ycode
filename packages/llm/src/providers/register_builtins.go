package providers

import (
	"context"

	src "ycode/packages/llm/src"
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

	// Azure OpenAI Responses API
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("azure-openai-responses"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToAzureOpenAIResponses(options)
				sourceCh := StreamAzureOpenAIResponses(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleAzureOpenAIResponses(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// OpenAI Codex Responses API (ChatGPT backend)
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("openai-codex-responses"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToOpenAICodexResponses(options)
				sourceCh := StreamOpenAICodexResponses(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleOpenAICodexResponses(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// Google Generative AI (Gemini API)
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("google-generative-ai"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToGoogle(options)
				sourceCh := StreamGoogle(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleGoogle(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// Google Vertex AI
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("google-vertex"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToGoogleVertex(options)
				sourceCh := StreamGoogleVertex(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleGoogleVertex(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// Note: Additional providers (mistral, bedrock, gemini-cli)
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
		SessionID:         options.SessionID,
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

// convertStreamOptionsToOpenAICodexResponses converts StreamOptions to
// OpenAICodexResponsesOptions
func convertStreamOptionsToOpenAICodexResponses(options *src.StreamOptions) *OpenAICodexResponsesOptions {
	if options == nil {
		return nil
	}
	cacheRetention := options.CacheRetention
	return &OpenAICodexResponsesOptions{
		APIKey:         options.APIKey,
		MaxTokens:      options.MaxTokens,
		Temperature:    options.Temperature,
		Headers:        options.Headers,
		SessionID:      options.SessionID,
		CacheRetention: &cacheRetention,
		Context:        options.Context,
		Transport:      options.Transport,
	}
}

// convertStreamOptionsToAzureOpenAIResponses converts StreamOptions to
// AzureOpenAIResponsesOptions
func convertStreamOptionsToAzureOpenAIResponses(options *src.StreamOptions) *AzureOpenAIResponsesOptions {
	if options == nil {
		return nil
	}
	cacheRetention := options.CacheRetention
	return &AzureOpenAIResponsesOptions{
		APIKey:         options.APIKey,
		MaxTokens:      options.MaxTokens,
		Temperature:    options.Temperature,
		Headers:        options.Headers,
		SessionID:      options.SessionID,
		CacheRetention: &cacheRetention,
		Context:        options.Context,
	}
}

// convertStreamOptionsToGoogle converts StreamOptions to GoogleOptions
func convertStreamOptionsToGoogle(options *src.StreamOptions) *GoogleOptions {
	if options == nil {
		return nil
	}
	return &GoogleOptions{
		APIKey:      options.APIKey,
		MaxTokens:   options.MaxTokens,
		Temperature: options.Temperature,
		Headers:     options.Headers,
		Context:     options.Context,
	}
}

// convertStreamOptionsToGoogleVertex converts StreamOptions to
// GoogleVertexOptions
func convertStreamOptionsToGoogleVertex(options *src.StreamOptions) *GoogleVertexOptions {
	if options == nil {
		return nil
	}
	return &GoogleVertexOptions{
		APIKey:      options.APIKey,
		MaxTokens:   options.MaxTokens,
		Temperature: options.Temperature,
		Headers:     options.Headers,
		Context:     options.Context,
	}
}
