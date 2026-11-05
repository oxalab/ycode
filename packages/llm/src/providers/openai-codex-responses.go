package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	src "ycode/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

// Port of openai-codex-responses.ts. The ChatGPT Codex backend lives at
// chatgpt.com/backend-api — NOT api.openai.com — and requires ChatGPT
// OAuth tokens passed as the API key plus codex-specific identity headers.
const (
	defaultCodexBaseURL    = "https://chatgpt.com/backend-api"
	codexJWTClaimPath      = "https://api.openai.com/auth"
	codexMaxRetries        = 3
	openAIBetaResponsesSSE = "responses=experimental"
)

// codexBaseDelayMs is BASE_DELAY_MS from the reference; a var only so tests
// can shrink the backoff.
var codexBaseDelayMs = 1000

var codexToolCallProviders = map[string]bool{
	"openai":       true,
	"openai-codex": true,
	"opencode":     true,
}

// CODEX_RESPONSE_STATUSES: statuses the terminal event may carry; anything
// else is normalized away (normalizeCodexStatus).
var codexResponseStatuses = map[string]bool{
	"completed":   true,
	"incomplete":  true,
	"failed":      true,
	"cancelled":   true,
	"queued":      true,
	"in_progress": true,
}

var codexRetryableTextPattern = regexp.MustCompile(`(?i)rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused`)

var codexUsageLimitPattern = regexp.MustCompile(`(?i)usage_limit_reached|usage_not_included|rate_limit_exceeded`)

// =============================================================================
// Types
// =============================================================================

// OpenAICodexResponsesOptions mirrors OpenAICodexResponsesOptions in
// openai-codex-responses.ts (plus Context/Transport from the Go StreamOptions
// seam). Transport is accepted but ignored: the WebSocket transport is
// deferred (P15-class) and this port always uses the SSE path, which is the
// reference's own functional fallback.
type OpenAICodexResponsesOptions struct {
	APIKey           string
	MaxTokens        *int // accepted but never sent: the codex body carries no token limit (TS parity)
	Temperature      *float64
	ServiceTier      *string
	ReasoningEffort  *string
	ReasoningSummary *string
	TextVerbosity    *string
	Headers          map[string]string
	SessionID        string
	CacheRetention   *src.CacheRetention
	Context          context.Context
	Transport        src.Transport
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamOpenAICodexResponses creates an async streaming function for the
// OpenAI Codex Responses API (ChatGPT backend).
func StreamOpenAICodexResponses(model src.Model[src.Api], context src.ChatContext, options *OpenAICodexResponsesOptions) func() <-chan src.AssistantMessageEvent {
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
				// TS catch block: drop the partialJson scratch, then surface
				// the error (aborted only when the caller actually aborted).
				for _, block := range output.Content {
					if tc, ok := block.(*src.ToolCall); ok {
						tc.PartialArgs = ""
					}
				}
				if ctx := codexContext(options); ctx != nil && ctx.Err() != nil {
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
				handleError(fmt.Errorf("No API key for provider: %s", model.Provider))
				return
			}

			accountId, err := extractCodexAccountId(apiKey)
			if err != nil {
				handleError(err)
				return
			}

			params := buildOpenAICodexResponsesParams(model, context, options)
			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			headers := buildCodexSSEHeaders(model.Headers, optionsHeaders(options), accountId, apiKey, optionsSessionID(options))

			// Fetch with retry logic for rate limits and transient errors.
			client := &http.Client{}
			body, err := json.Marshal(params)
			if err != nil {
				handleError(err)
				return
			}

			var resp *http.Response
			var lastErr error

		attemptLoop:
			for attempt := 0; attempt <= codexMaxRetries; attempt++ {
				if ctx := codexContext(options); ctx != nil && ctx.Err() != nil {
					handleError(fmt.Errorf("Request was aborted"))
					return
				}

				req, err := http.NewRequest("POST", resolveCodexURL(model.BaseURL), bytes.NewReader(body))
				if err != nil {
					handleError(err)
					return
				}
				if ctx := codexContext(options); ctx != nil {
					req = req.WithContext(ctx)
				}
				req.Header = headers

				resp, err = client.Do(req)
				if options != nil && options.OnResponse != nil && resp != nil {
					options.OnResponse(map[string]interface{}{
						"status":  resp.StatusCode,
						"headers": resp.Header,
					}, model)
				}

				if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
					break attemptLoop
				}

				if err != nil {
					lastErr = err
				} else {
					errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
					resp.Body.Close()
					if readErr != nil {
						lastErr = readErr
					} else {
						errorText := string(errorBody)
						if attempt < codexMaxRetries && isCodexRetryableError(resp.StatusCode, errorText) {
							if !codexSleep(codexBaseDelayMs*(1<<attempt), options) {
								handleError(fmt.Errorf("Request was aborted"))
								return
							}
							resp = nil
							continue
						}
						message, _ := parseCodexErrorResponse(resp.StatusCode, errorText)
						handleError(fmt.Errorf("%s", message))
						return
					}
				}

				// Network errors are retryable (unless they carry a usage
				// limit verdict, which is final).
				if attempt < codexMaxRetries && !strings.Contains(lastErr.Error(), "usage limit") {
					if !codexSleep(codexBaseDelayMs*(1<<attempt), options) {
						handleError(fmt.Errorf("Request was aborted"))
						return
					}
					resp = nil
					continue
				}
				handleError(lastErr)
				return
			}

			if resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
				if lastErr == nil {
					lastErr = fmt.Errorf("Failed after retries")
				}
				handleError(lastErr)
				return
			}
			if resp.Body == nil {
				handleError(fmt.Errorf("No response body"))
				return
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// Codex SSE layer: parse + codex-specific event mapping, then
			// feed the shared responses processor through a pipe.
			if err := consumeCodexStream(resp.Body, ch, output, model, options); err != nil {
				handleError(err)
				return
			}

			if ctx := codexContext(options); ctx != nil && ctx.Err() != nil {
				handleError(fmt.Errorf("Request was aborted"))
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleError(fmt.Errorf("stream ended with error: %s", output.StopReason))
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// =============================================================================
// Request/URL handling
// =============================================================================

// resolveCodexURL normalizes a base URL onto the codex responses endpoint
// (openai-codex-responses.ts resolveCodexUrl).
func resolveCodexURL(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultCodexBaseURL
	}
	normalized := strings.TrimRight(raw, "/")
	if strings.HasSuffix(normalized, "/codex/responses") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/codex") {
		return normalized + "/responses"
	}
	return normalized + "/codex/responses"
}

