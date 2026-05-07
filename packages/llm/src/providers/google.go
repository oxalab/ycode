package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	googleGenerativeAIBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
)

// =============================================================================
// Types
// =============================================================================

type GoogleOptions struct {
	APIKey     string
	MaxTokens  *int
	Temperature *float64
	ToolChoice *string
	Thinking   *GoogleThinkingConfig
	Headers    map[string]string
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

type GoogleThinkingConfig struct {
	Enabled      bool
	BudgetTokens *int
	Level        *string
}

// =============================================================================
// Provider registration
// =============================================================================

// StreamGoogle creates an async streaming function for Google Generative AI
func StreamGoogle(model src.Model[src.Api], context src.ChatContext, options *GoogleOptions) func() <-chan src.AssistantMessageEvent {
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
			params := buildGoogleParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doGoogleRequest(client, model, params, options)
			if err != nil {
				handleGoogleError(ch, output, err, false)
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
			if err := processGoogleStream(resp.Body, ch, output, model); err != nil {
				handleGoogleError(ch, output, err, true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleGoogleError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
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

func doGoogleRequest(client *http.Client, model src.Model[src.Api], params interface{}, options *GoogleOptions) (*http.Response, error) {
	baseURL := googleGenerativeAIBaseURL
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	// Build URL with API key
	apiKey := ""
	if options != nil && options.APIKey != "" {
		apiKey = options.APIKey
	}
	if apiKey == "" {
		apiKey = getEnvApiKey(string(model.Provider))
	}

	url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse&key=%s",
		baseURL, model.ID, apiKey)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

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

func handleGoogleError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
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

func buildGoogleParams(model src.Model[src.Api], context src.ChatContext, options *GoogleOptions) interface{} {
	contents := convertGoogleMessages(model, context)

	config := map[string]interface{}{}

	if context.SystemPrompt != "" {
		config["systemInstruction"] = utils.SanitizeSurrogates(context.SystemPrompt)
	}

	if options != nil && options.Temperature != nil {
		config["temperature"] = *options.Temperature
	}

	if options != nil && options.MaxTokens != nil {
		config["maxOutputTokens"] = *options.MaxTokens
	}

	if len(context.Tools) > 0 {
		config["tools"] = convertGoogleTools(context.Tools, false)
	}

	if options != nil && options.ToolChoice != nil {
		config["toolConfig"] = map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{
				"mode": mapGoogleToolChoice(*options.ToolChoice),
			},
		}
	}

	// Thinking configuration
	if model.Reasoning {
		if options != nil && options.Thinking != nil && options.Thinking.Enabled {
			thinkingConfig := map[string]interface{}{
				"includeThoughts": true,
			}
			if options.Thinking.Level != nil {
				thinkingConfig["thinkingLevel"] = *options.Thinking.Level
			} else if options.Thinking.BudgetTokens != nil {
				thinkingConfig["thinkingBudget"] = *options.Thinking.BudgetTokens
			}
			config["thinkingConfig"] = thinkingConfig
		} else if options != nil && options.Thinking != nil && !options.Thinking.Enabled {
			config["thinkingConfig"] = getDisabledGoogleThinkingConfig(model)
		}
	}

	return map[string]interface{}{
		"contents": contents,
		"config":   config,
	}
}

func getDisabledGoogleThinkingConfig(model src.Model[src.Api]) map[string]interface{} {
	// Gemini 3.1 Pro cannot disable thinking
	if isGemini3ProModel(model.ID) {
		return map[string]interface{}{
			"thinkingLevel": "LOW",
		}
	}

	// Gemini 3 Flash and Gemma 4 use minimal thinking
	if isGemini3FlashModel(model.ID) || isGemma4Model(model.ID) {
		return map[string]interface{}{
			"thinkingLevel": "MINIMAL",
		}
	}

	// Gemini 2.x supports disabling via thinkingBudget = 0
	return map[string]interface{}{
		"thinkingBudget": 0,
	}
}

func mapGoogleToolChoice(choice string) string {
	switch strings.ToLower(choice) {
	case "auto":
		return "AUTO"
	case "none":
		return "NONE"
	case "any":
		return "ANY"
	default:
		return "AUTO"
	}
}

// =============================================================================
// Stream processing
// =============================================================================

func processGoogleStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api]) error {
	decoder := json.NewDecoder(body)

	var currentBlock struct {
		Type                string
		Text                string
		Thinking            string
		TextSignature       string
		ThinkingSignature   string
		ID                  string
		Name                string
		Arguments           map[string]interface{}
	}

	currentBlock.Type = ""

	// Tool call counter for generating unique IDs
	toolCallCounter := 0

	for decoder.More() {
		var chunk map[string]interface{}
		if err := decoder.Decode(&chunk); err != nil {
			return err
		}

		// Set response ID
		if responseID, ok := chunk["responseId"].(string); ok && output.ResponseID == "" {
			output.ResponseID = responseID
		}

		// Get candidate
		candidates, ok := chunk["candidates"].([]interface{})
		if !ok || len(candidates) == 0 {
			continue
		}

		candidate, ok := candidates[0].(map[string]interface{})
		if !ok {
			continue
		}

		// Parse finish reason
		if finishReason, ok := candidate["finishReason"].(string); ok {
			output.StopReason = mapGoogleStopReason(finishReason)
			if hasToolCallsInContent(output.Content) {
				output.StopReason = src.StopReasonToolUse
			}
		}

		// Parse content parts
		if content, ok := candidate["content"].(map[string]interface{}); ok {
			if parts, ok := content["parts"].([]interface{}); ok {
				for _, part := range parts {
					if p, ok := part.(map[string]interface{}); ok {
						// Text part
						if text, ok := p["text"].(string); ok {
							isThinking := isGoogleThinkingPart(p)
							if currentBlock.Type == "" ||
								(isThinking && currentBlock.Type != "thinking") ||
								(!isThinking && currentBlock.Type != "text") {
								finishCurrentGoogleBlock(ch, output, &currentBlock)
								currentBlock.Type = "text"
								if isThinking {
									currentBlock.Type = "thinking"
								}
								currentBlock.Text = ""
								currentBlock.Thinking = ""

								if isThinking {
									newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
									output.Content = append(output.Content, newBlock)
									idx := len(output.Content) - 1
									ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}
								} else {
									newBlock := &src.TextContent{Type: "text", Text: ""}
									output.Content = append(output.Content, newBlock)
									idx := len(output.Content) - 1
									ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}
								}
							}

							idx := len(output.Content) - 1
							if currentBlock.Type == "thinking" {
								currentBlock.Thinking += text
								if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
									tc.Thinking = currentBlock.Thinking
									currentBlock.ThinkingSignature = retainGoogleThoughtSignature(currentBlock.ThinkingSignature, getGoogleThoughtSignature(p))
									tc.ThinkingSignature = currentBlock.ThinkingSignature
									ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: &idx, Delta: text, Partial: output}
								}
							} else {
								currentBlock.Text += text
								if tc, ok := output.Content[idx].(*src.TextContent); ok {
									tc.Text = currentBlock.Text
									currentBlock.TextSignature = retainGoogleThoughtSignature(currentBlock.TextSignature, getGoogleThoughtSignature(p))
									tc.TextSignature = currentBlock.TextSignature
									ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &idx, Delta: text, Partial: output}
								}
							}
						}

						// Function call part
						if functionCall, ok := p["functionCall"].(map[string]interface{}); ok {
							finishCurrentGoogleBlock(ch, output, &currentBlock)
							currentBlock.Type = ""

							name := ""
							if n, ok := functionCall["name"].(string); ok {
								name = n
							}

							args := map[string]interface{}{}
							if a, ok := functionCall["args"].(map[string]interface{}); ok {
								args = a
							}

							// Generate unique ID
							providedId := ""
							if id, ok := functionCall["id"].(string); ok {
								providedId = id
							}
							toolCallId := providedId
							if toolCallId == "" || hasToolCallID(output.Content, toolCallId) {
								toolCallId = fmt.Sprintf("%s_%d", name, toolCallCounter)
								toolCallCounter++
							}

							toolCall := &src.ToolCall{
								Type:      "toolCall",
								ID:        toolCallId,
								Name:      name,
								Arguments: args,
							}

							if sig := getGoogleThoughtSignature(p); sig != "" {
								toolCall.ThoughtSignature = sig
							}

							output.Content = append(output.Content, toolCall)
							idx := len(output.Content) - 1
							argsJson, _ := json.Marshal(args)
							ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &idx, Partial: output}
							ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: &idx, Delta: string(argsJson), Partial: output}
							ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: &idx, ToolCall: toolCall, Partial: output}
						}
					}
				}
			}
		}

		// Parse usage metadata
		if usageMetadata, ok := chunk["usageMetadata"].(map[string]interface{}); ok {
			promptTokens := getFloatValue(usageMetadata, "promptTokenCount")
			candidatesTokens := getFloatValue(usageMetadata, "candidatesTokenCount")
			totalTokens := getFloatValue(usageMetadata, "totalTokenCount")
			cachedTokens := getFloatValue(usageMetadata, "cachedContentTokenCount")
			thoughtsTokens := getFloatValue(usageMetadata, "thoughtsTokenCount")

			output.Usage.Input = int(promptTokens - cachedTokens)
			output.Usage.Output = int(candidatesTokens + thoughtsTokens)
			output.Usage.CacheRead = int(cachedTokens)
			output.Usage.CacheWrite = 0
			output.Usage.TotalTokens = int(totalTokens)
		}
	}

	finishCurrentGoogleBlock(ch, output, &currentBlock)
	return nil
}

