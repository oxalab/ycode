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
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	anthropicAPIBaseURL      = "https://api.anthropic.com/v1/messages"
	anthropicVersionHeader   = "anthropic-version"
	anthropicVersion         = "2023-06-01"
	fineGrainedToolStreamingBeta = "fine-grained-tool-streaming-2025-05-14"
	interleavedThinkingBeta  = "interleaved-thinking-2025-05-14"
	claudeCodeVersion        = "2.1.75"
)

// =============================================================================
// Types
// =============================================================================

type AnthropicOptions struct {
	APIKey                string
	MaxTokens             *int
	Temperature           *float64
	ThinkingEnabled       *bool
	ThinkingBudgetTokens  *int
	Effort                *string
	ThinkingDisplay       *string
	InterleavedThinking   *bool
	ToolChoice            interface{}
	Headers               map[string]string
	Metadata              map[string]string
	SessionID             string
	CacheRetention        *src.CacheRetention
	// Callbacks
	OnPayload func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamAnthropic creates an async streaming function for Anthropic Messages API
func StreamAnthropic(model src.Model[src.Api], context src.ChatContext, options *AnthropicOptions) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			output := &src.AssistantMessage{
				Role:      src.RoleAssistant,
				Content:   []src.ContentBlock{},
				Api:       model.Api,
				Provider:  model.Provider,
				Model:     model.ID,
				Usage:     src.Usage{Cost: src.UsageCost{}},
				StopReason: src.StopReasonStop,
			}

			var err error
			var resp *http.Response

			// Resolve the API key once (options → env) so OAuth detection is
			// consistent between params and headers (reference does the same).
			apiKey := ""
			if options != nil && options.APIKey != "" {
				apiKey = options.APIKey
			} else {
				apiKey = getEnvApiKey(string(model.Provider))
			}
			isOAuth := isOAuthToken(apiKey)

			// Build and send request
			client := &http.Client{}
			params := buildAnthropicParams(model, context, isOAuth, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err = doAnthropicRequest(client, model, context, params, apiKey, options)
			if err != nil {
				handleAnthropicError(ch, output, err, false)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status": resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response
			if err := consumeAnthropicStream(resp.Body, ch, output, model, context, isOAuth, options); err != nil {
				handleAnthropicError(ch, output, err, false)
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// consumeAnthropicStream parses the Anthropic SSE stream into output/content
// events. Returned errors surface as error events (never aborts — abort
// requires a signal, which is P15).
func consumeAnthropicStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], context src.ChatContext, isOAuth bool, options *AnthropicOptions) error {
	blockIndexMap := make(map[int]int)
	sawMessageStart := false
	sawMessageStop := false
	sseEvent := "" // SSE `event:` field, tracked for error events

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.HasPrefix(line, "event:") {
			sseEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")

		if data == "[DONE]" {
			break
		}

		// SSE-level error events fail the stream with the raw payload
		// (reference iterateAnthropicEvents throws on sse.event === "error").
		if sseEvent == "error" {
			return fmt.Errorf("%s", data)
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("Could not parse Anthropic SSE event %s: %v; data=%s", sseEvent, err, data)
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "message_start":
			sawMessageStart = true
			if msg, ok := event["message"].(map[string]interface{}); ok {
				output.ResponseID, _ = msg["id"].(string)
				if usage, ok := msg["usage"].(map[string]interface{}); ok {
					if inputTokens, ok := usage["input_tokens"].(float64); ok {
						output.Usage.Input = int(inputTokens)
					}
					if outputTokens, ok := usage["output_tokens"].(float64); ok {
						output.Usage.Output = int(outputTokens)
					}
					if cacheRead, ok := usage["cache_read_input_tokens"].(float64); ok {
						output.Usage.CacheRead = int(cacheRead)
					}
					if cacheWrite, ok := usage["cache_creation_input_tokens"].(float64); ok {
						output.Usage.CacheWrite = int(cacheWrite)
					}
					output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
					// Reference calls calculateCost after message_start usage.
					output.Usage.Cost = usageCostFromModelCost(src.CalculateCost(&model, &output.Usage))
				}
			}

		case "content_block_start":
			index, _ := event["index"].(float64)
			if block, ok := event["content_block"].(map[string]interface{}); ok {
				blockType, _ := block["type"].(string)
				switch blockType {
				case "text":
					newBlock := &src.TextContent{Type: "text", Text: ""}
					output.Content = append(output.Content, newBlock)
					blockIndexMap[int(index)] = len(output.Content) - 1
					ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
				case "thinking":
					newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
					output.Content = append(output.Content, newBlock)
					blockIndexMap[int(index)] = len(output.Content) - 1
					ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
				case "redacted_thinking":
					signature, _ := block["data"].(string)
					redacted := true
					newBlock := &src.ThinkingContent{Type: "thinking", Thinking: "[Reasoning redacted]", ThinkingSignature: signature, Redacted: &redacted}
					output.Content = append(output.Content, newBlock)
					blockIndexMap[int(index)] = len(output.Content) - 1
					ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
				case "tool_use":
					id, _ := block["id"].(string)
					name, _ := block["name"].(string)
					if isOAuth {
						name = fromClaudeCodeName(name, context.Tools)
					}
					input := map[string]interface{}{}
					if i, ok := block["input"].(map[string]interface{}); ok {
						input = i
					}
					newBlock := &src.ToolCall{Type: "toolCall", ID: id, Name: name, Arguments: input}
					output.Content = append(output.Content, newBlock)
					blockIndexMap[int(index)] = len(output.Content) - 1
					ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
				}
			}

		case "content_block_delta":
			index, _ := event["index"].(float64)
			if idx, ok := blockIndexMap[int(index)]; ok && idx < len(output.Content) {
				if delta, ok := event["delta"].(map[string]interface{}); ok {
					deltaType, _ := delta["type"].(string)
					switch deltaType {
					case "text_delta":
						if text, ok := delta["text"].(string); ok {
							if tc, ok := output.Content[idx].(*src.TextContent); ok {
								tc.Text += text
								ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: ptr(idx), Delta: text, Partial: output}
							}
						}
					case "thinking_delta":
						if thinking, ok := delta["thinking"].(string); ok {
							if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
								tc.Thinking += thinking
								ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: ptr(idx), Delta: thinking, Partial: output}
							}
						}
					case "input_json_delta":
						if partialJson, ok := delta["partial_json"].(string); ok {
							if toolCall, ok := output.Content[idx].(*src.ToolCall); ok {
								toolCall.PartialArgs += partialJson
								toolCall.Arguments = parseStreamingJson(toolCall.PartialArgs)
								ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: ptr(idx), Delta: partialJson, Partial: output}
							}
						}
					case "signature_delta":
						if signature, ok := delta["signature"].(string); ok {
							if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
								tc.ThinkingSignature += signature
							}
						}
					}
				}
			}

		case "content_block_stop":
			index, _ := event["index"].(float64)
			if idx, ok := blockIndexMap[int(index)]; ok && idx < len(output.Content) {
				delete(blockIndexMap, int(index))
				switch block := output.Content[idx].(type) {
				case *src.TextContent:
					ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: ptr(idx), Content: block.Text, Partial: output}
				case *src.ThinkingContent:
					ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: ptr(idx), Content: block.Thinking, Partial: output}
				case *src.ToolCall:
					block.Arguments = parseStreamingJson(block.PartialArgs)
					ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: ptr(idx), ToolCall: block, Partial: output}
				}
			}

		case "message_delta":
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if stopReason, ok := delta["stop_reason"].(string); ok {
					reason, reasonErr := mapAnthropicStopReason(stopReason)
					output.StopReason = reason
					if reasonErr != "" {
						return fmt.Errorf("%s", reasonErr)
					}
				}
			}
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				if inputTokens, ok := usage["input_tokens"].(float64); ok {
					output.Usage.Input = int(inputTokens)
				}
				if outputTokens, ok := usage["output_tokens"].(float64); ok {
					output.Usage.Output = int(outputTokens)
				}
				if cacheRead, ok := usage["cache_read_input_tokens"].(float64); ok {
					output.Usage.CacheRead = int(cacheRead)
				}
				if cacheWrite, ok := usage["cache_creation_input_tokens"].(float64); ok {
					output.Usage.CacheWrite = int(cacheWrite)
				}
				output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
				output.Usage.Cost = usageCostFromModelCost(src.CalculateCost(&model, &output.Usage))
			}

		case "message_stop":
			sawMessageStop = true

		case "error":
			if errorMsg, ok := event["error"].(map[string]interface{}); ok {
				msg, _ := errorMsg["message"].(string)
				return fmt.Errorf("anthropic error: %s", msg)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		// A mid-stream read failure is an error, not an abort
		// (abort only comes from a signal — P15).
		return err
	}

	// Truncated stream: the reference fails when message_stop never arrived.
	if sawMessageStart && !sawMessageStop {
		return fmt.Errorf("Anthropic stream ended before message_stop")
	}

	if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
		return fmt.Errorf("stream ended with error: %s", output.StopReason)
	}
	return nil
}