// isCodexRetryableError mirrors isRetryableError: 429/5xx-status or
// rate-limit-flavored error text.
func isCodexRetryableError(status int, errorText string) bool {
	if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
		return true
	}
	return codexRetryableTextPattern.MatchString(errorText)
}

// codexSleep waits for the exponential backoff delay; returns false when the
// caller aborted while waiting.
func codexSleep(ms int, options *OpenAICodexResponsesOptions) bool {
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	if ctx := codexContext(options); ctx != nil {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	<-timer.C
	return true
}

// parseCodexErrorResponse mirrors parseErrorResponse: raw text fallback chain,
// then the error object's message, plus the friendly ChatGPT usage-limit
// wording when the code (or a 429 status) says so.
func parseCodexErrorResponse(status int, raw string) (message string, friendlyMessage string) {
	trimmed := strings.TrimSpace(raw)
	message = trimmed
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "Request failed"
	}

	var parsed struct {
		Error struct {
			Code     string  `json:"code"`
			Type     string  `json:"type"`
			Message  string  `json:"message"`
			PlanType string  `json:"plan_type"`
			ResetsAt float64 `json:"resets_at"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		errObj := parsed.Error
		code := errObj.Code
		if code == "" {
			code = errObj.Type
		}
		if errObj.Message != "" || code != "" {
			if codexUsageLimitPattern.MatchString(code) || status == 429 {
				plan := ""
				if errObj.PlanType != "" {
					plan = fmt.Sprintf(" (%s plan)", strings.ToLower(errObj.PlanType))
				}
				when := ""
				if errObj.ResetsAt > 0 {
					mins := int(math.Round((errObj.ResetsAt*1000 - float64(time.Now().UnixMilli())) / 60000))
					if mins < 0 {
						mins = 0
					}
					when = fmt.Sprintf(" Try again in ~%d min.", mins)
				}
				friendlyMessage = strings.TrimSpace(fmt.Sprintf("You have hit your ChatGPT usage limit%s.%s", plan, when))
			}
			if errObj.Message != "" {
				message = errObj.Message
			} else if friendlyMessage != "" {
				message = friendlyMessage
			}
		}
	}
	return message, friendlyMessage
}

// =============================================================================
// Auth & headers
// =============================================================================

// extractCodexAccountId pulls chatgpt_account_id out of the ChatGPT OAuth
// JWT (openai-codex-responses.ts extractAccountId). All failures collapse to
// the reference's single error string.
func extractCodexAccountId(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("Failed to extract accountId from token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("Failed to extract accountId from token")
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return "", fmt.Errorf("Failed to extract accountId from token")
	}
	auth, ok := fields[codexJWTClaimPath].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("Failed to extract accountId from token")
	}
	if accountID, ok := auth["chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID, nil
	}
	return "", fmt.Errorf("Failed to extract accountId from token")
}

// buildCodexSSEHeaders mirrors buildSSEHeaders over buildBaseCodexHeaders:
// model headers, options override, auth/identity, then the SSE-specific set.
func buildCodexSSEHeaders(modelHeaders, additionalHeaders map[string]string, accountId, token, sessionId string) http.Header {
	headers := http.Header{}
	for k, v := range modelHeaders {
		headers.Set(k, v)
	}
	for k, v := range additionalHeaders {
		headers.Set(k, v)
	}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("chatgpt-account-id", accountId)
	headers.Set("originator", "pi")
	headers.Set("User-Agent", codexUserAgent())
	headers.Set("OpenAI-Beta", openAIBetaResponsesSSE)
	headers.Set("accept", "text/event-stream")
	headers.Set("content-type", "application/json")
	if sessionId != "" {
		headers.Set("session_id", sessionId)
		headers.Set("x-client-request-id", sessionId)
	}
	return headers
}

func optionsHeaders(options *OpenAICodexResponsesOptions) map[string]string {
	if options != nil {
		return options.Headers
	}
	return nil
}

func optionsSessionID(options *OpenAICodexResponsesOptions) string {
	if options != nil {
		return options.SessionID
	}
	return ""
}

func codexContext(options *OpenAICodexResponsesOptions) context.Context {
	if options != nil {
		return options.Context
	}
	return nil
}

// =============================================================================
// Parameter building
// =============================================================================

func buildOpenAICodexResponsesParams(model src.Model[src.Api], context src.ChatContext, options *OpenAICodexResponsesOptions) interface{} {
	messages := convertResponsesMessages(model, context, codexToolCallProviders, &ConvertResponsesMessagesOptions{
		IncludeSystemPrompt: false,
	})

	params := map[string]interface{}{
		"model":               model.ID,
		"store":               false,
		"stream":              true,
		"instructions":        context.SystemPrompt,
		"input":               messages,
		"text":                map[string]interface{}{"verbosity": "low"},
		"include":             []string{"reasoning.encrypted_content"},
		"prompt_cache_key":    optionsSessionID(options),
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}
	if context.SystemPrompt == "" {
		params["instructions"] = "You are a helpful assistant."
	}
	if options != nil && options.TextVerbosity != nil {
		params["text"] = map[string]interface{}{"verbosity": *options.TextVerbosity}
	}

	if options != nil && options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	if options != nil && options.ServiceTier != nil {
		params["service_tier"] = *options.ServiceTier
	}

	if len(context.Tools) > 0 {
		tools := convertResponsesTools(context.Tools, &ConvertResponsesToolsOptions{})
		// TS passes { strict: null } — the key is present but null.
		for _, tool := range tools {
			tool["strict"] = nil
		}
		params["tools"] = tools
	}

	// Reasoning is sent only when an effort was requested. TS:
	// effort === "none" ? (thinkingLevelMap?.off ?? "none") : (thinkingLevelMap?.[effort] ?? effort),
	// and the reasoning field is skipped when that resolves to null — a
	// thinkingLevelMap with off:null marks a model that cannot disable
	// thinking (omitting the field leaves it on).
	if options != nil && options.ReasoningEffort != nil {
		effort := *options.ReasoningEffort
		if effort == "none" {
			mapped, has := model.ThinkingLevelMap["off"]
			switch {
			case has && mapped == nil:
				effort = "" // TS null → no reasoning field
			case has:
				effort = string(*mapped)
			default:
				effort = "none"
			}
		} else if mapped, has := model.ThinkingLevelMap[effort]; has && mapped != nil {
			effort = string(*mapped)
		}
		if effort != "" {
			params["reasoning"] = map[string]interface{}{
				"effort":  effort,
				"summary": reasoningSummaryOrDefault(options),
			}
		}
	}

	return params
}

func reasoningSummaryOrDefault(options *OpenAICodexResponsesOptions) string {
	if options != nil && options.ReasoningSummary != nil {
		return *options.ReasoningSummary
	}
	return "auto"
}

// =============================================================================
// Codex SSE layer
// =============================================================================

// consumeCodexStream ports the parseSSE → mapCodexEvents →
// processResponsesStream pipeline: codex-specific SSE parsing and event
// mapping run first, and the transformed events are re-serialized into a
// pipe consumed by the shared responses processor.
func consumeCodexStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], options *OpenAICodexResponsesOptions) error {
	pr, pw := io.Pipe()
	transformErr := make(chan error, 1)
	go func() {
		transformErr <- transformCodexSSE(body, pw, options)
	}()

	processorOptions := &OpenAIResponsesOptions{
		ServiceTier: optionsServiceTier(options),
	}
	processErr := processOpenAIResponsesStream(pr, ch, output, model, processorOptions)
	_ = pr.Close()
	_ = body.Close()
	if err := <-transformErr; err != nil && processErr == nil {
		processErr = err
	}
	return processErr
}

func optionsServiceTier(options *OpenAICodexResponsesOptions) *string {
	if options != nil {
		return options.ServiceTier
	}
	return nil
}

// transformCodexSSE reads codex SSE frames, applies mapCodexEvents, and
// writes the transformed events as SSE frames into pw. A non-nil return is
// the stream's error (in-band error events, protocol errors).
func transformCodexSSE(body io.ReadCloser, pw *io.PipeWriter, options *OpenAICodexResponsesOptions) error {
	defer pw.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var dataLines []string
	flush := func() (bool, error) {
		defer func() { dataLines = dataLines[:0] }()
		if len(dataLines) == 0 {
			return false, nil
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		if data == "" || data == "[DONE]" {
			return false, nil
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return true, fmt.Errorf("Invalid Codex SSE JSON: %v", err)
		}
		return mapCodexEvent(event, pw, options)
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
			continue
		}
		if line == "" {
			done, err := flush()
			if err != nil || done {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err := flush()
	return err
}

// mapCodexEvent ports mapCodexEvents for a single event. Returns done=true
// after the terminal event (the reference generator stops there).
func mapCodexEvent(event map[string]interface{}, pw *io.PipeWriter, options *OpenAICodexResponsesOptions) (bool, error) {
	eventType, _ := event["type"].(string)
	if eventType == "" {
		return false, nil
	}

	if eventType == "error" {
		code, _ := event["code"].(string)
		message, _ := event["message"].(string)
		fallback := code
		if fallback == "" {
			if raw, err := json.Marshal(event); err == nil {
				fallback = string(raw)
			}
		}
		if message == "" {
			message = fallback
		}
		return false, fmt.Errorf("Codex error: %s", message)
	}

	if eventType == "response.failed" {
		message := "Codex response failed"
		if response, ok := event["response"].(map[string]interface{}); ok {
			if errObj, ok := response["error"].(map[string]interface{}); ok {
				if m, ok := errObj["message"].(string); ok && m != "" {
					message = m
				}
			}
		}
		return false, fmt.Errorf("%s", message)
	}

	if eventType == "response.done" || eventType == "response.completed" || eventType == "response.incomplete" {
		event["type"] = "response.completed"
		if response, ok := event["response"].(map[string]interface{}); ok {
			// normalizeCodexStatus: unknown statuses are dropped.
			if status, ok := response["status"].(string); ok {
				if !codexResponseStatuses[status] {
					delete(response, "status")
				}
			} else {
				delete(response, "status")
			}
			// resolveCodexServiceTier: a "default" response tier does not
			// override a requested flex/priority tier; the shared processor
			// then prices from the resolved tier.
			responseTier, _ := response["service_tier"].(string)
			requestTier := ""
			if options != nil && options.ServiceTier != nil {
				requestTier = *options.ServiceTier
			}
			resolved := resolveCodexServiceTier(responseTier, requestTier)
			if resolved == "" {
				delete(response, "service_tier")
			} else {
				response["service_tier"] = resolved
			}
		}
		if err := writeCodexEvent(pw, event); err != nil {
			return true, err
		}
		return true, nil
	}

	if err := writeCodexEvent(pw, event); err != nil {
		return false, err
	}
	return false, nil
}

// resolveCodexServiceTier ports resolveCodexServiceTier.
func resolveCodexServiceTier(responseTier, requestTier string) string {
	if responseTier == "default" && (requestTier == "flex" || requestTier == "priority") {
		return requestTier
	}
	if responseTier != "" {
		return responseTier
	}
	return requestTier
}

func writeCodexEvent(pw *io.PipeWriter, event map[string]interface{}) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = pw.Write(append(append([]byte("data: "), encoded...), '\n', '\n'))
	return err
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleOpenAICodexResponses provides a simplified streaming interface
// for the OpenAI Codex Responses API.
func StreamSimpleOpenAICodexResponses(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	opts := buildOpenAICodexResponsesSimpleOptions(model, options)
	return StreamOpenAICodexResponses(model, context, opts)
}

func buildOpenAICodexResponsesSimpleOptions(model src.Model[src.Api], options *src.SimpleStreamOptions) *OpenAICodexResponsesOptions {
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

	return &OpenAICodexResponsesOptions{
		APIKey:          base.APIKey,
		MaxTokens:       base.MaxTokens,
		Temperature:     base.Temperature,
		Headers:         base.Headers,
		SessionID:       base.SessionID,
		CacheRetention:  &base.CacheRetention,
		Context:         base.Context,
		Transport:       base.Transport,
		ReasoningEffort: reasoningEffort,
	}
}
