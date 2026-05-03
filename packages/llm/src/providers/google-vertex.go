package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"pigo.dev/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

const (
	googleVertexBaseURL = "https:// generativelanguage.googleapis.com/v1beta/projects/%s/locations/%s"
)

// =============================================================================
// Types
// =============================================================================

type GoogleVertexOptions struct {
	AccessToken string
	ProjectID   string
	Location    string
	APIKey      string
	MaxTokens   *int
	Temperature *float64
	ToolChoice  *string
	Thinking    *GoogleThinkingConfig
	Headers     map[string]string
	// Callbacks
	OnPayload  func(params interface{}, model llm.Model[llm.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model llm.Model[llm.Api])
}


// =============================================================================
// Streaming
// =============================================================================

// StreamGoogleVertex creates an async streaming function for Google Vertex AI
func StreamGoogleVertex(model llm.Model[llm.Api], context llm.ChatContext, options *GoogleVertexOptions) func() <-chan llm.AssistantMessageEvent {
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
			params := buildGoogleVertexParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doGoogleVertexRequest(client, model, params, options)
			if err != nil {
				handleGoogleVertexError(ch, output, err, false)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status": resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			ch <- llm.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response - reuse Google stream processor
			if err := processGoogleStream(resp.Body, ch, output, model); err != nil {
				handleGoogleVertexError(ch, output, err, true)
				return
			}

			if output.StopReason == llm.StopReasonError || output.StopReason == llm.StopReasonAborted {
				handleGoogleVertexError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
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

func doGoogleVertexRequest(client *http.Client, model llm.Model[llm.Api], params interface{}, options *GoogleVertexOptions) (*http.Response, error) {
	projectID := "your-project-id"
	location := "us-central1"

	if options != nil && options.ProjectID != "" {
		projectID = options.ProjectID
	}
	if options != nil && options.Location != "" {
		location = options.Location
	}

	baseURL := fmt.Sprintf(googleVertexBaseURL, projectID, location)
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	// Build URL with API key or access token
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", baseURL, model.ID)

	if options != nil && options.APIKey != "" {
		url += "&key=" + options.APIKey
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Add OAuth token if provided
	if options != nil && options.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+options.AccessToken)
	}

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

func handleGoogleVertexError(ch chan<- llm.AssistantMessageEvent, output *llm.AssistantMessage, err error, isAborted bool) {
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

func buildGoogleVertexParams(model llm.Model[llm.Api], context llm.ChatContext, options *GoogleVertexOptions) interface{} {
	// Vertex AI uses the same format as Google Generative AI
	return buildGoogleParams(model, context, &GoogleOptions{
		APIKey:      "",
		MaxTokens:   options.MaxTokens,
		Temperature: options.Temperature,
		ToolChoice:  options.ToolChoice,
		Thinking:    options.Thinking,
		Headers:     options.Headers,
		OnPayload:   options.OnPayload,
		OnResponse:  options.OnResponse,
	})
}

func getVertexThinkingLevel(effort llm.ThinkingLevel, modelId string) string {
	return getGoogleThinkingLevel(effort, modelId)
}

func getDisabledVertexThinkingConfig(model llm.Model[llm.Api]) map[string]interface{} {
	return getDisabledGoogleThinkingConfig(model)
}

func isVertexThinkingPart(part map[string]interface{}) bool {
	return isGoogleThinkingPart(part)
}

func getVertexThoughtSignature(part map[string]interface{}) string {
	return getGoogleThoughtSignature(part)
}

func hasVertexToolCallID(content []llm.ContentBlock, id string) bool {
	for _, c := range content {
		if tc, ok := c.(*llm.ToolCall); ok && tc.ID == id {
			return true
		}
	}
	return false
}

func hasVertexToolCallsInContent(content []llm.ContentBlock) bool {
	return hasToolCallsInContent(content)
}

func finishCurrentVertexBlock(ch chan<- llm.AssistantMessageEvent, output *llm.AssistantMessage, block *struct {
	Type                string
	Text                string
	Thinking            string
	TextSignature       string
	ThinkingSignature   string
	ID                  string
	Name                string
	Arguments           map[string]interface{}
}) {
	finishCurrentGoogleBlock(ch, output, block)
}