// =============================================================================
// Request handling
// =============================================================================

func doAnthropicRequest(client *http.Client, model src.Model[src.Api], context src.ChatContext, params interface{}, apiKey string, options *AnthropicOptions) (*http.Response, error) {
	baseURL := anthropicAPIBaseURL
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	compat := getAnthropicCompat(model)

	// Beta features: interleaved thinking defaults ON (nil = true) for
	// non-adaptive models; fine-grained tool streaming applies to tool calls
	// when eager per-tool streaming is unsupported.
	interleavedThinking := options == nil || options.InterleavedThinking == nil || *options.InterleavedThinking
	needsInterleavedBeta := interleavedThinking && !supportsAdaptiveThinking(model.ID)
	var betaFeatures []string
	eager := compat.SupportsEagerToolInputStreaming == nil || *compat.SupportsEagerToolInputStreaming
	if len(context.Tools) > 0 && !eager {
		betaFeatures = append(betaFeatures, fineGrainedToolStreamingBeta)
	}
	if needsInterleavedBeta {
		betaFeatures = append(betaFeatures, interleavedThinkingBeta)
	}
	betaHeader := ""
	if len(betaFeatures) > 0 {
		betaHeader = strings.Join(betaFeatures, ",")
	}

	setStandardHeaders := func(req *http.Request) {
		req.Header.Set("accept", "application/json")
		req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
		if betaHeader != "" {
			req.Header.Set("anthropic-beta", betaHeader)
		}
	}

	isGateway := model.Provider == "cloudflare-ai-gateway"
	if isGateway {
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

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(anthropicVersionHeader, anthropicVersion)

	// Model headers merge before option headers (options win), matching the
	// reference mergeHeaders order.
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	switch {
	case isGateway:
		// Gateway authenticates via cf-aig-authorization; no x-api-key.
		setStandardHeaders(req)
		req.Header.Set("cf-aig-authorization", "Bearer "+apiKey)
	case model.Provider == src.GithubCopilot:
		// Bearer auth + selective betas + dynamic intent headers.
		setStandardHeaders(req)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		hasImages := HasCopilotVisionInput(contextMessagesParam(context))
		for k, v := range BuildCopilotDynamicHeaders(contextMessagesParam(context), hasImages) {
			req.Header.Set(k, v)
		}
	case isOAuthToken(apiKey):
		// OAuth: Bearer auth + Claude Code identity headers + CC betas.
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("anthropic-beta", strings.Join(append([]string{"claude-code-20250219", "oauth-2025-04-20"}, betaFeatures...), ","))
		req.Header.Set("User-Agent", "claude-cli/"+claudeCodeVersion)
		req.Header.Set("X-App", "cli")
		req.Header.Set("accept", "application/json")
	default:
		setStandardHeaders(req)
		req.Header.Set("x-api-key", apiKey)
		if options != nil && options.SessionID != "" && (compat.SendSessionAffinityHeaders != nil && *compat.SendSessionAffinityHeaders) {
			req.Header.Set("x-session-affinity", options.SessionID)
		}
	}

	// Add options headers last (override defaults)
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
	// check an error body has no `data:` lines and yields an empty "success".
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Anthropic API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(excerpt)))
	}
	return resp, nil
}

