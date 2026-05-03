package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	src "pigo.dev/packages/llm/src"
	"pigo.dev/packages/llm/src/utils"
)

const (
	fauxDefaultAPI         = "faux"
	fauxDefaultProvider    = "faux"
	fauxDefaultModelID     = "faux-1"
	fauxDefaultModelName   = "Faux Model"
	fauxDefaultBaseURL     = "http://localhost:0"
	fauxMinTokenSize       = 3
	fauxMaxTokenSize       = 5
)

var fauxDefaultUsage = src.Usage{
	Cost: src.UsageCost{},
}

type FauxModelDefinition struct {
	ID            string
	Name          string
	Reasoning     bool
	Input         []src.InputType
	Cost          src.ModelCost
	ContextWindow int
	MaxTokens     int
}

type FauxResponseStep func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage

type FauxState struct {
	CallCount int
}

type FauxProviderRegistration struct {
	Api                    string
	Models                 []src.Model[src.Api]
	State                  *FauxState
	SetResponses           func(responses []FauxResponseStep)
	AppendResponses        func(responses []FauxResponseStep)
	GetPendingResponseCount func() int
	Unregister             func()
}

func FauxText(text string) *src.TextContent {
	return &src.TextContent{Type: "text", Text: text}
}

func FauxThinking(thinking string) *src.ThinkingContent {
	return &src.ThinkingContent{Type: "thinking", Thinking: thinking}
}

func FauxToolCall(name string, arguments map[string]any, id string) *src.ToolCall {
	if id == "" {
		id = fmt.Sprintf("tool:%d:%s", time.Now().UnixMilli(), randomString(8))
	}
	return &src.ToolCall{Type: "toolCall", ID: id, Name: name, Arguments: arguments}
}

func FauxAssistantMessage(content []src.ContentBlock, stopReason src.StopReason, errorMessage string) *src.AssistantMessage {
	return &src.AssistantMessage{
		Role:         src.RoleAssistant,
		Content:      content,
		Api:          fauxDefaultAPI,
		Provider:     fauxDefaultProvider,
		Model:        fauxDefaultModelID,
		Usage:        fauxDefaultUsage,
		StopReason:   stopReason,
		ErrorMessage: errorMessage,
		Timestamp:    time.Now().UnixMilli(),
	}
}

func RegisterFauxProvider() *FauxProviderRegistration {
	api := fmt.Sprintf("%s-%s", fauxDefaultAPI, randomString(6))
	provider := fauxDefaultProvider
	models := []src.Model[src.Api]{
		{
			ID: fauxDefaultModelID, Name: fauxDefaultModelName, Api: src.Api(api),
			Provider: src.Provider(provider), BaseURL: fauxDefaultBaseURL,
			Input: []src.InputType{src.InputText, src.InputImage},
			Cost: src.ModelCost{}, ContextWindow: 128000, MaxTokens: 16384,
		},
	}
	state := &FauxState{}
	var pendingResponses []FauxResponseStep
	var mu sync.Mutex

	stream := func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
		outer := make(chan src.AssistantMessageEvent, 256)
		mu.Lock()
		step := pendingResponses
		if len(step) > 0 {
			pendingResponses = step[1:]
			step = step[:1]
		}
		state.CallCount++
		mu.Unlock()

		go func() {
			defer close(outer)
			output := &src.AssistantMessage{
				Role: src.RoleAssistant, Content: nil, Api: src.Api(api),
				Provider: src.Provider(provider), Model: model.ID,
				Usage: src.Usage{Cost: src.UsageCost{}}, StopReason: src.StopReasonStop,
				Timestamp: time.Now().UnixMilli(),
			}

			if len(step) == 0 {
				output.StopReason = src.StopReasonError
				output.ErrorMessage = "No more faux responses queued"
				outer <- src.AssistantMessageEvent{Type: "error", Error: output}
				return
			}

			resolved := step[0](chatCtx, options, state, model)
			clone := *resolved
			clone.Api = src.Api(api)
			clone.Provider = src.Provider(provider)
			clone.Model = model.ID
			clone.Timestamp = time.Now().UnixMilli()

			streamWithDeltas(outer, &clone)
		}()
		return outer
	}

	streamSimple := func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
		// Convert SimpleStreamOptions to StreamOptions
		var streamOpts *src.StreamOptions
		if options != nil {
			streamOpts = &src.StreamOptions{
				APIKey:     options.APIKey,
				MaxTokens:  options.MaxTokens,
				Temperature: options.Temperature,
				Headers:    options.Headers,
			}
		}
		return stream(ctx, model, chatCtx, streamOpts)
	}

	src.RegisterApiProvider(src.ApiProvider{
		Api:          src.Api(api),
		Stream:       stream,
		StreamSimple: streamSimple,
	}, "")

	return &FauxProviderRegistration{
		Api:     api,
		Models:  models,
		State:   state,
		SetResponses: func(responses []FauxResponseStep) {
			mu.Lock()
			defer mu.Unlock()
			pendingResponses = append([]FauxResponseStep{}, responses...)
		},
		AppendResponses: func(responses []FauxResponseStep) {
			mu.Lock()
			defer mu.Unlock()
			pendingResponses = append(pendingResponses, responses...)
		},
		GetPendingResponseCount: func() int {
			mu.Lock()
			defer mu.Unlock()
			return len(pendingResponses)
		},
		Unregister: func() {
			// no-op for now
		},
	}
}

