package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

// Port of google.ts (the google-generative-ai provider). The reference
// delegates transport to the @google/genai SDK; this port speaks the same
// REST wire contract directly:
//
//	URL:  {base}/models/{model}:streamGenerateContent?alt=sse  (base carries
//	      the api version, e.g. .../v1beta — tModel prepends models/)
//	Auth: x-goog-api-key header
//	Body: {contents, generationConfig:{temperature,maxOutputTokens,
//	      thinkingConfig}, systemInstruction:{parts:[{text}]}, tools,
//	      toolConfig} — all top-level siblings, no "config" key.
const (
	defaultGoogleBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

var (
	gemini3ProPattern   = regexp.MustCompile(`(?i)gemini-3(?:\.\d+)?-pro`)
	gemini3FlashPattern = regexp.MustCompile(`(?i)gemini-3(?:\.\d+)?-flash`)
	gemma4Pattern       = regexp.MustCompile(`(?i)gemma-?4`)
)

// Package-level counter for unique tool-call IDs (google.ts keeps a global
// too; IDs embed a timestamp segment).
var googleToolCallCounter atomic.Int64

// =============================================================================
// Types
// =============================================================================

type GoogleThinkingConfig struct {
	Enabled      bool
	BudgetTokens *int
	Level        *string
}

// GoogleOptions mirrors GoogleOptions in google.ts plus the Go Context seam.
type GoogleOptions struct {
	APIKey      string
	MaxTokens   *int
	Temperature *float64
	ToolChoice  *string
	Thinking    *GoogleThinkingConfig
	Headers     map[string]string
	Context     context.Context
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamGoogle creates an async streaming function for the Google
// Generative AI (Gemini) API.
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

			handleError := func(err error) {
				if ctx := googleContext(options); ctx != nil && ctx.Err() != nil {
					output.StopReason = src.StopReasonAborted
				} else {
					output.StopReason = src.StopReasonError
				}
				output.ErrorMessage = err.Error()
				ch <- src.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
			}

			params := buildGoogleParams(model, context, options)
			if options != nil && options.OnPayload != nil {
				nextParams, e := options.OnPayload(params, model)
				if e != nil {
					handleError(e)
					return
				}
				if nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doGoogleRequest(model, params, options)
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
				handleError(fmt.Errorf("Google API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(excerpt))))
				return
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// No signal support beyond request-bound Context (P15), so a
			// mid-stream read error is an error, never an abort.
			if err := processGoogleStream(resp.Body, ch, output, model); err != nil {
				handleError(err)
				return
			}
			resp.Body.Close()

			if ctx := googleContext(options); ctx != nil && ctx.Err() != nil {
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

func googleContext(options *GoogleOptions) context.Context {
	if options != nil {
		return options.Context
	}
	return nil
}

// =============================================================================
// Request handling
// =============================================================================

// resolveGoogleBaseURL normalizes the effective base (catalog entries carry
// the api version, e.g. .../v1beta).
func resolveGoogleBaseURL(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultGoogleBaseURL
	}
	return strings.TrimRight(raw, "/")
}

// googleModelPath mirrors the SDK's tModel: ids with an explicit models/ or
// tunedModels/ prefix pass through, anything else gets models/.
func googleModelPath(modelID string) string {
	if strings.HasPrefix(modelID, "models/") || strings.HasPrefix(modelID, "tunedModels/") {
		return modelID
	}
	return "models/" + modelID
}

func doGoogleRequest(model src.Model[src.Api], params interface{}, options *GoogleOptions) (*http.Response, error) {
	body, err := marshalGoogleWireBody(params)
	if err != nil {
		return nil, err
	}

	// {base}/models/{model}:streamGenerateContent?alt=sse
	endpoint := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse",
		resolveGoogleBaseURL(model.BaseURL), googleModelPath(model.ID))

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if ctx := googleContext(options); ctx != nil {
		req = req.WithContext(ctx)
	}

	req.Header.Set("Content-Type", "application/json")

	// SDK parity: the API key travels in the x-goog-api-key header.
	apiKey := ""
	if options != nil && options.APIKey != "" {
		apiKey = options.APIKey
	}
	if apiKey == "" {
		apiKey = getEnvApiKey(string(model.Provider))
	}
	if apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	}

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

// marshalGoogleWireBody strips the "model" key before sending: the SDK moves
// the model into the URL path, and the wire body must not carry it.
func marshalGoogleWireBody(params interface{}) ([]byte, error) {
	if m, ok := params.(map[string]interface{}); ok {
		wire := make(map[string]interface{}, len(m))
		for k, v := range m {
			if k != "model" {
				wire[k] = v
			}
		}
		return json.Marshal(wire)
	}
	return json.Marshal(params)
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

	// Params visible to onPayload mirror the TS pre-SDK shape:
	// {model, contents, generationConfig, systemInstruction?, tools?,
	// toolConfig?}.
	params := map[string]interface{}{
		"model":    model.ID,
		"contents": contents,
	}

	generationConfig := map[string]interface{}{}

	if options != nil && options.Temperature != nil {
		generationConfig["temperature"] = *options.Temperature
	}

	if options != nil && options.MaxTokens != nil {
		generationConfig["maxOutputTokens"] = *options.MaxTokens
	}

	if context.SystemPrompt != "" {
		// SDK tContent parity: a string systemInstruction is wrapped into a
		// Content with a single text part.
		params["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": utils.SanitizeSurrogates(context.SystemPrompt)},
			},
		}
	}

	if len(context.Tools) > 0 {
		params["tools"] = convertGoogleTools(context.Tools, false)
	}

	// toolConfig only applies when tools are present (google.ts gates it).
	if len(context.Tools) > 0 && options != nil && options.ToolChoice != nil {
		params["toolConfig"] = map[string]interface{}{
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
			generationConfig["thinkingConfig"] = thinkingConfig
		} else if options != nil && options.Thinking != nil && !options.Thinking.Enabled {
			generationConfig["thinkingConfig"] = getDisabledGoogleThinkingConfig(model)
		}
	}

	if len(generationConfig) > 0 {
		params["generationConfig"] = generationConfig
	}

	return params
}

func getDisabledGoogleThinkingConfig(model src.Model[src.Api]) map[string]interface{} {
	// Google docs: Gemini 3 Pro cannot disable thinking, and Gemini 3
	// Flash / Gemma 4 do not support full thinking-off either. Use the
	// lowest supported thinkingLevel so hidden thinking stays invisible.
	if isGemini3ProModel(model.ID) {
		return map[string]interface{}{"thinkingLevel": "LOW"}
	}
	if isGemini3FlashModel(model.ID) {
		return map[string]interface{}{"thinkingLevel": "MINIMAL"}
	}
	if isGemma4Model(model.ID) {
		return map[string]interface{}{"thinkingLevel": "MINIMAL"}
	}

	// Gemini 2.x supports disabling via thinkingBudget = 0.
	return map[string]interface{}{"thinkingBudget": 0}
}

func isGemini3ProModel(modelID string) bool {
	return gemini3ProPattern.MatchString(strings.ToLower(modelID))
}

func isGemini3FlashModel(modelID string) bool {
	return gemini3FlashPattern.MatchString(strings.ToLower(modelID))
}

func isGemma4Model(modelID string) bool {
	return gemma4Pattern.MatchString(strings.ToLower(modelID))
}

// mapGoogleToolChoice maps toolChoice strings to Gemini function-calling
// modes (google-shared.ts mapToolChoice).
func mapGoogleToolChoice(toolChoice string) string {
	switch strings.ToLower(toolChoice) {
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

type googleBlockState struct {
	Type              string
	Text              string
	Thinking          string
	TextSignature     string
	ThinkingSignature string
}

func processGoogleStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api]) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var currentBlock googleBlockState

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("Could not parse Google SSE event: %v", err)
		}

		// Set response ID
		if responseID, ok := chunk["responseId"].(string); ok && output.ResponseID == "" {
			output.ResponseID = responseID
		}

		// usageMetadata is processed independently of candidates (a chunk
		// may carry usage without any candidate).
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
			output.Usage.Cost = usageCostFromModelCost(src.CalculateCost(&model, &output.Usage))
		}

		candidates, ok := chunk["candidates"].([]interface{})
		if !ok || len(candidates) == 0 {
			continue
		}

		candidate, ok := candidates[0].(map[string]interface{})
		if !ok {
			continue
		}

		// Content parts are processed BEFORE the finish reason (TS order) so
		// the toolUse override below sees tool calls from this same chunk.
		if content, ok := candidate["content"].(map[string]interface{}); ok {
			if parts, ok := content["parts"].([]interface{}); ok {
				for _, part := range parts {
					p, ok := part.(map[string]interface{})
					if !ok {
						continue
					}
					processGooglePart(p, ch, output, &currentBlock)
				}
			}
		}

		// Parse finish reason
		if finishReason, ok := candidate["finishReason"].(string); ok {
			output.StopReason = mapGoogleStopReason(finishReason)
			if hasToolCallsInContent(output.Content) {
				output.StopReason = src.StopReasonToolUse
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	finishCurrentGoogleBlock(ch, output, &currentBlock)
	return nil
}

func processGooglePart(p map[string]interface{}, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, currentBlock *googleBlockState) {
	// Text part
	if text, ok := p["text"].(string); ok {
		isThinking := isGoogleThinkingPart(p)
		if currentBlock.Type == "" ||
			(isThinking && currentBlock.Type != "thinking") ||
			(!isThinking && currentBlock.Type != "text") {
			finishCurrentGoogleBlock(ch, output, currentBlock)
			*currentBlock = googleBlockState{}
			if isThinking {
				currentBlock.Type = "thinking"
				newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
				output.Content = append(output.Content, newBlock)
				idx := len(output.Content) - 1
				ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}
			} else {
				currentBlock.Type = "text"
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
		finishCurrentGoogleBlock(ch, output, currentBlock)
		*currentBlock = googleBlockState{}

		name := ""
		if n, ok := functionCall["name"].(string); ok {
			name = n
		}

		// args ?? {} — never marshal a nil map.
		args := map[string]interface{}{}
		if a, ok := functionCall["args"].(map[string]interface{}); ok {
			args = a
		}

		// Unique ID when missing or duplicated
		// (google.ts: `${name}_${Date.now()}_${++counter}`).
		providedID := ""
		if id, ok := functionCall["id"].(string); ok {
			providedID = id
		}
		toolCallID := providedID
		if toolCallID == "" || hasToolCallID(output.Content, toolCallID) {
			toolCallID = fmt.Sprintf("%s_%d_%d", name, time.Now().UnixMilli(), googleToolCallCounter.Add(1))
		}

		toolCall := &src.ToolCall{
			Type:      "toolCall",
			ID:        toolCallID,
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

func finishCurrentGoogleBlock(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, block *googleBlockState) {
	if block == nil || block.Type == "" {
		return
	}
	idx := len(output.Content) - 1
	if idx < 0 {
		return
	}
	if block.Type == "text" {
		if tc, ok := output.Content[idx].(*src.TextContent); ok {
			tc.TextSignature = block.TextSignature
			ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: &idx, Content: tc.Text, Partial: output}
		}
	} else if block.Type == "thinking" {
		if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
			tc.ThinkingSignature = block.ThinkingSignature
			ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: &idx, Content: tc.Thinking, Partial: output}
		}
	}
	block.Type = ""
}

func isGoogleThinkingPart(part map[string]interface{}) bool {
	thought, ok := part["thought"].(bool)
	return ok && thought
}

func getGoogleThoughtSignature(part map[string]interface{}) string {
	sig, _ := part["thoughtSignature"].(string)
	return sig
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
// Simple stream wrapper
// =============================================================================

// StreamSimpleGoogle provides a simplified streaming interface for the
// Google Generative AI API (google.ts streamSimpleGoogle).
func StreamSimpleGoogle(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
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

	base := BuildBaseOptions(model, options, "")
	if options == nil || options.Reasoning == "" {
		opts := googleOptionsFromStreamOptions(base)
		opts.APIKey = apiKey
		opts.Thinking = &GoogleThinkingConfig{Enabled: false}
		return StreamGoogle(model, context, opts)
	}

	clamped := src.ClampThinkingLevel(&model, options.Reasoning)
	effort := string(clamped)
	if clamped == src.Off {
		effort = "high"
	}

	modelID := model.ID
	if isGemini3ProModel(modelID) || isGemini3FlashModel(modelID) || isGemma4Model(modelID) {
		level := getGoogleThinkingLevel(effort, modelID)
		opts := googleOptionsFromStreamOptions(base)
		opts.APIKey = apiKey
		opts.Thinking = &GoogleThinkingConfig{Enabled: true, Level: &level}
		return StreamGoogle(model, context, opts)
	}

	var customBudgets *src.ThinkingBudgets
	if options != nil {
		customBudgets = options.ThinkingBudgets
	}
	budget := getGoogleBudget(modelID, effort, customBudgets)
	opts := googleOptionsFromStreamOptions(base)
	opts.APIKey = apiKey
	opts.Thinking = &GoogleThinkingConfig{Enabled: true, BudgetTokens: &budget}
	return StreamGoogle(model, context, opts)
}

func googleOptionsFromStreamOptions(base src.StreamOptions) *GoogleOptions {
	return &GoogleOptions{
		MaxTokens:   base.MaxTokens,
		Temperature: base.Temperature,
		Headers:     base.Headers,
		Context:     base.Context,
	}
}

// getGoogleThinkingLevel ports getThinkingLevel: level-based thinking for
// Gemini 3 Pro/Flash and Gemma 4 models.
func getGoogleThinkingLevel(effort string, modelID string) string {
	if isGemini3ProModel(modelID) {
		switch effort {
		case "minimal", "low":
			return "LOW"
		case "medium", "high":
			return "HIGH"
		}
	}
	if isGemma4Model(modelID) {
		switch effort {
		case "minimal", "low":
			return "MINIMAL"
		case "medium", "high":
			return "HIGH"
		}
	}
	switch effort {
	case "minimal":
		return "MINIMAL"
	case "low":
		return "LOW"
	case "medium":
		return "MEDIUM"
	case "high":
		return "HIGH"
	}
	return "MEDIUM"
}

// getGoogleBudget ports getGoogleBudget: budget-based thinking for Gemini
// 2.5 models, -1 (dynamic) otherwise.
func getGoogleBudget(modelID string, effort string, customBudgets *src.ThinkingBudgets) int {
	if customBudgets != nil {
		switch effort {
		case "minimal":
			if customBudgets.Minimal != nil {
				return *customBudgets.Minimal
			}
		case "low":
			if customBudgets.Low != nil {
				return *customBudgets.Low
			}
		case "medium":
			if customBudgets.Medium != nil {
				return *customBudgets.Medium
			}
		case "high":
			if customBudgets.High != nil {
				return *customBudgets.High
			}
		}
	}

	lower := strings.ToLower(modelID)
	type budgetTable struct {
		minimal, low, medium, high int
	}
	var table budgetTable
	switch {
	case strings.Contains(lower, "2.5-pro"):
		table = budgetTable{minimal: 128, low: 2048, medium: 8192, high: 32768}
	case strings.Contains(lower, "2.5-flash-lite"):
		table = budgetTable{minimal: 512, low: 2048, medium: 8192, high: 24576}
	case strings.Contains(lower, "2.5-flash"):
		table = budgetTable{minimal: 128, low: 2048, medium: 8192, high: 24576}
	default:
		return -1
	}
	switch effort {
	case "minimal":
		return table.minimal
	case "low":
		return table.low
	case "medium":
		return table.medium
	case "high":
		return table.high
	}
	return -1
}
