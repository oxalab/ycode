package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	src "ycode/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

const (
	azureOpenAIBaseURL = "https://%s.openai.azure.com/openai/deployments/%s"
)

// =============================================================================
// Types
// =============================================================================

type AzureOpenAIResponsesOptions struct {
	APIKey          string
	Endpoint        string
	DeploymentName  string
	APIVersion      string
	MaxTokens       *int
	MaxOutputTokens *int
	Temperature     *float64
	Headers         map[string]string
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamAzureOpenAIResponses creates an async streaming function for Azure OpenAI Responses API
func StreamAzureOpenAIResponses(model src.Model[src.Api], context src.ChatContext, options *AzureOpenAIResponsesOptions) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			output := &src.AssistantMessage{
				Role:       src.RoleAssistant,
				Content:    []src.ContentBlock{},
				Api:        model.Api,
				Provider:   model.Provider,
				Model:      model.ID,
				Usage:      src.Usage{Cost: src.UsageCost{}},
				StopReason: src.StopReasonStop,
			}

			// Build and send request
			client := &http.Client{}
			params := buildAzureOpenAIResponsesParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doAzureOpenAIResponsesRequest(client, model, params, options)
			if err != nil {
				handleAzureOpenAIResponsesError(ch, output, err, false)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status": resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response - Azure uses OpenAI-compatible format
			compat := &OpenAICompletionsCompat{
				SupportsStore:                    false,
				SupportsDeveloperRole:             true,
				SupportsReasoningEffort:           true,
				SupportsUsageInStreaming:          true,
				MaxTokensField:                    "max_tokens",
				RequiresToolResultName:            false,
				RequiresAssistantAfterToolResult:  false,
				ThinkingFormat:                   "openai",
				CacheControlFormat:                "",
				SendSessionAffinityHeaders:        false,
			}
			if err := processOpenAICompletionsStream(resp.Body, ch, output, model, compat); err != nil {
				handleAzureOpenAIResponsesError(ch, output, err, true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleAzureOpenAIResponsesError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// =============================================================================
// Request handling
// =============================================================================

func doAzureOpenAIResponsesRequest(client *http.Client, model src.Model[src.Api], params interface{}, options *AzureOpenAIResponsesOptions) (*http.Response, error) {
	endpoint := ""
	deployment := model.ID

	if options != nil && options.Endpoint != "" {
		endpoint = options.Endpoint
	}
	if options != nil && options.DeploymentName != "" {
		deployment = options.DeploymentName
	}

	// Extract resource name from endpoint
	resourceName := "myresource"
	if parts := strings.Split(endpoint, "."); len(parts) > 0 {
		urlParts := strings.Split(parts[0], "://")
		if len(urlParts) > 1 {
			resourceName = urlParts[1]
		}
	}

	baseURL := fmt.Sprintf(azureOpenAIBaseURL, resourceName, deployment)
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	// Add API version
	url := baseURL + "?api-version=" + getAzureAPIVersion(options)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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

	req.Header.Set("api-key", apiKey)

	// Add model headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	return client.Do(req)
}

func handleAzureOpenAIResponsesError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
	output.StopReason = src.StopReasonError
	if isAborted {
		output.StopReason = src.StopReasonAborted
	}
	output.ErrorMessage = err.Error()
	ch <- src.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
}

// =============================================================================
// Parameter building
// =============================================================================

func buildAzureOpenAIResponsesParams(model src.Model[src.Api], context src.ChatContext, options *AzureOpenAIResponsesOptions) interface{} {
	allowedToolCallProviders := map[string]bool{
		"openai":         true,
		"openai-codex":   true,
		"azure-openai":   true,
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

	// Tools
	if len(context.Tools) > 0 {
		convertToolsOptions := &ConvertResponsesToolsOptions{}
		params["tools"] = convertResponsesTools(context.Tools, convertToolsOptions)
	}

	return params
}

func getAzureAPIVersion(options *AzureOpenAIResponsesOptions) string {
	if options != nil && options.APIVersion != "" {
		return options.APIVersion
	}
	return "2024-02-15-preview"
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleAzureOpenAIResponses provides a simplified streaming interface for Azure OpenAI Responses
func StreamSimpleAzureOpenAIResponses(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	opts := buildAzureOpenAIResponsesSimpleOptions(options)
	return StreamAzureOpenAIResponses(model, context, opts)
}

func buildAzureOpenAIResponsesSimpleOptions(options *src.SimpleStreamOptions) *AzureOpenAIResponsesOptions {
	if options == nil {
		return nil
	}

	return &AzureOpenAIResponsesOptions{
		APIKey:      options.APIKey,
		MaxTokens:   options.MaxTokens,
		Temperature: options.Temperature,
		Headers:     options.Headers,
	}
}