// getAnthropicCompat resolves per-model compat over provider auto-detection
// (reference getAnthropicCompat): Fireworks lacks eager tool streaming, long
// cache retention and tool cache control, and needs session affinity; the
// Cloudflare Anthropic gateway needs session affinity too.
func getAnthropicCompat(model src.Model[src.Api]) (compat src.AnthropicMessagesCompat) {
	isFireworks := model.Provider == "fireworks"
	isCloudflareAiGatewayAnthropic := model.Provider == "cloudflare-ai-gateway" && strings.Contains(model.BaseURL, "anthropic")

	compat.SupportsEagerToolInputStreaming = boolPtr(!isFireworks)
	compat.SupportsLongCacheRetention = boolPtr(!isFireworks)
	compat.SendSessionAffinityHeaders = boolPtr(isFireworks || isCloudflareAiGatewayAnthropic)
	compat.SupportsCacheControlOnTools = boolPtr(!isFireworks)

	if mc := model.Compat.AnthropicMessages; mc != nil {
		if mc.SupportsEagerToolInputStreaming != nil {
			compat.SupportsEagerToolInputStreaming = mc.SupportsEagerToolInputStreaming
		}
		if mc.SupportsLongCacheRetention != nil {
			compat.SupportsLongCacheRetention = mc.SupportsLongCacheRetention
		}
		if mc.SendSessionAffinityHeaders != nil {
			compat.SendSessionAffinityHeaders = mc.SendSessionAffinityHeaders
		}
		if mc.SupportsCacheControlOnTools != nil {
			compat.SupportsCacheControlOnTools = mc.SupportsCacheControlOnTools
		}
	}
	return
}

