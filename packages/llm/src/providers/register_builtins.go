package providers

import (
	"context"
	"net/http"

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

	// Mistral (chat completions)
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("mistral-conversations"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToMistral(options)
				sourceCh := StreamMistral(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleMistral(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// Amazon Bedrock (converse stream)
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("bedrock-converse-stream"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				opts := convertStreamOptionsToBedrock(options)
				sourceCh := StreamAmazonBedrock(model, chatCtx, opts)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 256)
			go func() {
				defer close(ch)
				sourceCh := StreamSimpleAmazonBedrock(model, chatCtx, options)()
				forwardEvents(ctx, ch, sourceCh)
			}()
			return ch
		},
	}, "builtin")

	// Note: all nine built-in APIs are registered — the set matches the TS
	// reference (the gemini-cli family was removed upstream in 0.71.0).
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
	converted := &AnthropicOptions{
		APIKey:         options.APIKey,
		MaxTokens:      options.MaxTokens,
		Temperature:    options.Temperature,
		Headers:        options.Headers,
		SessionID:      options.SessionID,
		CacheRetention: &cacheRetention,
		Context:        options.Context,
		TimeoutMs:      options.TimeoutMs,
		MaxRetries:     options.MaxRetries,
		OnPayload:      adaptOnPayload(options.OnPayload),
		OnResponse:     adaptOnResponse(options.OnResponse),
	}
	// The reference extracts only metadata.user_id (anthropic.ts) — other
	// metadata keys are ignored.
	if userID, ok := options.Metadata["user_id"].(string); ok {
		converted.Metadata = map[string]string{"user_id": userID}
	}
	return converted
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
		Context:        options.Context,
		TimeoutMs:      options.TimeoutMs,
		MaxRetries:     options.MaxRetries,
		OnPayload:      adaptOnPayload(options.OnPayload),
		OnResponse:     adaptOnResponse(options.OnResponse),
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
		Context:        options.Context,
		TimeoutMs:      options.TimeoutMs,
		MaxRetries:     options.MaxRetries,
		OnPayload:      adaptOnPayload(options.OnPayload),
		OnResponse:     adaptOnResponse(options.OnResponse),
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
		OnPayload:      adaptOnPayload(options.OnPayload),
		OnResponse:     adaptOnResponse(options.OnResponse),
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
		TimeoutMs:      options.TimeoutMs,
		MaxRetries:     options.MaxRetries,
		OnPayload:      adaptOnPayload(options.OnPayload),
		OnResponse:     adaptOnResponse(options.OnResponse),
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
		OnPayload:   adaptOnPayload(options.OnPayload),
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
		OnPayload:   adaptOnPayload(options.OnPayload),
	}
}

// convertStreamOptionsToMistral converts StreamOptions to MistralOptions
func convertStreamOptionsToMistral(options *src.StreamOptions) *MistralOptions {
	if options == nil {
		return nil
	}
	return &MistralOptions{
		APIKey:      options.APIKey,
		MaxTokens:   options.MaxTokens,
		Temperature: options.Temperature,
		Headers:     options.Headers,
		SessionID:   options.SessionID,
		Context:     options.Context,
		OnPayload:   adaptOnPayload(options.OnPayload),
	}
}

// convertStreamOptionsToBedrock converts StreamOptions to AmazonBedrockOptions
func convertStreamOptionsToBedrock(options *src.StreamOptions) *AmazonBedrockOptions {
	if options == nil {
		return nil
	}
	return &AmazonBedrockOptions{
		MaxTokens:      options.MaxTokens,
		Temperature:    options.Temperature,
		Headers:        options.Headers,
		CacheRetention: options.CacheRetention,
		Context:        options.Context,
		OnPayload:      adaptOnPayload(options.OnPayload),
		OnResponse:     adaptOnResponse(options.OnResponse),
	}
}

// adaptOnPayload adapts the generic StreamOptions.OnPayload callback to the
// per-provider signature. A nil return keeps the original payload (TS:
// undefined = keep).
func adaptOnPayload(fn func(payload any) any) func(params interface{}, model src.Model[src.Api]) (interface{}, error) {
	if fn == nil {
		return nil
	}
	return func(params interface{}, model src.Model[src.Api]) (interface{}, error) {
		out := fn(params)
		if out == nil {
			return nil, nil
		}
		return out, nil
	}
}

// adaptOnResponse adapts the generic StreamOptions.OnResponse callback to the
// per-provider signature. Providers hand over a map with "status" and
// "headers" (or an *http.Response); the generic callback wants *http.Response,
// so rebuild one carrying the same status and headers.
func adaptOnResponse(fn func(resp *http.Response)) func(resp interface{}, model src.Model[src.Api]) {
	if fn == nil {
		return nil
	}
	return func(resp interface{}, model src.Model[src.Api]) {
		out := &http.Response{Header: http.Header{}}
		switch v := resp.(type) {
		case *http.Response:
			out.StatusCode = v.StatusCode
			out.Header = v.Header
		case map[string]interface{}:
			if s, ok := v["status"].(int); ok {
				out.StatusCode = s
			}
			if h, ok := v["headers"].(http.Header); ok {
				out.Header = h
			}
		}
		fn(out)
	}
}
