package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	src "pigo.dev/packages/llm/src"
	"pigo.dev/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	geminiCLIDefaultEndpoint           = "https://cloudcode-pa.googleapis.com"
	antigravityDailyEndpoint           = "https://daily-cloudcode-pa.sandbox.googleapis.com"
	antigravityAutopushEndpoint        = "https://autopush-cloudcode-pa.sandbox.googleapis.com"
	antigravitySystemInstruction       = "You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding."
	maxRetries                         = 3
	baseDelayMs                        = 1000
	maxEmptyStreamRetries              = 2
	emptyStreamBaseDelayMs             = 500
)

// =============================================================================
// Types
// =============================================================================

type GoogleGeminiCliOptions struct {
	APIKey      string
	MaxTokens   *int
	Temperature *float64
	ToolChoice  *string
	Thinking    *GoogleCliThinkingConfig
	SessionID   string
	ProjectID   string
	Headers     map[string]string
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

type GoogleCliThinkingConfig struct {
	Enabled      bool
	BudgetTokens *int
	Level        *string // "MINIMAL" | "LOW" | "MEDIUM" | "HIGH"
}

type CloudCodeAssistRequest struct {
	Project    string                 `json:"project"`
	Model      string                 `json:"model"`
	Request    CloudCodeAssistParams  `json:"request"`
	RequestType string                `json:"requestType,omitempty"`
	UserAgent   string                 `json:"userAgent,omitempty"`
	RequestID   string                `json:"requestId,omitempty"`
}

type CloudCodeAssistParams struct {
	Contents           []map[string]interface{} `json:"contents"`
	SessionID          string                 `json:"sessionId,omitempty"`
	SystemInstruction  map[string]interface{} `json:"systemInstruction,omitempty"`
	GenerationConfig   map[string]interface{} `json:"generationConfig,omitempty"`
	Tools              []map[string]interface{} `json:"tools,omitempty"`
	ToolConfig         map[string]interface{} `json:"toolConfig,omitempty"`
}

type CloudCodeAssistResponseChunk struct {
	Response *struct {
		Candidates      []map[string]interface{} `json:"candidates,omitempty"`
		UsageMetadata   map[string]interface{}   `json:"usageMetadata,omitempty"`
		ModelVersion    string                   `json:"modelVersion,omitempty"`
		ResponseID      string                   `json:"responseId,omitempty"`
	} `json:"response,omitempty"`
	TraceID string `json:"traceId,omitempty"`
}

// =============================================================================
// Provider registration
// =============================================================================

// StreamGoogleGeminiCli creates an async streaming function for Google Gemini CLI
func StreamGoogleGeminiCli(model src.Model[src.Api], context src.ChatContext, options *GoogleGeminiCliOptions) func() <-chan src.AssistantMessageEvent {
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

			// Parse OAuth credentials
			if options == nil || options.APIKey == "" {
				handleGeminiCliError(ch, output, fmt.Errorf("Google Cloud Code Assist requires OAuth authentication"), false)
				return
			}

			accessToken, projectId, err := parseGeminiCliCredentials(options.APIKey)
			if err != nil {
				handleGeminiCliError(ch, output, err, false)
				return
			}

			isAntigravity := model.Provider == "google-antigravity"
			baseURL := model.BaseURL
			if baseURL == "" {
				baseURL = strings.TrimSpace(baseURL)
			}

			var endpoints []string
			if baseURL != "" {
				endpoints = []string{baseURL}
			} else if isAntigravity {
				endpoints = []string{antigravityDailyEndpoint, antigravityAutopushEndpoint, geminiCLIDefaultEndpoint}
			} else {
				endpoints = []string{geminiCLIDefaultEndpoint}
			}

			// Build request
			requestBody := buildGeminiCliRequest(model, context, projectId, options, isAntigravity)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(requestBody, model); e == nil && nextParams != nil {
					if req, ok := nextParams.(*CloudCodeAssistRequest); ok {
						requestBody = req
					}
				}
			}

			// Build headers
			headers := buildGeminiCliHeaders(model, options, isAntigravity)

			requestBodyJson, _ := json.Marshal(requestBody)

			// Fetch with retry logic
			var lastError error
			var response *http.Response
			endpointIndex := 0

			for attempt := 0; attempt <= maxRetries; attempt++ {
				endpoint := endpoints[endpointIndex]
				requestURL := fmt.Sprintf("%s/v1internal:streamGenerateContent?alt=sse", endpoint)

				req, err := http.NewRequest("POST", requestURL, bytes.NewReader(requestBodyJson))
				if err != nil {
					lastError = err
					time.Sleep(time.Duration(baseDelayMs*(1<<attempt)) * time.Millisecond)
					continue
				}

				// Set headers
				req.Header.Set("Authorization", "Bearer "+accessToken)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "text/event-stream")
				for k, v := range headers {
					req.Header.Set(k, v)
				}

				// Make request
				client := &http.Client{}
				response, err = client.Do(req)
				if err != nil {
					lastError = err
					if attempt < maxRetries {
						time.Sleep(time.Duration(baseDelayMs*(1<<attempt)) * time.Millisecond)
						continue
					}
					break
				}

				// Check response
				if options != nil && options.OnResponse != nil {
					options.OnResponse(map[string]interface{}{
						"status": response.StatusCode,
						"headers": response.Header,
					}, model)
				}

				if response.StatusCode == 403 || response.StatusCode == 404 {
					if endpointIndex < len(endpoints)-1 {
						endpointIndex++
						response.Body.Close()
						continue
					}
				}

				if response.StatusCode == http.StatusOK {
					break
				}

				// Read error text
				errorBody, _ := io.ReadAll(response.Body)
				response.Body.Close()
				errorText := string(errorBody)

				// Check if retryable
				if attempt < maxRetries && isRetryableGeminiError(response.StatusCode, errorText) {
					// Advance endpoint if possible
					if endpointIndex < len(endpoints)-1 {
						endpointIndex++
					}

					// Extract retry delay or use exponential backoff
					serverDelay := extractGeminiRetryDelay(errorText, response.Header)
					delayMs := baseDelayMs * (1 << attempt)
					if serverDelay > 0 {
						delayMs = serverDelay
					}

					time.Sleep(time.Duration(delayMs) * time.Millisecond)
					continue
				}

				lastError = fmt.Errorf("Cloud Code Assist API error (%d): %s", response.StatusCode, extractGeminiErrorMessage(errorText))
				break
			}

			if response == nil || response.StatusCode != http.StatusOK {
				handleGeminiCliError(ch, output, lastError, false)
				return
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response with empty stream retry
			receivedContent := false
			for emptyAttempt := 0; emptyAttempt <= maxEmptyStreamRetries; emptyAttempt++ {
				if emptyAttempt > 0 {
					time.Sleep(time.Duration(emptyStreamBaseDelayMs*(1<<(emptyAttempt-1))) * time.Millisecond)

					// Retry request
					endpoint := endpoints[0]
					requestURL := fmt.Sprintf("%s/v1internal:streamGenerateContent?alt=sse", endpoint)

					req, _ := http.NewRequest("POST", requestURL, bytes.NewReader(requestBodyJson))
					req.Header.Set("Authorization", "Bearer "+accessToken)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Accept", "text/event-stream")
					for k, v := range headers {
						req.Header.Set(k, v)
					}

					client := &http.Client{}
					response, err = client.Do(req)
					if err != nil || response.StatusCode != http.StatusOK {
						errMsg := "unknown error"
						if err != nil {
							errMsg = err.Error()
						} else if response != nil {
							errorBody, _ := io.ReadAll(response.Body)
							errMsg = string(errorBody)
						}
						handleGeminiCliError(ch, output, fmt.Errorf("Retry failed: %s", errMsg), false)
						return
					}
				}

				hasContent, err := processGeminiCliStream(response.Body, ch, output, model)
				if err != nil {
					handleGeminiCliError(ch, output, err, true)
					return
				}

				if hasContent {
					receivedContent = true
					break
				}

				// Reset output for retry
				output.Content = []src.ContentBlock{}
				output.Usage = src.Usage{Cost: src.UsageCost{}}
				output.StopReason = src.StopReasonStop
			}

			if !receivedContent {
				handleGeminiCliError(ch, output, fmt.Errorf("Cloud Code Assist API returned an empty response"), false)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleGeminiCliError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// =============================================================================
// Request building
// =============================================================================

func buildGeminiCliRequest(model src.Model[src.Api], context src.ChatContext, projectId string, options *GoogleGeminiCliOptions, isAntigravity bool) *CloudCodeAssistRequest {
	contents := convertGoogleMessages(model, context)

	generationConfig := map[string]interface{}{}

	if options != nil && options.Temperature != nil {
		generationConfig["temperature"] = *options.Temperature
	}

	if options != nil && options.MaxTokens != nil {
		generationConfig["maxOutputTokens"] = *options.MaxTokens
	}

	// Thinking config
	if options != nil && options.Thinking != nil && options.Thinking.Enabled && model.Reasoning {
		thinkingConfig := map[string]interface{}{
			"includeThoughts": true,
		}

		if options.Thinking.Level != nil {
			thinkingConfig["thinkingLevel"] = *options.Thinking.Level
		} else if options.Thinking.BudgetTokens != nil {
			thinkingConfig["thinkingBudget"] = *options.Thinking.BudgetTokens
		}

		generationConfig["thinkingConfig"] = thinkingConfig
	} else if model.Reasoning && options != nil && options.Thinking != nil && !options.Thinking.Enabled {
		generationConfig["thinkingConfig"] = getDisabledGeminiCliThinkingConfig(model.ID)
	}

	request := &CloudCodeAssistParams{
		Contents: contents,
	}

	if options != nil && options.SessionID != "" {
		request.SessionID = options.SessionID
	}

	if context.SystemPrompt != "" {
		request.SystemInstruction = map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": utils.SanitizeSurrogates(context.SystemPrompt)},
			},
		}
	}

	if len(generationConfig) > 0 {
		request.GenerationConfig = generationConfig
	}

	if len(context.Tools) > 0 {
		useParameters := strings.HasPrefix(model.ID, "claude-")
		request.Tools = convertGoogleTools(context.Tools, useParameters)

		if options != nil && options.ToolChoice != nil {
			request.ToolConfig = map[string]interface{}{
				"functionCallingConfig": map[string]interface{}{
					"mode": mapGoogleToolChoice(*options.ToolChoice),
				},
			}
		}
	}

	// Add Antigravity system instruction
	if isAntigravity {
		existingParts := []map[string]interface{}{}
		if request.SystemInstruction != nil {
			if parts, ok := request.SystemInstruction["parts"].([]map[string]interface{}); ok {
				existingParts = parts
			}
		}

		request.SystemInstruction = map[string]interface{}{
			"role": "user",
			"parts": []map[string]interface{}{
				{"text": antigravitySystemInstruction},
				{"text": fmt.Sprintf("Please ignore following [ignore]%s[/ignore]", antigravitySystemInstruction)},
			},
		}

		request.SystemInstruction["parts"] = append(request.SystemInstruction["parts"].([]map[string]interface{}), existingParts...)
	}

	userAgent := "pi-coding-agent"
	requestType := ""
	if isAntigravity {
		userAgent = "antigravity"
		requestType = "agent"
	}

	requestID := fmt.Sprintf("%s-%d-%s", requestType, time.Now().Unix(), randomString(9))

	return &CloudCodeAssistRequest{
		Project:    projectId,
		Model:      model.ID,
		Request:    *request,
		RequestType: requestType,
		UserAgent:   userAgent,
		RequestID:   requestID,
	}
}

