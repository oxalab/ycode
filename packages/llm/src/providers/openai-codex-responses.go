package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"ycode/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

const (
	openAICodexBaseURL = "https://api.openai.com/v1/responses"
)

// =============================================================================
// Types
// =============================================================================

type OpenAICodexResponsesOptions struct {
	APIKey             string
	MaxTokens          *int
	MaxOutputTokens    *int
	Temperature        *float64
	ServiceTier        *string
	ReasoningEffort    *string
	ReasoningSummary   *string
	Headers            map[string]string
	SessionID          string
	CacheRetention     *llm.CacheRetention
	// Callbacks
	OnPayload  func(params interface{}, model llm.Model[llm.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model llm.Model[llm.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamOpenAICodexResponses creates an async streaming function for OpenAI Codex Responses API
func StreamOpenAICodexResponses(model llm.Model[llm.Api], context llm.ChatContext, options *OpenAICodexResponsesOptions) func() <-chan llm.AssistantMessageEvent {
	return func() <-chan llm.AssistantMessageEvent {
		ch := make(chan llm.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			output := &llm.AssistantMessage{
				Role:       llm.RoleAssistant,
				Content:    []llm.ContentBlock{},
				Api:        model.Api,
				Provider:   model.Provider,
				Model:      model.ID,
				Usage:      llm.Usage{Cost: llm.UsageCost{}},
				StopReason: llm.StopReasonStop,
			}

			// Build and send request
			client := &http.Client{}
			params := buildOpenAICodexResponsesParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doOpenAICodexResponsesRequest(client, model, context, params, options)
			if err != nil {
				handleOpenAICodexResponsesError(ch, output, err, false)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status": resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			ch <- llm.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response - reuse OpenAI Responses stream processor
			if err := processOpenAIResponsesStream(resp.Body, ch, output, model, &OpenAIResponsesOptions{
				APIKey:             options.APIKey,
				MaxTokens:          options.MaxTokens,
				MaxOutputTokens:    options.MaxOutputTokens,
				Temperature:        options.Temperature,
				ServiceTier:        options.ServiceTier,
				ReasoningEffort:    options.ReasoningEffort,
				ReasoningSummary:   options.ReasoningSummary,
				Headers:            options.Headers,
				SessionID:          options.SessionID,
				CacheRetention:     options.CacheRetention,
				OnPayload:          options.OnPayload,
				OnResponse:         options.OnResponse,
			}); err != nil {
				handleOpenAICodexResponsesError(ch, output, err, true)
				return
			}

			if output.StopReason == llm.StopReasonError || output.StopReason == llm.StopReasonAborted {
				handleOpenAICodexResponsesError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
				return
			}

			ch <- llm.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// =============================================================================
// Request handling
// =============================================================================

func doOpenAICodexResponsesRequest(client *http.Client, model llm.Model[llm.Api], context llm.ChatContext, params interface{}, options *OpenAICodexResponsesOptions) (*http.Response, error) {
	baseURL := openAICodexBaseURL
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	apiKey := ""
	if options != nil && options.APIKey != "" {
		apiKey = options.APIKey
	}
	if apiKey == "" {
		apiKey = getEnvApiKey(string(model.Provider))
	}
	if apiKey == "" {
		apiKey = getEnvApiKey("openai")
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Add model headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	// Add Copilot dynamic headers if applicable
	if model.Provider == llm.GithubCopilot {
		hasImages := HasCopilotVisionInput(contextMessagesParam(context))
		copilotHeaders := BuildCopilotDynamicHeaders(contextMessagesParam(context), hasImages)
		for k, v := range copilotHeaders {
			req.Header.Set(k, v)
		}
	}

	// Add session ID headers
	if options != nil && options.SessionID != "" {
		retention := llm.CacheRetentionShort
		if options.CacheRetention != nil {
			retention = *options.CacheRetention
		}
		if retention != llm.CacheRetentionNone {
			req.Header.Set("session_id", options.SessionID)
			req.Header.Set("x-client-request-id", options.SessionID)
		}
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	return client.Do(req)
}

func handleOpenAICodexResponsesError(ch chan<- llm.AssistantMessageEvent, output *llm.AssistantMessage, err error, isAborted bool) {
	output.StopReason = llm.StopReasonError
	if isAborted {
		output.StopReason = llm.StopReasonAborted
	}
	output.ErrorMessage = err.Error()
	ch <- llm.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
}

// =============================================================================
// Parameter building
// =============================================================================

func buildOpenAICodexResponsesParams(model llm.Model[llm.Api], context llm.ChatContext, options *OpenAICodexResponsesOptions) interface{} {
	allowedToolCallProviders := map[string]bool{
		"openai":         true,
		"openai-codex":   true,
	}

	convertOptions := &ConvertResponsesMessagesOptions{
		IncludeSystemPrompt: true,
	}

	messages := convertResponsesMessages(model, context, allowedToolCallProviders, convertOptions)

	params := map[string]interface{}{
		"model":  model.ID,
		"input":  messages,
		"stream": true,
		"store":  false,
	}

	// Max tokens
	maxTokens := 0
	if options != nil && options.MaxOutputTokens != nil {
		maxTokens = *options.MaxOutputTokens
	} else if options != nil && options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	if maxTokens > 0 {
		params["max_output_tokens"] = maxTokens
	}

	// Temperature
	if options != nil && options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	// Service tier
	if options != nil && options.ServiceTier != nil {
		params["service_tier"] = *options.ServiceTier
	}

	// Tools
	if len(context.Tools) > 0 {
		convertToolsOptions := &ConvertResponsesToolsOptions{}
		params["tools"] = convertResponsesTools(context.Tools, convertToolsOptions)
	}

	// Reasoning
	if model.Reasoning {
		effort := "medium"
		summary := "auto"

		if options != nil && options.ReasoningEffort != nil {
			effort = *options.ReasoningEffort
		}
		if options != nil && options.ReasoningSummary != nil {
			summary = *options.ReasoningSummary
		}

		params["reasoning"] = map[string]interface{}{
			"effort":  effort,
			"summary": summary,
		}
	}

	// Prompt cache
	cacheRetention := llm.CacheRetentionShort
	if options != nil && options.CacheRetention != nil {
		cacheRetention = *options.CacheRetention
	}
	if cacheRetention != llm.CacheRetentionNone {
		if options != nil && options.SessionID != "" {
			params["prompt_cache_key"] = options.SessionID
		}
		if cacheRetention == llm.CacheRetentionLong {
			params["prompt_cache_retention"] = "24h"
		}
	}

	return params
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleOpenAICodexResponses provides a simplified streaming interface for OpenAI Codex Responses
func StreamSimpleOpenAICodexResponses(model llm.Model[llm.Api], context llm.ChatContext, options *llm.SimpleStreamOptions) func() <-chan llm.AssistantMessageEvent {
	opts := buildOpenAICodexResponsesSimpleOptions(options)
	return StreamOpenAICodexResponses(model, context, opts)
}

func buildOpenAICodexResponsesSimpleOptions(options *llm.SimpleStreamOptions) *OpenAICodexResponsesOptions {
	if options == nil {
		return nil
	}

	reasoningEffort := ""
	if options.Reasoning != "" {
		reasoningEffort = string(options.Reasoning)
	}

	return &OpenAICodexResponsesOptions{
		APIKey:          options.APIKey,
		MaxTokens:       options.MaxTokens,
		Temperature:     options.Temperature,
		ReasoningEffort: &reasoningEffort,
		Headers:         options.Headers,
	}
}