func streamWithDeltas(ch chan<- src.AssistantMessageEvent, msg *src.AssistantMessage) {
	output := &src.AssistantMessage{
		Role: msg.Role, Content: []src.ContentBlock{}, Api: msg.Api,
		Provider: msg.Provider, Model: msg.Model, Usage: msg.Usage,
		StopReason: msg.StopReason, ErrorMessage: msg.ErrorMessage, Timestamp: msg.Timestamp,
	}
	ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

	for i, block := range msg.Content {
		switch b := block.(type) {
		case *src.ThinkingContent:
			output.Content = append(output.Content, &src.ThinkingContent{Type: "thinking", Thinking: ""})
			for _, chunk := range splitByTokenSize(b.Thinking) {
				output.Content[i].(*src.ThinkingContent).Thinking += chunk
				ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: &i, Delta: chunk, Partial: output}
			}
			ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: &i, Content: b.Thinking, Partial: output}

		case *src.TextContent:
			output.Content = append(output.Content, &src.TextContent{Type: "text", Text: ""})
			for _, chunk := range splitByTokenSize(b.Text) {
				output.Content[i].(*src.TextContent).Text += chunk
				ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &i, Delta: chunk, Partial: output}
			}
			ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: &i, Content: b.Text, Partial: output}

		case *src.ToolCall:
			output.Content = append(output.Content, &src.ToolCall{Type: "toolCall", ID: b.ID, Name: b.Name, Arguments: map[string]any{}})
			for _, chunk := range splitByTokenSize(mustMarshal(b.Arguments)) {
				ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: &i, Delta: chunk, Partial: output}
			}
			output.Content[i].(*src.ToolCall).Arguments = b.Arguments
			ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: &i, ToolCall: b, Partial: output}
		}
	}

	if msg.StopReason == src.StopReasonError || msg.StopReason == src.StopReasonAborted {
		ch <- src.AssistantMessageEvent{Type: "error", Reason: msg.StopReason, Error: output}
		return
	}
	ch <- src.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: output}
}

func splitByTokenSize(text string) []string {
	if text == "" {
		return []string{""}
	}
	var chunks []string
	i := 0
	for i < len(text) {
		tokenSize := fauxMinTokenSize + rand.Intn(fauxMaxTokenSize-fauxMinTokenSize+1)
		charSize := tokenSize * 4
		if charSize < 1 {
			charSize = 1
		}
		end := i + charSize
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[i:end])
		i = end
	}
	return chunks
}

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func randomString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(charset[rand.Intn(len(charset))])
	}
	return sb.String()
}

func init() {
	_ = utils.ShortHash // ensure utils import is used
}
