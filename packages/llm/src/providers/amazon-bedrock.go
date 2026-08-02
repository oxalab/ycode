package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	amazonBedrockBaseURL = "https://bedrock-runtime.%s.amazonaws.com"
)

// =============================================================================
// Types
// =============================================================================

type AmazonBedrockOptions struct {
	AccessKey     string
	SecretKey     string
	SessionToken  string
	Region        string
	MaxTokens     *int
	Temperature   *float64
	TopP          *float64
	TopK          *int
	Headers       map[string]string
	// Callbacks
	OnPayload  func(params interface{}, model llm.Model[llm.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model llm.Model[llm.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamAmazonBedrock creates an async streaming function for Amazon Bedrock
func StreamAmazonBedrock(model llm.Model[llm.Api], context llm.ChatContext, options *AmazonBedrockOptions) func() <-chan llm.AssistantMessageEvent {
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
			params := buildAmazonBedrockParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doAmazonBedrockRequest(client, model, params, options)
			if err != nil {
				handleAmazonBedrockError(ch, output, err, false)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status": resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			ch <- llm.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response
			if err := processAmazonBedrockStream(resp.Body, ch, output, model); err != nil {
				handleAmazonBedrockError(ch, output, err, true)
				return
			}

			if output.StopReason == llm.StopReasonError || output.StopReason == llm.StopReasonAborted {
				handleAmazonBedrockError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
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

func doAmazonBedrockRequest(client *http.Client, model llm.Model[llm.Api], params interface{}, options *AmazonBedrockOptions) (*http.Response, error) {
	region := "us-east-1"
	if options != nil && options.Region != "" {
		region = options.Region
	}

	baseURL := fmt.Sprintf(amazonBedrockBaseURL, region)
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	modelId := strings.Replace(model.ID, ".", "/", -1)
	url := fmt.Sprintf("%s/model/%s/converse-stream", baseURL, modelId)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Add AWS credentials using Signature Version 4
	if options != nil && options.AccessKey != "" {
		awsSignV4(req, options.AccessKey, options.SecretKey, options.SessionToken, region, "bedrock", body)
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

func handleAmazonBedrockError(ch chan<- llm.AssistantMessageEvent, output *llm.AssistantMessage, err error, isAborted bool) {
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

func buildAmazonBedrockParams(model llm.Model[llm.Api], context llm.ChatContext, options *AmazonBedrockOptions) interface{} {
	messages := convertAmazonBedrockMessages(model, context)
	
	inferenceConfig := map[string]interface{}{}

	if options != nil && options.MaxTokens != nil {
		inferenceConfig["maxTokens"] = *options.MaxTokens
	}
	if options != nil && options.Temperature != nil {
		inferenceConfig["temperature"] = *options.Temperature
	}
	if options != nil && options.TopP != nil {
		inferenceConfig["topP"] = *options.TopP
	}
	if options != nil && options.TopK != nil {
		inferenceConfig["topK"] = *options.TopK
	}

	params := map[string]interface{}{
		"messages":           messages,
		"inferenceConfig":    inferenceConfig,
	}

	if context.SystemPrompt != "" {
		params["system"] = context.SystemPrompt
	}

	if len(context.Tools) > 0 {
		params["toolConfig"] = map[string]interface{}{
			"tools": convertAmazonBedrockTools(context.Tools),
		}
	}

	return params
}

func convertAmazonBedrockMessages(model llm.Model[llm.Api], context llm.ChatContext) []map[string]interface{} {
	var messages []map[string]interface{}

	for _, msg := range context.Messages {
		role := msgRole(msg)

		if role == llm.RoleUser {
			um, ok := msg.(*llm.UserMessage)
			if !ok {
				continue
			}

			content := convertAmazonBedrockContent(um.Content)
			messages = append(messages, map[string]interface{}{
				"role":    "user",
				"content": content,
			})
		} else if role == llm.RoleAssistant {
			am := okAssistant(msg)
			if am == nil {
				continue
			}

			var content []map[string]interface{}

			for _, block := range am.Content {
				switch b := block.(type) {
				case *llm.TextContent:
					if b.Text != "" {
						content = append(content, map[string]interface{}{
							"text": utils.SanitizeSurrogates(b.Text),
						})
					}
				case *llm.ToolCall:
					content = append(content, map[string]interface{}{
						"toolUse": map[string]interface{}{
							"name":     b.Name,
							"input":    b.Arguments,
						},
					})
				}
			}

			messages = append(messages, map[string]interface{}{
				"role":    "assistant",
				"content": content,
			})
		} else if role == llm.RoleTool {
			tr, ok := msg.(*llm.ToolResultMessage[any])
			if !ok {
				continue
			}

			var textParts []string
			for _, c := range tr.Content {
				if tc, ok := c.(*llm.TextContent); ok {
					textParts = append(textParts, tc.Text)
				}
			}
			textResult := strings.Join(textParts, "\n")

			messages = append(messages, map[string]interface{}{
				"role":    "user",
				"content": []map[string]interface{}{
					{
						"toolResult": map[string]interface{}{
							"toolUseId": tr.ToolCallID,
							"content":   textResult,
							"status":    "success",
						},
					},
				},
			})
		}
	}

	return messages
}

func convertAmazonBedrockContent(content []llm.ContentBlock) []map[string]interface{} {
	var result []map[string]interface{}

	for _, item := range content {
		switch c := item.(type) {
		case *llm.TextContent:
			result = append(result, map[string]interface{}{
				"text": utils.SanitizeSurrogates(c.Text),
			})
		case *llm.ImageContent:
			result = append(result, map[string]interface{}{
				"image": map[string]interface{}{
					"format": strings.Split(c.MimeType, "/")[1],
					"source": map[string]string{
						"bytes": c.Data,
					},
				},
			})
		}
	}

	return result
}

func convertAmazonBedrockTools(tools []llm.Tool[any]) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))

	for i, tool := range tools {
		result[i] = map[string]interface{}{
			"toolSpec": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			},
		}

		if tool.Parameters != nil {
			result[i]["toolSpec"].(map[string]interface{})["inputSchema"] = tool.Parameters
		}
	}

	return result
}

// =============================================================================
// Stream processing
// =============================================================================

func processAmazonBedrockStream(body io.ReadCloser, ch chan<- llm.AssistantMessageEvent, output *llm.AssistantMessage, model llm.Model[llm.Api]) error {
	defer body.Close()

	decoder := json.NewDecoder(body)

	for decoder.More() {
		var chunk map[string]interface{}
		if err := decoder.Decode(&chunk); err != nil {
			return err
		}

		// Parse response
		if _, ok := chunk["response"].(map[string]interface{}); ok {
			// Handle response
		}

		// Parse delta
		if _, ok := chunk["delta"].(map[string]interface{}); ok {
			// Handle delta
		}

		// Parse usage
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			if inputTokens, ok := usage["promptTokens"].(float64); ok {
				output.Usage.Input = int(inputTokens)
			}
			if outputTokens, ok := usage["completionTokens"].(float64); ok {
				output.Usage.Output = int(outputTokens)
			}
			output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output
		}

		// Parse stop reason
		if stopReason, ok := chunk["stopReason"].(string); ok {
			output.StopReason = mapAmazonBedrockStopReason(stopReason)
		}
	}

	return nil
}

func mapAmazonBedrockStopReason(reason string) llm.StopReason {
	switch reason {
	case "end_turn":
		return llm.StopReasonStop
	case "max_tokens":
		return llm.StopReasonLength
	case "tool_use":
		return llm.StopReasonToolUse
	case "stop_sequence":
		return llm.StopReasonStop
	default:
		return llm.StopReasonStop
	}
}