func finishCurrentGoogleBlock(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, block *struct {
	Type                string
	Text                string
	Thinking            string
	TextSignature       string
	ThinkingSignature   string
	ID                  string
	Name                string
	Arguments           map[string]interface{}
}) {
	switch block.Type {
	case "text":
		if len(output.Content) > 0 {
			idx := len(output.Content) - 1
			if tc, ok := output.Content[idx].(*src.TextContent); ok {
				ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: &idx, Content: tc.Text, Partial: output}
			}
		}
	case "thinking":
		if len(output.Content) > 0 {
			idx := len(output.Content) - 1
			if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
				ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: &idx, Content: tc.Thinking, Partial: output}
			}
		}
	}

	block.Type = ""
	block.Text = ""
	block.Thinking = ""
	block.TextSignature = ""
	block.ThinkingSignature = ""
}

func isGoogleThinkingPart(part map[string]interface{}) bool {
	if thought, ok := part["thought"].(bool); ok {
		return thought
	}
	return false
}

func getGoogleThoughtSignature(part map[string]interface{}) string {
	if sig, ok := part["thoughtSignature"].(string); ok {
		return sig
	}
	return ""
}

func retainGoogleThoughtSignature(existing, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

func hasToolCallID(content []src.ContentBlock, id string) bool {
	for _, c := range content {
		if tc, ok := c.(*src.ToolCall); ok && tc.ID == id {
			return true
		}
	}
	return false
}

func hasToolCallsInContent(content []src.ContentBlock) bool {
	for _, c := range content {
		if _, ok := c.(*src.ToolCall); ok {
			return true
		}
	}
	return false
}

// =============================================================================
// Model detection helpers
// =============================================================================

var (
	gemma4Pattern      = regexp.MustCompile(`(?i)gemma-?4`)
	gemini3ProPattern  = regexp.MustCompile(`(?i)gemini-3(?:\.\d+)?-pro`)
	gemini3FlashPattern = regexp.MustCompile(`(?i)gemini-3(?:\.\d+)?-flash`)
)

func isGemma4Model(modelId string) bool {
	return gemma4Pattern.MatchString(modelId)
}

func isGemini3ProModel(modelId string) bool {
	return gemini3ProPattern.MatchString(modelId)
}

func isGemini3FlashModel(modelId string) bool {
	return gemini3FlashPattern.MatchString(modelId)
}

func getGoogleThinkingLevel(effort src.ThinkingLevel, modelId string) string {
	clamped := src.High
	if effort == src.Minimal || effort == src.Low {
		clamped = effort
	}

	if isGemini3ProModel(modelId) {
		switch clamped {
		case src.Minimal, src.Low:
			return "LOW"
		case src.Medium, src.High:
			return "HIGH"
		}
	}

	if isGemma4Model(modelId) {
		switch clamped {
		case src.Minimal, src.Low:
			return "MINIMAL"
		case src.Medium, src.High:
			return "HIGH"
		}
	}

	switch clamped {
	case src.Minimal:
		return "MINIMAL"
	case src.Low:
		return "LOW"
	case src.Medium:
		return "MEDIUM"
	case src.High:
		return "HIGH"
	default:
		return "MEDIUM"
	}
}
