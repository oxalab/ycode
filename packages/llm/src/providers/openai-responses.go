package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	src "ycode/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

const (
	openAIResponsesBaseURL = "https://api.openai.com/v1/responses"
)

// =============================================================================
// Types
// =============================================================================

type OpenAIResponsesOptions struct {
	APIKey             string
	MaxTokens          *int
	MaxOutputTokens    *int
	Temperature        *float64
	ServiceTier        *string
	ReasoningEffort    *string
	ReasoningSummary   *string
	Headers            map[string]string
	SessionID          string
	CacheRetention     *src.CacheRetention
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Provider registration
// =============================================================================

// StreamOpenAIResponses creates an async streaming function for OpenAI Responses API
func StreamOpenAIResponses(model src.Model[src.Api], context src.ChatContext, options *OpenAIResponsesOptions) func() <-chan src.AssistantMessageEvent {
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
			params := buildOpenAIResponsesParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doOpenAIResponsesRequest(client, model, context, params, options)
			if err != nil {
				handleOpenAIResponsesError(ch, output, err, false)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status": resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response. No signal support yet (P15), so a
			// mid-stream read error is an error, never an abort.
			if err := processOpenAIResponsesStream(resp.Body, ch, output, model, options); err != nil {
				handleOpenAIResponsesError(ch, output, err, false)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleOpenAIResponsesError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
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

func doOpenAIResponsesRequest(client *http.Client, model src.Model[src.Api], context src.ChatContext, params interface{}, options *OpenAIResponsesOptions) (*http.Response, error) {
	baseURL := openAIResponsesBaseURL
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	// Cloudflare Workers AI / AI Gateway: resolve {VAR} placeholders.
	isCloudflare := IsCloudflareProvider(string(model.Provider))
	if isCloudflare {
		resolved, err := ResolveCloudflareBaseUrl(model)
		if err != nil {
			return nil, err
		}
		baseURL = resolved
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
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required. Set OPENAI_API_KEY environment variable or pass it as an argument.")
	}

	// The AI Gateway authenticates via cf-aig-authorization; the plain
	// Authorization header is dropped (reference createClient defaultHeaders).
	if isCloudflare {
		req.Header.Set("cf-aig-authorization", "Bearer "+apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// Add model headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	// Add Copilot dynamic headers if applicable
	if model.Provider == src.GithubCopilot {
		hasImages := HasCopilotVisionInput(contextMessagesParam(context))
		copilotHeaders := BuildCopilotDynamicHeaders(contextMessagesParam(context), hasImages)
		for k, v := range copilotHeaders {
			req.Header.Set(k, v)
		}
	}

	// Add session ID headers. session_id honors the compat flag; the
	// x-client-request-id correlation header is unconditional.
	if options != nil && options.SessionID != "" {
		sendSessionID, _ := getResponsesCompat(model)
		if sendSessionID {
			req.Header.Set("session_id", options.SessionID)
		}
		req.Header.Set("x-client-request-id", options.SessionID)
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	// The reference SDK throws a structured APIError on non-2xx; without this
	// check an error body parses as junk and yields an empty "success".
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Responses API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(excerpt)))
	}
	return resp, nil
}

// getResponsesCompat resolves per-model compat over the reference defaults
// (both flags default true).
func getResponsesCompat(model src.Model[src.Api]) (sendSessionID, supportsLongRetention bool) {
	sendSessionID, supportsLongRetention = true, true
	if mc := model.Compat.OpenAIResponses; mc != nil {
		if mc.SendSessionIDHeader != nil {
			sendSessionID = *mc.SendSessionIDHeader
		}
		if mc.SupportsLongCacheRetention != nil {
			supportsLongRetention = *mc.SupportsLongCacheRetention
		}
	}
	return
}

func handleOpenAIResponsesError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
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

func buildOpenAIResponsesParams(model src.Model[src.Api], context src.ChatContext, options *OpenAIResponsesOptions) interface{} {
	allowedToolCallProviders := map[string]bool{
		"openai":         true,
		"openai-codex":   true,
		"opencode":       true,
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

		// Only set reasoning if explicitly configured
		if options != nil && (options.ReasoningEffort != nil || options.ReasoningSummary != nil) {
			params["reasoning"] = map[string]interface{}{
				"effort":  effort,
				"summary": summary,
			}
			params["include"] = []string{"reasoning.encrypted_content"}
		} else if model.Provider != src.GithubCopilot {
			params["reasoning"] = map[string]interface{}{
				"effort": "none",
			}
		}
	}

	// Prompt cache
	cacheRetention := src.CacheRetentionShort
	if options != nil && options.CacheRetention != nil {
		cacheRetention = *options.CacheRetention
	} else if os.Getenv("PI_CACHE_RETENTION") == "long" {
		cacheRetention = src.CacheRetentionLong
	}
	if cacheRetention != src.CacheRetentionNone {
		if options != nil && options.SessionID != "" {
			params["prompt_cache_key"] = options.SessionID
		}
		if cacheRetention == src.CacheRetentionLong {
			if _, longRetention := getResponsesCompat(model); longRetention {
				params["prompt_cache_retention"] = "24h"
			}
		}
	}

	return params
}

// =============================================================================
// Stream processing
// =============================================================================

func processOpenAIResponsesStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], options *OpenAIResponsesOptions) error {
	var currentItem map[string]interface{}
	var currentBlockIndex *int
	// The reference gates text/refusal deltas on the last content part added
	// (content_part.added bookkeeping) and reasoning summary deltas on a
	// summary part having started.
	lastContentPartType := ""
	summaryPartStarted := false

	// The response is an SSE stream (`stream: true`): `event:`/`data:` lines.
	// A raw json.Decoder can't read SSE — parse it line by line, mirroring
	// the completions processor.
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue // skip a malformed line rather than aborting the stream
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "response.created":
			if response, ok := event["response"].(map[string]interface{}); ok {
				if id, ok := response["id"].(string); ok {
					output.ResponseID = id
				}
			}

		case "response.output_item.added":
			if item, ok := event["item"].(map[string]interface{}); ok {
				itemType, _ := item["type"].(string)
				currentItem = item
				lastContentPartType = ""
				summaryPartStarted = false

				switch itemType {
				case "reasoning":
					newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					currentBlockIndex = &idx
					ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}

				case "message":
					newBlock := &src.TextContent{Type: "text", Text: ""}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					currentBlockIndex = &idx
					ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}

				case "function_call":
					callId, _ := item["call_id"].(string)
					id, _ := item["id"].(string)
					name, _ := item["name"].(string)
					args, _ := item["arguments"].(string)

					newBlock := &src.ToolCall{
						Type:        "toolCall",
						ID:          callId + "|" + id,
						Name:        name,
						Arguments:   map[string]interface{}{},
						PartialArgs: args, // some providers send full args on the added event
					}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					currentBlockIndex = &idx
					ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &idx, Partial: output}
				}
			}

		case "response.reasoning_summary_part.added":
			if currentItem != nil && currentItem["type"] == "reasoning" {
				summaryPartStarted = true
			}

		case "response.reasoning_summary_text.delta":
			if currentItem != nil && currentItem["type"] == "reasoning" && summaryPartStarted &&
				currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if tc, ok := output.Content[*currentBlockIndex].(*src.ThinkingContent); ok {
					delta, _ := event["delta"].(string)
					tc.Thinking += delta
					ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.reasoning_summary_part.done":
			if currentItem != nil && currentItem["type"] == "reasoning" && summaryPartStarted &&
				currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if tc, ok := output.Content[*currentBlockIndex].(*src.ThinkingContent); ok {
					tc.Thinking += "\n\n"
					ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: currentBlockIndex, Delta: "\n\n", Partial: output}
				}
			}

		case "response.reasoning_text.delta":
			if currentItem != nil && currentItem["type"] == "reasoning" &&
				currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if tc, ok := output.Content[*currentBlockIndex].(*src.ThinkingContent); ok {
					delta, _ := event["delta"].(string)
					tc.Thinking += delta
					ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.content_part.added":
			if currentItem != nil && currentItem["type"] == "message" {
				if part, ok := event["part"].(map[string]interface{}); ok {
					partType, _ := part["type"].(string)
					// Only output_text and refusal parts carry streamable text.
					if partType == "output_text" || partType == "refusal" {
						lastContentPartType = partType
					}
				}
			}

		case "response.output_text.delta":
			if lastContentPartType == "output_text" &&
				currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if tc, ok := output.Content[*currentBlockIndex].(*src.TextContent); ok {
					delta, _ := event["delta"].(string)
					tc.Text += delta
					ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.refusal.delta":
			if lastContentPartType == "refusal" &&
				currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if tc, ok := output.Content[*currentBlockIndex].(*src.TextContent); ok {
					delta, _ := event["delta"].(string)
					tc.Text += delta
					ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.function_call_arguments.delta":
			if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if toolCall, ok := output.Content[*currentBlockIndex].(*src.ToolCall); ok {
					delta, _ := event["delta"].(string)
					toolCall.PartialArgs += delta
					toolCall.Arguments = parseStreamingJson(toolCall.PartialArgs)
					ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.function_call_arguments.done":
			if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if toolCall, ok := output.Content[*currentBlockIndex].(*src.ToolCall); ok {
					if arguments, ok := event["arguments"].(string); ok {
						previous := toolCall.PartialArgs
						toolCall.PartialArgs = arguments
						toolCall.Arguments = parseStreamingJson(arguments)
						// Catch-up delta for the suffix beyond the last partial.
						if strings.HasPrefix(arguments, previous) {
							if extra := arguments[len(previous):]; extra != "" {
								ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: currentBlockIndex, Delta: extra, Partial: output}
							}
						}
					}
				}
			}

		case "response.output_item.done":
			if item, ok := event["item"].(map[string]interface{}); ok {
				itemType, _ := item["type"].(string)

				switch itemType {
				case "reasoning":
					if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
						if tc, ok := output.Content[*currentBlockIndex].(*src.ThinkingContent); ok {
							summaryText := joinTextParts(item["summary"])
							contentText := joinTextParts(item["content"])
							// The authoritative item content wins; the live
							// accumulation is only the fallback.
							if summaryText != "" {
								tc.Thinking = summaryText
							} else if contentText != "" {
								tc.Thinking = contentText
							}
							sig, _ := json.Marshal(item)
							tc.ThinkingSignature = string(sig)
							ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: currentBlockIndex, Content: tc.Thinking, Partial: output}
						}
					}
					currentBlockIndex = nil

				case "message":
					if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
						if tc, ok := output.Content[*currentBlockIndex].(*src.TextContent); ok {
							// Authoritative final content: output_text parts
							// contribute text, refusal parts their refusal.
							if content, ok := item["content"].([]interface{}); ok {
								var textParts []string
								for _, c := range content {
									if cm, ok := c.(map[string]interface{}); ok {
										switch cm["type"] {
										case "output_text":
											if t, ok := cm["text"].(string); ok {
												textParts = append(textParts, t)
											}
										case "refusal":
											if r, ok := cm["refusal"].(string); ok {
												textParts = append(textParts, r)
											}
										}
									}
								}
								tc.Text = strings.Join(textParts, "")
							}
							id, _ := item["id"].(string)
							// Phase is omitted when absent (TS `item.phase ?? undefined`).
							if phase, ok := item["phase"].(string); ok && phase != "" {
								tc.TextSignature = encodeTextSignatureV1(id, &phase)
							} else {
								tc.TextSignature = encodeTextSignatureV1(id, nil)
							}
							ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: currentBlockIndex, Content: tc.Text, Partial: output}
						}
					}
					currentBlockIndex = nil

				case "function_call":
					if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
						if toolCall, ok := output.Content[*currentBlockIndex].(*src.ToolCall); ok {
							callId, _ := item["call_id"].(string)
							id, _ := item["id"].(string)
							name, _ := item["name"].(string)
							toolCall.ID = callId + "|" + id
							toolCall.Name = name
							args := ""
							if toolCall.PartialArgs != "" {
								args = toolCall.PartialArgs
							} else if a, ok := item["arguments"].(string); ok {
								args = a
							}
							if args == "" {
								args = "{}"
							}
							toolCall.Arguments = parseStreamingJson(args)
							ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: currentBlockIndex, ToolCall: toolCall, Partial: output}
						}
					}
					currentBlockIndex = nil
				}
			}

		case "response.completed":
			if response, ok := event["response"].(map[string]interface{}); ok {
				if id, ok := response["id"].(string); ok {
					output.ResponseID = id
				}
				if usage, ok := response["usage"].(map[string]interface{}); ok {
					cachedTokens := 0.0
					if details, ok := usage["input_tokens_details"].(map[string]interface{}); ok {
						if ct, ok := details["cached_tokens"].(float64); ok {
							cachedTokens = ct
						}
					}

					inputTokens, _ := usage["input_tokens"].(float64)
					outputTokens, _ := usage["output_tokens"].(float64)
					totalTokens, _ := usage["total_tokens"].(float64)

					output.Usage.Input = int(inputTokens - cachedTokens)
					output.Usage.Output = int(outputTokens)
					output.Usage.CacheRead = int(cachedTokens)
					output.Usage.CacheWrite = 0
					output.Usage.TotalTokens = int(totalTokens)
				}
				// Reference calls calculateCost after usage mapping.
				output.Usage.Cost = usageCostFromModelCost(src.CalculateCost(&model, &output.Usage))
				applyOpenAIResponsesServiceTierPricing(&output.Usage.Cost, response, options, model.ID)

				status, _ := response["status"].(string)
				output.StopReason = mapOpenAIResponsesStopReason(status)
				if hasToolCalls(output.Content) && output.StopReason == src.StopReasonStop {
					output.StopReason = src.StopReasonToolUse
				}
			}

		case "error":
			code, _ := event["code"].(string)
			message, _ := event["message"].(string)
			return fmt.Errorf("Error Code %s: %s", code, message)

		case "response.failed":
			if response, ok := event["response"].(map[string]interface{}); ok {
				if errObj, ok := response["error"].(map[string]interface{}); ok {
					code, _ := errObj["code"].(string)
					msg, _ := errObj["message"].(string)
					return fmt.Errorf("%s: %s", code, msg)
				}
				if details, ok := response["incomplete_details"].(map[string]interface{}); ok {
					if reason, ok := details["reason"].(string); ok {
						return fmt.Errorf("incomplete: %s", reason)
					}
				}
			}
			return fmt.Errorf("Unknown error (no error details in response)")
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