func buildGeminiCliHeaders(model src.Model[src.Api], options *GoogleGeminiCliOptions, isAntigravity bool) map[string]string {
	headers := make(map[string]string)

	if isAntigravity {
		headers["User-Agent"] = fmt.Sprintf("antigravity/%s darwin/arm64", "1.21.9")
	} else {
		headers["User-Agent"] = "google-cloud-sdk vscode_cloudshelleditor/0.1"
		headers["X-Goog-Api-Client"] = "gl-node/22.17.0"

		clientMetadata := map[string]interface{}{
			"ideType":     "IDE_UNSPECIFIED",
			"platform":    "PLATFORM_UNSPECIFIED",
			"pluginType":  "GEMINI",
		}
		metadataJson, _ := json.Marshal(clientMetadata)
		headers["Client-Metadata"] = string(metadataJson)
	}

	// Add Claude thinking beta header for Antigravity + Claude models
	if isAntigravity && strings.HasPrefix(model.ID, "claude-") && model.Reasoning {
		headers["anthropic-beta"] = "interleaved-thinking-2025-05-14"
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			headers[k] = v
		}
	}

	// Add model headers
	for k, v := range model.Headers {
		headers[k] = v
	}

	return headers
}

// =============================================================================
// Stream processing
// =============================================================================

func processGeminiCliStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api]) (bool, error) {
	defer body.Close()

	var currentBlock struct {
		Type  string
		Text  string
		delta string
	}
	currentBlock.Type = ""

	toolCallCounter := 0
	hasContent := false

	buf := make([]byte, 4096)
	var buffer strings.Builder

	for {
		n, err := io.ReadFull(body, buf)
		if n > 0 {
			buffer.Write(buf[:n])
		}

		if err != nil {
			if err == io.EOF {
				break
			}
			if err == io.ErrUnexpectedEOF {
				break
			}
			return hasContent, err
		}

		// Process lines
		lines := strings.Split(buffer.String(), "\n")
		buffer.Reset()

		// Keep last incomplete line in buffer
		for i, line := range lines {
			if i == len(lines)-1 && !strings.HasSuffix(line, "\n") {
				buffer.WriteString(line)
				continue
			}

			if !strings.HasPrefix(line, "data:") {
				continue
			}

			jsonStr := strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimSpace(jsonStr)
			if jsonStr == "" {
				continue
			}

			var chunk CloudCodeAssistResponseChunk
			if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
				continue
			}

			responseData := chunk.Response
			if responseData == nil {
				continue
			}

			// Set response ID
			if responseData.ResponseID != "" && output.ResponseID == "" {
				output.ResponseID = responseData.ResponseID
			}

			// Process candidates
			if len(responseData.Candidates) > 0 {
				candidate := responseData.Candidates[0]

				// Process parts
				if content, ok := candidate["content"].(map[string]interface{}); ok {
					if parts, ok := content["parts"].([]interface{}); ok {
						for _, part := range parts {
							if p, ok := part.(map[string]interface{}); ok {
								// Text part
								if text, ok := p["text"].(string); ok {
									hasContent = true
									isThinking := isGeminiCliThinkingPart(p)

									if currentBlock.Type == "" ||
										(isThinking && currentBlock.Type != "thinking") ||
										(!isThinking && currentBlock.Type != "text") {

										finishCurrentGeminiCliBlock(ch, output, &currentBlock)

										if isThinking {
											currentBlock.Type = "thinking"
											currentBlock.Text = ""
											newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
											output.Content = append(output.Content, newBlock)
											idx := len(output.Content) - 1
											ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}
										} else {
											currentBlock.Type = "text"
											currentBlock.Text = ""
											newBlock := &src.TextContent{Type: "text", Text: ""}
											output.Content = append(output.Content, newBlock)
											idx := len(output.Content) - 1
											ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}
										}
									}

									idx := len(output.Content) - 1
									if currentBlock.Type == "thinking" {
										currentBlock.Text += text
										if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
											tc.Thinking = currentBlock.Text
											if sig := getGeminiCliThoughtSignature(p); sig != "" {
												tc.ThinkingSignature = retainGeminiCliThoughtSignature(tc.ThinkingSignature, sig)
											}
											ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: &idx, Delta: text, Partial: output}
										}
									} else {
										currentBlock.Text += text
										if tc, ok := output.Content[idx].(*src.TextContent); ok {
											tc.Text = currentBlock.Text
											if sig := getGeminiCliThoughtSignature(p); sig != "" {
												tc.TextSignature = retainGeminiCliThoughtSignature(tc.TextSignature, sig)
											}
											ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &idx, Delta: text, Partial: output}
										}
									}
								}

								// Function call part
								if functionCall, ok := p["functionCall"].(map[string]interface{}); ok {
									hasContent = true
									finishCurrentGeminiCliBlock(ch, output, &currentBlock)
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
									if toolCallId == "" || hasGeminiCliToolCallID(output.Content, toolCallId) {
										toolCallId = fmt.Sprintf("%s_%d", name, toolCallCounter)
										toolCallCounter++
									}

									toolCall := &src.ToolCall{
										Type:      "toolCall",
										ID:        toolCallId,
										Name:      name,
										Arguments: args,
									}

									if sig := getGeminiCliThoughtSignature(p); sig != "" {
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

				// Parse finish reason
				if finishReason, ok := candidate["finishReason"].(string); ok {
					output.StopReason = mapGoogleStopReason(finishReason)
					if hasGeminiCliToolCallsInContent(output.Content) {
						output.StopReason = src.StopReasonToolUse
					}
				}
			}

			// Parse usage metadata
			if responseData.UsageMetadata != nil {
				promptTokens := getFloatValue(responseData.UsageMetadata, "promptTokenCount")
				cacheReadTokens := getFloatValue(responseData.UsageMetadata, "cachedContentTokenCount")
				candidatesTokens := getFloatValue(responseData.UsageMetadata, "candidatesTokenCount")
				thoughtsTokens := getFloatValue(responseData.UsageMetadata, "thoughtsTokenCount")
				totalTokens := getFloatValue(responseData.UsageMetadata, "totalTokenCount")

				output.Usage.Input = int(promptTokens - cacheReadTokens)
				output.Usage.Output = int(candidatesTokens + thoughtsTokens)
				output.Usage.CacheRead = int(cacheReadTokens)
				output.Usage.TotalTokens = int(totalTokens)
			}
		}
	}

	finishCurrentGeminiCliBlock(ch, output, &currentBlock)
	return hasContent, nil
}