func boolPtr(v bool) *bool { return &v }

func handleAnthropicError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
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

func buildAnthropicParams(model src.Model[src.Api], context src.ChatContext, isOAuth bool, options *AnthropicOptions) interface{} {
	cacheControl := getAnthropicCacheControl(model, options)

	params := map[string]interface{}{
		"model":      model.ID,
		"messages":   convertAnthropicMessages(context.Messages, model, isOAuth, cacheControl),
		"max_tokens": getDefaultMaxTokens(model, options),
		"stream":     true,
	}

	// System prompt
	if isOAuth {
		// OAuth tokens MUST carry the Claude Code identity; cache_control
		// applies to both blocks (reference buildParams OAuth branch).
		system := []map[string]interface{}{
			{
				"type": "text",
				"text": "You are Claude Code, Anthropic's official CLI for Claude.",
			},
		}
		if cacheControl != nil {
			system[0]["cache_control"] = cacheControl
		}
		if context.SystemPrompt != "" {
			block := map[string]interface{}{
				"type": "text",
				"text": utils.SanitizeSurrogates(context.SystemPrompt),
			}
			if cacheControl != nil {
				block["cache_control"] = cacheControl
			}
			system = append(system, block)
		}
		params["system"] = system
	} else if context.SystemPrompt != "" {
		block := map[string]interface{}{
			"type": "text",
			"text": utils.SanitizeSurrogates(context.SystemPrompt),
		}
		if cacheControl != nil {
			block["cache_control"] = cacheControl
		}
		params["system"] = []map[string]interface{}{block}
	}

	// Temperature (incompatible with extended thinking)
	if options != nil && options.Temperature != nil {
		thinkingEnabled := false
		if options.ThinkingEnabled != nil {
			thinkingEnabled = *options.ThinkingEnabled
		}
		if !thinkingEnabled {
			params["temperature"] = *options.Temperature
		}
	}

	// Tools
	if len(context.Tools) > 0 {
		compat := getAnthropicCompat(model)
		cacheCtlForTools := cacheControl
		if compat.SupportsCacheControlOnTools != nil && !*compat.SupportsCacheControlOnTools {
			cacheCtlForTools = nil
		}
		eager := compat.SupportsEagerToolInputStreaming == nil || *compat.SupportsEagerToolInputStreaming
		params["tools"] = convertAnthropicTools(context.Tools, isOAuth, eager, cacheCtlForTools)
	}

	// Thinking mode
	if model.Reasoning {
		thinkingEnabled := false
		if options != nil && options.ThinkingEnabled != nil {
			thinkingEnabled = *options.ThinkingEnabled
		}

		if thinkingEnabled {
			display := "summarized"
			if options != nil && options.ThinkingDisplay != nil {
				display = *options.ThinkingDisplay
			}

			if supportsAdaptiveThinking(model.ID) {
				params["thinking"] = map[string]interface{}{
					"type":    "adaptive",
					"display": display,
				}
				if options != nil && options.Effort != nil {
					// output_config is a TOP-LEVEL sibling of thinking
					// (reference: params.output_config = { effort }).
					params["output_config"] = map[string]interface{}{
						"effort": *options.Effort,
					}
				}
			} else {
				budget := 1024
				if options != nil && options.ThinkingBudgetTokens != nil {
					budget = *options.ThinkingBudgetTokens
				}
				params["thinking"] = map[string]interface{}{
					"type":          "enabled",
					"budget_tokens": budget,
					"display":       display,
				}
			}
		} else if options != nil && options.ThinkingEnabled != nil && !*options.ThinkingEnabled {
			params["thinking"] = map[string]interface{}{
				"type": "disabled",
			}
		}
	}

	// Tool choice: the API expects an object ({type: ...}); wrap bare strings.
	if options != nil && options.ToolChoice != nil {
		switch tc := options.ToolChoice.(type) {
		case string:
			params["tool_choice"] = map[string]interface{}{"type": tc}
		default:
			params["tool_choice"] = tc
		}
	}

	// Metadata
	if options != nil && options.Metadata != nil {
		if userID, ok := options.Metadata["user_id"]; ok {
			params["metadata"] = map[string]interface{}{
				"user_id": userID,
			}
		}
	}

	return params
}