// joinTextParts joins the "text" field of each entry of a decoded array of
// parts with "\n\n" (the reference's `item.summary?.map(s => s.text).join("\n\n")`).
func joinTextParts(v interface{}) string {
	parts, ok := v.([]interface{})
	if !ok {
		return ""
	}
	var out []string
	for _, p := range parts {
		if pm, ok := p.(map[string]interface{}); ok {
			if t, ok := pm["text"].(string); ok {
				out = append(out, t)
			}
		}
	}
	return strings.Join(out, "\n\n")
}

// applyOpenAIResponsesServiceTierPricing mirrors applyServiceTierPricing:
// flex ×0.5, priority ×2 (×2.5 for gpt-5.5), resolved from the response tier
// falling back to the requested tier.
func applyOpenAIResponsesServiceTierPricing(cost *src.UsageCost, response map[string]interface{}, options *OpenAIResponsesOptions, modelID string) {
	tier := ""
	if options != nil && options.ServiceTier != nil {
		tier = *options.ServiceTier
	}
	if rt, ok := response["service_tier"].(string); ok && rt != "" {
		tier = rt
	}
	multiplier := 1.0
	switch tier {
	case "flex":
		multiplier = 0.5
	case "priority":
		if modelID == "gpt-5.5" {
			multiplier = 2.5
		} else {
			multiplier = 2
		}
	}
	if multiplier == 1 {
		return
	}
	cost.Input *= multiplier
	cost.Output *= multiplier
	cost.CacheRead *= multiplier
	cost.CacheWrite *= multiplier
	cost.Total = cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite
}

// =============================================================================
// Helpers
// =============================================================================

func contextMessagesParam(context src.ChatContext) []src.Message {
	return context.Messages
}

func hasToolCalls(content []src.ContentBlock) bool {
	for _, c := range content {
		if _, ok := c.(*src.ToolCall); ok {
			return true
		}
	}
	return false
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleOpenAIResponses provides a simplified streaming interface for OpenAI Responses
func StreamSimpleOpenAIResponses(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	opts := buildOpenAIResponsesSimpleOptions(options)
	return StreamOpenAIResponses(model, context, opts)
}

func buildOpenAIResponsesSimpleOptions(options *src.SimpleStreamOptions) *OpenAIResponsesOptions {
	if options == nil {
		return nil
	}

	reasoningEffort := ""
	if options.Reasoning != "" {
		reasoningEffort = string(options.Reasoning)
	}

	return &OpenAIResponsesOptions{
		APIKey:          options.APIKey,
		MaxTokens:       options.MaxTokens,
		Temperature:     options.Temperature,
		ReasoningEffort: &reasoningEffort,
		Headers:         options.Headers,
	}
}