func finishCurrentGeminiCliBlock(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, block *struct {
	Type  string
	Text  string
	delta string
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
}

// =============================================================================
// Helpers
// =============================================================================

func parseGeminiCliCredentials(apiKey string) (accessToken, projectId string, err error) {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(apiKey), &parsed); err != nil {
		return "", "", fmt.Errorf("Invalid Google Cloud Code Assist credentials. Use /login to re-authenticate.")
	}

	token, ok := parsed["token"].(string)
	if !ok || token == "" {
		return "", "", fmt.Errorf("Missing token in Google Cloud credentials. Use /login to re-authenticate.")
	}

	pid, ok := parsed["projectId"].(string)
	if !ok || pid == "" {
		return "", "", fmt.Errorf("Missing projectId in Google Cloud credentials. Use /login to re-authenticate.")
	}

	return token, pid, nil
}

func isGeminiCliThinkingPart(part map[string]interface{}) bool {
	if thought, ok := part["thought"].(bool); ok {
		return thought
	}
	return false
}

func getGeminiCliThoughtSignature(part map[string]interface{}) string {
	if sig, ok := part["thoughtSignature"].(string); ok {
		return sig
	}
	return ""
}

func retainGeminiCliThoughtSignature(existing, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

func hasGeminiCliToolCallID(content []src.ContentBlock, id string) bool {
	for _, c := range content {
		if tc, ok := c.(*src.ToolCall); ok && tc.ID == id {
			return true
		}
	}
	return false
}

func hasGeminiCliToolCallsInContent(content []src.ContentBlock) bool {
	for _, c := range content {
		if _, ok := c.(*src.ToolCall); ok {
			return true
		}
	}
	return false
}

func handleGeminiCliError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
	output.StopReason = src.StopReasonError
	if isAborted {
		output.StopReason = src.StopReasonAborted
	}
	output.ErrorMessage = err.Error()
	ch <- src.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
}