func convertAnthropicMessages(messages []src.Message, model src.Model[src.Api], isOAuth bool, cacheControl map[string]interface{}) []map[string]interface{} {
	var params []map[string]interface{}

	transformedMessages := TransformMessages(messages, model, normalizeAnthropicToolCallId)

	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]
		role := msgRole(msg)

		if role == src.RoleUser {
			um, ok := msg.(*src.UserMessage)
			if !ok {
				continue
			}

			if len(um.Content) == 1 {
				if tc, ok := um.Content[0].(*src.TextContent); ok && strings.TrimSpace(tc.Text) != "" {
					params = append(params, map[string]interface{}{
						"role":    "user",
						"content": utils.SanitizeSurrogates(tc.Text),
					})
					continue
				}
			}

			var blocks []map[string]interface{}
			for _, item := range um.Content {
				switch c := item.(type) {
				case *src.TextContent:
					if strings.TrimSpace(c.Text) == "" {
						continue
					}
					blocks = append(blocks, map[string]interface{}{
						"type": "text",
						"text": utils.SanitizeSurrogates(c.Text),
					})
				case *src.ImageContent:
					blocks = append(blocks, map[string]interface{}{
						"type": "image",
						"source": map[string]string{
							"type":       "base64",
							"media_type": c.MimeType,
							"data":       c.Data,
						},
					})
				}
			}

			if len(blocks) == 0 {
				continue
			}

			params = append(params, map[string]interface{}{
				"role":    "user",
				"content": blocks,
			})
		} else if role == src.RoleAssistant {
			am := okAssistant(msg)
			if am == nil {
				continue
			}

			var blocks []map[string]interface{}
			for _, block := range am.Content {
				switch b := block.(type) {
				case *src.TextContent:
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					blocks = append(blocks, map[string]interface{}{
						"type": "text",
						"text": utils.SanitizeSurrogates(b.Text),
					})
				case *src.ThinkingContent:
					// Redacted check FIRST — a redacted block replays as
					// redacted_thinking even with empty thinking text.
					if b.Redacted != nil && *b.Redacted {
						blocks = append(blocks, map[string]interface{}{
							"type": "redacted_thinking",
							"data": b.ThinkingSignature,
						})
						continue
					}
					if strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					if strings.TrimSpace(b.ThinkingSignature) == "" {
						blocks = append(blocks, map[string]interface{}{
							"type": "text",
							"text": utils.SanitizeSurrogates(b.Thinking),
						})
					} else {
						blocks = append(blocks, map[string]interface{}{
							"type":      "thinking",
							"thinking":  utils.SanitizeSurrogates(b.Thinking),
							"signature": b.ThinkingSignature,
						})
					}
				case *src.ToolCall:
					name := b.Name
					if isOAuth {
						name = toClaudeCodeName(b.Name)
					}
					blocks = append(blocks, map[string]interface{}{
						"type":  "tool_use",
						"id":    b.ID,
						"name":  name,
						"input": b.Arguments,
					})
				}
			}

			if len(blocks) == 0 {
				continue
			}

			params = append(params, map[string]interface{}{
				"role":    "assistant",
				"content": blocks,
			})
		} else if role == src.RoleTool {
			tr, ok := msg.(*src.ToolResultMessage[any])
			if !ok {
				continue
			}

			// Collect consecutive tool results
			var toolResults []map[string]interface{}
			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": tr.ToolCallID,
				"content":     convertAnthropicContentBlocks(tr.Content),
				"is_error":    tr.IsError,
			})

			j := i + 1
			for j < len(transformedMessages) && msgRole(transformedMessages[j]) == src.RoleTool {
				nextMsg := transformedMessages[j]
				if nextTr, ok := nextMsg.(*src.ToolResultMessage[any]); ok {
					toolResults = append(toolResults, map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": nextTr.ToolCallID,
						"content":     convertAnthropicContentBlocks(nextTr.Content),
						"is_error":    nextTr.IsError,
					})
				}
				j++
			}

			i = j - 1

			params = append(params, map[string]interface{}{
				"role":    "user",
				"content": toolResults,
			})
		}
	}

	// Add cache_control to last user message
	if cacheControl != nil && len(params) > 0 {
		lastMsg := params[len(params)-1]
		if lastMsg["role"] == "user" {
			if content, ok := lastMsg["content"]; ok {
				switch c := content.(type) {
				case string:
					lastMsg["content"] = []map[string]interface{}{
						{
							"type":         "text",
							"text":         c,
							"cache_control": cacheControl,
						},
					}
				case []map[string]interface{}:
					if len(c) > 0 {
						c[len(c)-1]["cache_control"] = cacheControl
					}
				}
			}
		}
	}

	return params
}

