package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	src "ycode/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

// Port of azure-openai-responses.ts: Azure OpenAI's v1 surface posts to
// {base}/responses?api-version=v1 (the legacy /openai/deployments path with
// api-version=2024-02-15-preview speaks chat-completions, not Responses).
const (
	defaultAzureAPIVersion = "v1"
)

var azureToolCallProviders = map[string]bool{
	"openai":                 true,
	"openai-codex":           true,
	"opencode":               true,
	"azure-openai-responses": true,
}

// =============================================================================
// Types
// =============================================================================

// AzureOpenAIResponsesOptions mirrors AzureOpenAIResponsesOptions in
// azure-openai-responses.ts.
type AzureOpenAIResponsesOptions struct {
	APIKey              string
	MaxTokens           *int
	Temperature         *float64
	ReasoningEffort     *string
	ReasoningSummary    *string
	AzureAPIVersion     string
	AzureResourceName   string
	AzureBaseURL        string
	AzureDeploymentName string
	Headers             map[string]string
	SessionID           string
	CacheRetention      *src.CacheRetention
	Context             context.Context
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamAzureOpenAIResponses creates an async streaming function for the
// Azure OpenAI Responses API.
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

			handleError := func(err error) {
				// TS catch block: drop streaming scratch fields, then
				// surface the error (aborted only on real aborts).
				for _, block := range output.Content {
					if tc, ok := block.(*src.ToolCall); ok {
						tc.PartialArgs = ""
					}
				}
				if ctx := azureContext(options); ctx != nil && ctx.Err() != nil {
					output.StopReason = src.StopReasonAborted
				} else {
					output.StopReason = src.StopReasonError
				}
				output.ErrorMessage = err.Error()
				ch <- src.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
			}

			apiKey := ""
			if options != nil {
				apiKey = options.APIKey
			}
			if apiKey == "" {
				apiKey = getEnvApiKey(string(model.Provider))
			}
			if apiKey == "" {
				handleError(fmt.Errorf("Azure OpenAI API key is required. Set AZURE_OPENAI_API_KEY environment variable or pass it as an argument."))
				return
			}

			params := buildAzureOpenAIResponsesParams(model, context, options)
			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doAzureOpenAIResponsesRequest(model, params, options, apiKey)
			if err != nil {
				handleError(err)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status":  resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				resp.Body.Close()
				handleError(fmt.Errorf("Azure OpenAI API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(excerpt))))
				return
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// Azure speaks the same Responses event stream as api.openai.com.
			processorOptions := &OpenAIResponsesOptions{
				APIKey:    apiKey,
				SessionID: optionsSessionIDAzure(options),
			}
			if err := processOpenAIResponsesStream(resp.Body, ch, output, model, processorOptions); err != nil {
				handleError(err)
				return
			}
			resp.Body.Close()

			if ctx := azureContext(options); ctx != nil && ctx.Err() != nil {
				handleError(fmt.Errorf("Request was aborted"))
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleError(fmt.Errorf("An unknown error occurred"))
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

func doAzureOpenAIResponsesRequest(model src.Model[src.Api], params interface{}, options *AzureOpenAIResponsesOptions, apiKey string) (*http.Response, error) {
	baseURL, _, err := resolveAzureConfig(model, options)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	endpoint := baseURL + "/responses?api-version=" + url.QueryEscape(azureAPIVersion(options))

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if ctx := azureContext(options); ctx != nil {
		req = req.WithContext(ctx)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{}
	return client.Do(req)
}

// normalizeAzureBaseUrl mirrors normalizeAzureBaseUrl: trim trailing slashes,
// validate the URL, and force /openai/v1 on Azure hosts so /responses can be
// appended.
func normalizeAzureBaseUrl(baseURL string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("Invalid Azure OpenAI base URL: %s", baseURL)
	}

	isAzureHost := strings.HasSuffix(parsed.Hostname(), ".openai.azure.com") ||
		strings.HasSuffix(parsed.Hostname(), ".cognitiveservices.azure.com")
	normalizedPath := strings.TrimRight(parsed.Path, "/")

	if isAzureHost && (normalizedPath == "" || normalizedPath == "/openai") {
		parsed.Path = "/openai/v1"
		parsed.RawQuery = ""
	}

	result := strings.TrimRight(parsed.String(), "/")
	return result, nil
}

func buildDefaultAzureBaseURL(resourceName string) string {
	return fmt.Sprintf("https://%s.openai.azure.com/openai/v1", resourceName)
}

// resolveAzureConfig mirrors resolveAzureConfig: options → env →
// model.BaseURL, error when nothing is set.
func resolveAzureConfig(model src.Model[src.Api], options *AzureOpenAIResponsesOptions) (baseURL string, apiVersion string, err error) {
	apiVersion = azureAPIVersion(options)

	var rawBaseURL string
	if options != nil && strings.TrimSpace(options.AzureBaseURL) != "" {
		rawBaseURL = strings.TrimSpace(options.AzureBaseURL)
	} else if env := os.Getenv("AZURE_OPENAI_BASE_URL"); strings.TrimSpace(env) != "" {
		rawBaseURL = strings.TrimSpace(env)
	}

	if rawBaseURL == "" {
		resourceName := ""
		if options != nil && options.AzureResourceName != "" {
			resourceName = options.AzureResourceName
		} else if env := os.Getenv("AZURE_OPENAI_RESOURCE_NAME"); env != "" {
			resourceName = env
		}
		if resourceName != "" {
			rawBaseURL = buildDefaultAzureBaseURL(resourceName)
		}
	}

	if rawBaseURL == "" && model.BaseURL != "" {
		rawBaseURL = model.BaseURL
	}

	if rawBaseURL == "" {
		return "", "", fmt.Errorf("Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME, or pass azureBaseUrl, azureResourceName, or model.baseUrl.")
	}

	normalized, err := normalizeAzureBaseUrl(rawBaseURL)
	if err != nil {
		return "", "", err
	}
	return normalized, apiVersion, nil
}

func azureAPIVersion(options *AzureOpenAIResponsesOptions) string {
	if options != nil && options.AzureAPIVersion != "" {
		return options.AzureAPIVersion
	}
	if env := os.Getenv("AZURE_OPENAI_API_VERSION"); env != "" {
		return env
	}
	return defaultAzureAPIVersion
}

func azureContext(options *AzureOpenAIResponsesOptions) context.Context {
	if options != nil {
		return options.Context
	}
	return nil
}

func optionsSessionIDAzure(options *AzureOpenAIResponsesOptions) string {
	if options != nil {
		return options.SessionID
	}
	return ""
}

// =============================================================================
// Parameter building
// =============================================================================

func buildAzureOpenAIResponsesParams(model src.Model[src.Api], context src.ChatContext, options *AzureOpenAIResponsesOptions) interface{} {
	deploymentName := resolveAzureDeploymentName(model, options)

	messages := convertResponsesMessages(model, context, azureToolCallProviders, &ConvertResponsesMessagesOptions{
		IncludeSystemPrompt: true,
	})

	params := map[string]interface{}{
		"model":            deploymentName,
		"input":            messages,
		"stream":           true,
		"prompt_cache_key": optionsSessionIDAzure(options),
	}

	maxTokens := 0
	if options != nil && options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	if maxTokens > 0 {
		params["max_output_tokens"] = maxTokens
	}

	if options != nil && options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	if len(context.Tools) > 0 {
		params["tools"] = convertResponsesTools(context.Tools, &ConvertResponsesToolsOptions{})
	}

	// model.reasoning branch (azure-openai-responses.ts:263-278): explicit
	// effort/summary win (effort remapped through thinkingLevelMap, null
	// passthrough); otherwise reasoning defaults to the model's off level,
	// and a thinkingLevelMap with off:null (cannot disable thinking) sends
	// no reasoning field at all.
	if model.Reasoning {
		effortSet := options != nil && options.ReasoningEffort != nil
		summarySet := options != nil && options.ReasoningSummary != nil
		if effortSet || summarySet {
			effort := "medium"
			if effortSet {
				effort = *options.ReasoningEffort
				if mapped, has := model.ThinkingLevelMap[effort]; has && mapped != nil {
					effort = string(*mapped)
				}
			}
			summary := "auto"
			if summarySet {
				summary = *options.ReasoningSummary
			}
			params["reasoning"] = map[string]interface{}{
				"effort":  effort,
				"summary": summary,
			}
			params["include"] = []string{"reasoning.encrypted_content"}
		} else if mapped, has := model.ThinkingLevelMap["off"]; !(has && mapped == nil) {
			// thinkingLevelMap?.off !== null branch: off ?? "none".
			effort := "none"
			if has && mapped != nil {
				effort = string(*mapped)
			}
			params["reasoning"] = map[string]interface{}{"effort": effort}
		}
	}

	return params
}

// parseAzureDeploymentNameMap mirrors parseDeploymentNameMap: CSV of
// modelId=deployment pairs.
func parseAzureDeploymentNameMap(value string) map[string]string {
	mapping := map[string]string{}
	if value == "" {
		return mapping
	}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		modelID := strings.TrimSpace(parts[0])
		deployment := strings.TrimSpace(parts[1])
		if modelID == "" || deployment == "" {
			continue
		}
		mapping[modelID] = deployment
	}
	return mapping
}

// resolveAzureDeploymentName mirrors resolveDeploymentName: options →
// AZURE_OPENAI_DEPLOYMENT_NAME_MAP → model id.
func resolveAzureDeploymentName(model src.Model[src.Api], options *AzureOpenAIResponsesOptions) string {
	if options != nil && options.AzureDeploymentName != "" {
		return options.AzureDeploymentName
	}
	mapping := parseAzureDeploymentNameMap(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP"))
	if mapped, ok := mapping[model.ID]; ok {
		return mapped
	}
	return model.ID
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleAzureOpenAIResponses provides a simplified streaming interface
// for the Azure OpenAI Responses API.
func StreamSimpleAzureOpenAIResponses(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	// TS streamSimple throws `No API key for provider: ${model.provider}`
	// before delegating (a different message than the full path's
	// createClient error).
	apiKey := ""
	if options != nil {
		apiKey = options.APIKey
	}
	if apiKey == "" {
		apiKey = getEnvApiKey(string(model.Provider))
	}
	if apiKey == "" {
		return noKeyErrorStream(model, fmt.Errorf("No API key for provider: %s", model.Provider))
	}
	opts := buildAzureOpenAIResponsesSimpleOptions(model, options)
	return StreamAzureOpenAIResponses(model, context, opts)
}

// noKeyErrorStream returns a stream that emits a single error event, for the
// reference's synchronous simple-wrapper throws.
func noKeyErrorStream(model src.Model[src.Api], err error) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 1)
		output := &src.AssistantMessage{
			Role:         src.RoleAssistant,
			Content:      []src.ContentBlock{},
			Api:          model.Api,
			Provider:     model.Provider,
			Model:        model.ID,
			Usage:        src.Usage{Cost: src.UsageCost{}},
			StopReason:   src.StopReasonError,
			ErrorMessage: err.Error(),
		}
		ch <- src.AssistantMessageEvent{Type: "error", Reason: src.StopReasonError, Error: output}
		close(ch)
		return ch
	}
}

func buildAzureOpenAIResponsesSimpleOptions(model src.Model[src.Api], options *src.SimpleStreamOptions) *AzureOpenAIResponsesOptions {
	if options == nil {
		return nil
	}

	base := BuildBaseOptions(model, options, "")

	var reasoningEffort *string
	if options.Reasoning != "" {
		clamped := src.ClampThinkingLevel(&model, options.Reasoning)
		if clamped != "off" {
			effort := string(clamped)
			reasoningEffort = &effort
		}
	}

	return &AzureOpenAIResponsesOptions{
		APIKey:          base.APIKey,
		MaxTokens:       base.MaxTokens,
		Temperature:     base.Temperature,
		Headers:         base.Headers,
		SessionID:       base.SessionID,
		CacheRetention:  &base.CacheRetention,
		Context:         base.Context,
		ReasoningEffort: reasoningEffort,
	}
}