func getDisabledGeminiCliThinkingConfig(modelId string) map[string]interface{} {
	// Gemini 3.1 Pro cannot disable thinking
	if isGemini3ProModel(modelId) {
		return map[string]interface{}{
			"thinkingLevel": "LOW",
		}
	}

	// Gemini 3 Flash uses minimal
	if isGemini3FlashModel(modelId) {
		return map[string]interface{}{
			"thinkingLevel": "MINIMAL",
		}
	}

	// Gemini 2.x supports disabling via thinkingBudget = 0
	return map[string]interface{}{
		"thinkingBudget": 0,
	}
}

// Retry delay extraction patterns
var (
	durationPattern    = regexp.MustCompile(`reset after (?:(\d+)h)?(?:(\d+)m)?(\d+(?:\.\d+)?)s`)
	retryInPattern     = regexp.MustCompile(`Please retry in ([0-9.]+)(ms|s)`)
	retryDelayPattern  = regexp.MustCompile(`"retryDelay":\s*"([0-9.]+)(ms|s)"`)
)

func extractGeminiRetryDelay(errorText string, header http.Header) int {
	normalizeDelay := func(ms int) int {
		if ms > 0 {
			return ms/1000 + 1
		}
		return 0
	}

	// Check headers first
	if retryAfter := header.Get("retry-after"); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil {
			return normalizeDelay(seconds * 1000)
		}
	}

	if rateLimitReset := header.Get("x-ratelimit-reset"); rateLimitReset != "" {
		if seconds, err := strconv.Atoi(rateLimitReset); err == nil {
			return normalizeDelay(seconds*1000 - int(time.Now().UnixMilli()))
		}
	}

	// Pattern 1: "reset after XhYmZs"
	if matches := durationPattern.FindStringSubmatch(errorText); matches != nil {
		hours := 0
		minutes := 0
		seconds := 0.0

		if matches[1] != "" {
			if h, err := strconv.Atoi(matches[1]); err == nil {
				hours = h
			}
		}
		if matches[2] != "" {
			if m, err := strconv.Atoi(matches[2]); err == nil {
				minutes = m
			}
		}
		if s, err := strconv.ParseFloat(matches[3], 64); err == nil {
			seconds = s
		}

		totalMs := int((float64(hours*60+minutes)*60 + seconds) * 1000)
		return normalizeDelay(totalMs)
	}

	// Pattern 2: "Please retry in X[ms|s]"
	if matches := retryInPattern.FindStringSubmatch(errorText); matches != nil {
		if value, err := strconv.ParseFloat(matches[1], 64); err == nil {
			ms := int(value)
			if matches[2] == "s" {
				ms = int(value * 1000)
			}
			return normalizeDelay(ms)
		}
	}

	// Pattern 3: "retryDelay": "X[ms|s]"
	if matches := retryDelayPattern.FindStringSubmatch(errorText); matches != nil {
		if value, err := strconv.ParseFloat(matches[1], 64); err == nil {
			ms := int(value)
			if matches[2] == "s" {
				ms = int(value * 1000)
			}
			return normalizeDelay(ms)
		}
	}

	return 0
}

func isRetryableGeminiError(status int, errorText string) bool {
	if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
		return true
	}

	lower := strings.ToLower(errorText)
	return strings.Contains(lower, "resource exhausted") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "overloaded") ||
		strings.Contains(lower, "service unavailable") ||
		strings.Contains(lower, "other side closed")
}

func extractGeminiErrorMessage(errorText string) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(errorText), &parsed); err == nil {
		if errObj, ok := parsed["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["message"].(string); ok {
				return msg
			}
		}
	}
	return errorText
}