// convertAnthropicContentBlocks mirrors convertContentBlocks: text-only
// content becomes one joined string; image content becomes a block array
// with a leading placeholder when no text is present.
func convertAnthropicContentBlocks(content []src.ContentBlock) interface{} {
	hasImages := false
	for _, c := range content {
		if _, ok := c.(*src.ImageContent); ok {
			hasImages = true
			break
		}
	}

	if !hasImages {
		var texts []string
		for _, c := range content {
			if tc, ok := c.(*src.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
		return utils.SanitizeSurrogates(strings.Join(texts, "\n"))
	}

	var blocks []map[string]interface{}
	hasText := false
	for _, c := range content {
		switch block := c.(type) {
		case *src.TextContent:
			hasText = true
			blocks = append(blocks, map[string]interface{}{
				"type": "text",
				"text": utils.SanitizeSurrogates(block.Text),
			})
		case *src.ImageContent:
			blocks = append(blocks, map[string]interface{}{
				"type": "image",
				"source": map[string]string{
					"type":       "base64",
					"media_type": block.MimeType,
					"data":       block.Data,
				},
			})
		}
	}

	if !hasText {
		placeholder := map[string]interface{}{
			"type": "text",
			"text": "(see attached image)",
		}
		blocks = append([]map[string]interface{}{placeholder}, blocks...)
	}

	return blocks
}

func convertAnthropicTools(tools []src.Tool[any], isOAuth bool, supportsEagerToolInputStreaming bool, cacheControl map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		name := tool.Name
		if isOAuth {
			name = toClaudeCodeName(name)
		}

		parameters, _ := tool.Parameters.(map[string]interface{})
		properties := map[string]interface{}{}
		required := []string{}
		if parameters != nil {
			if p, ok := parameters["properties"].(map[string]interface{}); ok {
				properties = p
			}
			if req, ok := parameters["required"].([]interface{}); ok {
				for _, r := range req {
					if s, ok := r.(string); ok {
						required = append(required, s)
					}
				}
			}
		}

		toolDef := map[string]interface{}{
			"name":        name,
			"description": tool.Description,
			"input_schema": map[string]interface{}{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		}
		if supportsEagerToolInputStreaming {
			toolDef["eager_input_streaming"] = true
		}
		if cacheControl != nil && i == len(tools)-1 {
			toolDef["cache_control"] = cacheControl
		}

		result[i] = toolDef
	}
	return result
}

// =============================================================================
// Helpers
// =============================================================================

func isOAuthToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

func supportsAdaptiveThinking(modelId string) bool {
	lower := strings.ToLower(modelId)
	return strings.Contains(lower, "opus-4-6") || strings.Contains(lower, "opus-4.6") ||
		strings.Contains(lower, "opus-4-7") || strings.Contains(lower, "opus-4.7") ||
		strings.Contains(lower, "sonnet-4-6") || strings.Contains(lower, "sonnet-4.6")
}

func normalizeAnthropicToolCallId(id string, _ src.Model[src.Api], _ src.AssistantMessage) string {
	var result strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
	}
	sanitized := result.String()
	if len(sanitized) > 64 {
		sanitized = sanitized[:64]
	}
	return sanitized
}

func getAnthropicCacheControl(model src.Model[src.Api], options *AnthropicOptions) map[string]interface{} {
	retention := src.CacheRetentionShort
	if options != nil && options.CacheRetention != nil {
		retention = *options.CacheRetention
	} else if os.Getenv("PI_CACHE_RETENTION") == "long" {
		retention = src.CacheRetentionLong
	}

	if retention == src.CacheRetentionNone {
		return nil
	}

	cacheControl := map[string]interface{}{
		"type": "ephemeral",
	}

	// Long retention only when the provider supports the 1h TTL.
	if retention == src.CacheRetentionLong {
		if compat := getAnthropicCompat(model); compat.SupportsLongCacheRetention != nil && *compat.SupportsLongCacheRetention {
			cacheControl["ttl"] = "1h"
		}
	}

	return cacheControl
}

func getDefaultMaxTokens(model src.Model[src.Api], options *AnthropicOptions) int {
	if options != nil && options.MaxTokens != nil {
		return *options.MaxTokens
	}
	// Reference: (model.maxTokens / 3) | 0 — may be 0 for uncataloged models,
	// which the API rejects with a clear error (no invented fallback).
	if model.MaxTokens > 0 {
		return model.MaxTokens / 3
	}
	return 0
}

// mapAnthropicStopReason returns the stop reason plus an error message for
// unknown values (the reference throws on unhandled reasons, surfacing as an
// error event).
func mapAnthropicStopReason(reason string) (src.StopReason, string) {
	switch reason {
	case "end_turn":
		return src.StopReasonStop, ""
	case "max_tokens":
		return src.StopReasonLength, ""
	case "tool_use":
		return src.StopReasonToolUse, ""
	case "refusal":
		return src.StopReasonError, ""
	case "pause_turn":
		return src.StopReasonStop, ""
	case "stop_sequence":
		return src.StopReasonStop, ""
	case "sensitive":
		// Content flagged by safety filters.
		return src.StopReasonError, ""
	default:
		return src.StopReasonError, "Unhandled stop reason: " + reason
	}
}

func ptr(i int) *int {
	return &i
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleAnthropic provides a simplified streaming interface for Anthropic
func StreamSimpleAnthropic(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	// The reference throws synchronously when no key is configured.
	apiKey := ""
	if options != nil && options.APIKey != "" {
		apiKey = options.APIKey
	} else {
		apiKey = getEnvApiKey(string(model.Provider))
	}
	opts := buildAnthropicSimpleOptions(model, options, apiKey)
	return StreamAnthropic(model, context, opts)
}

func buildAnthropicSimpleOptions(model src.Model[src.Api], options *src.SimpleStreamOptions, apiKey string) *AnthropicOptions {
	if options == nil {
		return nil
	}

	opts := &AnthropicOptions{
		APIKey:    apiKey,
		MaxTokens: options.MaxTokens,
		Headers:   options.Headers,
	}

	if options.Reasoning == "" {
		disabled := false
		opts.ThinkingEnabled = &disabled
		return opts
	}

	// Adaptive-thinking models: effort level (thinkingLevelMap lookup is P15).
	if supportsAdaptiveThinking(model.ID) {
		enabled := true
		opts.ThinkingEnabled = &enabled
		opts.Effort = anthropicEffortPtr(options.Reasoning)
		return opts
	}

	// Older models: budget-based thinking with an adjusted maxTokens budget.
	adjusted := AdjustMaxTokensForThinking(derefInt(options.MaxTokens), model.MaxTokens, options.Reasoning, options.ThinkingBudgets)
	enabled := true
	opts.ThinkingEnabled = &enabled
	opts.MaxTokens = &adjusted.MaxTokens
	opts.ThinkingBudgetTokens = &adjusted.ThinkingBudget
	return opts
}

// anthropicEffortPtr maps a thinking level to the reference effort table.
func anthropicEffortPtr(level src.ThinkingLevel) *string {
	var effort string
	switch level {
	case "minimal", "low":
		effort = "low"
	case "medium":
		effort = "medium"
	default:
		effort = "high"
	}
	return &effort
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
