package providers

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	src "ycode/packages/llm/src"
)

// Faux provider (port of the reference providers/faux.ts): a registered
// API provider that plays back queued responses as synthetic streams, for
// testing the agent layer without network access.
//
// Parity notes:
//   - emits start / *_start / *_delta / *_end / done|error like the real
//     providers, including block-level start events
//   - honors options.OnResponse (simulated 200 before anything else),
//     options.SessionID + CacheRetention (prompt-cache usage simulation) and
//     options.Context (abort checks at the same five points as the reference)
//   - usage = ceil(runeCount/4) of the serialized context and output (the
//     reference counts UTF-16 units; runes are the closest Go equivalent)
//   - a queued factory function receives (chatCtx, options, state, model)

const (
	fauxDefaultAPI       = "faux"
	fauxDefaultProvider  = "faux"
	fauxDefaultModelID   = "faux-1"
	fauxDefaultModelName = "Faux Model"
	fauxDefaultBaseURL   = "http://localhost:0"
	fauxMinTokenSize     = 3
	fauxMaxTokenSize     = 5
)

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

// RegisterFauxProviderOptions mirrors RegisterFauxProviderOptions in faux.ts.
type RegisterFauxProviderOptions struct {
	// API overrides the generated api id; Provider overrides "faux".
	API      string
	Provider string
	// Models replaces the default faux-1 model definition.
	Models []FauxModelDefinition
	// TokensPerSecond paces chunk emission (>0: delay per chunk proportional
	// to its estimated tokens; otherwise chunks stream as fast as possible).
	TokensPerSecond float64
	// TokenSize bounds the per-chunk size in "tokens" (1 token = 4 chars),
	// clamped exactly like the reference: min at least 1, min <= max.
	TokenSizeMin int
	TokenSizeMax int
}

type FauxProviderRegistration struct {
	Api    string
	Models []src.Model[src.Api]
	State  *FauxState
	// GetModel returns the first model; GetModelByID finds one by id.
	GetModel                func() src.Model[src.Api]
	GetModelByID            func(id string) (src.Model[src.Api], bool)
	SetResponses            func(responses []FauxResponseStep)
	AppendResponses         func(responses []FauxResponseStep)
	GetPendingResponseCount func() int
	Unregister              func()
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
		Usage:        src.Usage{Cost: src.UsageCost{}},
		StopReason:   stopReason,
		ErrorMessage: errorMessage,
		Timestamp:    time.Now().UnixMilli(),
	}
}

// RegisterFauxProvider registers a faux provider with defaults.
func RegisterFauxProvider() *FauxProviderRegistration {
	return RegisterFauxProviderWithOptions(nil)
}

// RegisterFauxProviderWithOptions registers a faux provider, mirroring the
// reference registerFauxProvider(options).
func RegisterFauxProviderWithOptions(options *RegisterFauxProviderOptions) *FauxProviderRegistration {
	api := fmt.Sprintf("%s-%s", fauxDefaultAPI, randomString(6))
	provider := fauxDefaultProvider
	minTokenSize := fauxMinTokenSize
	maxTokenSize := fauxMaxTokenSize
	tokensPerSecond := 0.0

	modelDefs := []FauxModelDefinition{}
	if options != nil {
		if options.API != "" {
			api = options.API
		}
		if options.Provider != "" {
			provider = options.Provider
		}
		if len(options.Models) > 0 {
			modelDefs = options.Models
		}
		tokensPerSecond = options.TokensPerSecond
		// Reference clamp: min >= 1 and min <= max (defaults 3/5).
		optMin, optMax := fauxMinTokenSize, fauxMaxTokenSize
		if options.TokenSizeMin > 0 {
			optMin = options.TokenSizeMin
		}
		if options.TokenSizeMax > 0 {
			optMax = options.TokenSizeMax
		}
		minTokenSize = optMin
		if optMax < minTokenSize {
			minTokenSize = optMax
		}
		if minTokenSize < 1 {
			minTokenSize = 1
		}
		maxTokenSize = optMax
		if maxTokenSize < minTokenSize {
			maxTokenSize = minTokenSize
		}
	}
	if len(modelDefs) == 0 {
		modelDefs = []FauxModelDefinition{{ID: fauxDefaultModelID, Name: fauxDefaultModelName}}
	}
	models := make([]src.Model[src.Api], 0, len(modelDefs))
	for _, def := range modelDefs {
		name := def.Name
		if name == "" {
			name = def.ID
		}
		input := def.Input
		if len(input) == 0 {
			input = []src.InputType{src.InputText, src.InputImage}
		}
		contextWindow := def.ContextWindow
		if contextWindow == 0 {
			contextWindow = 128000
		}
		maxTokens := def.MaxTokens
		if maxTokens == 0 {
			maxTokens = 16384
		}
		models = append(models, src.Model[src.Api]{
			ID: def.ID, Name: name, Api: src.Api(api),
			Provider: src.Provider(provider), BaseURL: fauxDefaultBaseURL,
			Reasoning: def.Reasoning, Input: input, Cost: def.Cost,
			ContextWindow: contextWindow, MaxTokens: maxTokens,
		})
	}

	state := &FauxState{}
	var pendingResponses []FauxResponseStep
	var mu sync.Mutex
	promptCache := map[string]string{}
	sourceID := fmt.Sprintf("faux-provider:%d:%s", time.Now().UnixMilli(), randomString(8))

	withUsageEstimate := func(message *src.AssistantMessage, chatCtx src.ChatContext, options *src.StreamOptions) *src.AssistantMessage {
		promptText := fauxSerializeContext(chatCtx)
		promptTokens := fauxEstimateTokens(promptText)
		outputTokens := fauxEstimateTokens(fauxAssistantContentToText(message.Content))
		input := promptTokens
		cacheRead, cacheWrite := 0, 0

		if options != nil && options.SessionID != "" && options.CacheRetention != src.CacheRetentionNone {
			mu.Lock()
			previousPrompt := promptCache[options.SessionID]
			if previousPrompt != "" {
				cachedChars := fauxCommonPrefixLength(previousPrompt, promptText)
				cacheRead = fauxEstimateTokens(string([]rune(previousPrompt)[:cachedChars]))
				cacheWrite = fauxEstimateTokens(string([]rune(promptText)[cachedChars:]))
				input = promptTokens - cacheRead
				if input < 0 {
					input = 0
				}
			} else {
				cacheWrite = promptTokens
			}
			promptCache[options.SessionID] = promptText
			mu.Unlock()
		}

		message.Usage = src.Usage{
			Input:       input,
			Output:      outputTokens,
			CacheRead:   cacheRead,
			CacheWrite:  cacheWrite,
			TotalTokens: input + outputTokens + cacheRead + cacheWrite,
			Cost:        src.UsageCost{},
		}
		return message
	}

	stream := func(ctx gocontext.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
		outer := make(chan src.AssistantMessageEvent, 256)
		mu.Lock()
		var step FauxResponseStep
		if len(pendingResponses) > 0 {
			step = pendingResponses[0]
			pendingResponses = pendingResponses[1:]
		}
		state.CallCount++
		mu.Unlock()

		// options.Context is the Go equivalent of the TS AbortSignal.
		aborted := func() bool {
			return options != nil && options.Context != nil && options.Context.Err() != nil
		}

		go func() {
			defer close(outer)

			// Simulated 200 before anything else (reference faux.ts).
			if options != nil && options.OnResponse != nil {
				options.OnResponse(&http.Response{StatusCode: 200, Header: http.Header{}})
			}

			if step == nil {
				message := &src.AssistantMessage{
					Role: src.RoleAssistant, Content: []src.ContentBlock{}, Api: src.Api(api),
					Provider: src.Provider(provider), Model: model.ID,
					Usage: src.Usage{Cost: src.UsageCost{}}, StopReason: src.StopReasonError,
					ErrorMessage: "No more faux responses queued",
					Timestamp:    time.Now().UnixMilli(),
				}
				message = withUsageEstimate(message, chatCtx, options)
				outer <- src.AssistantMessageEvent{Type: "error", Reason: src.StopReasonError, Error: message}
				return
			}

			// Step resolution (factory or canned message). A panicking factory
			// becomes an error event, mirroring the reference try/catch.
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						message := &src.AssistantMessage{
							Role: src.RoleAssistant, Content: []src.ContentBlock{}, Api: src.Api(api),
							Provider: src.Provider(provider), Model: model.ID,
							Usage: src.Usage{Cost: src.UsageCost{}}, StopReason: src.StopReasonError,
							ErrorMessage: fmt.Sprintf("%v", rec),
							Timestamp:    time.Now().UnixMilli(),
						}
						outer <- src.AssistantMessageEvent{Type: "error", Reason: src.StopReasonError, Error: message}
					}
				}()

				resolved := step(chatCtx, options, state, model)
				clone := *resolved
				clone.Api = src.Api(api)
				clone.Provider = src.Provider(provider)
				clone.Model = model.ID
				if clone.Timestamp == 0 {
					clone.Timestamp = time.Now().UnixMilli()
				}
				message := withUsageEstimate(&clone, chatCtx, options)
				streamWithDeltas(outer, message, minTokenSize, maxTokenSize, tokensPerSecond, aborted)
			}()
		}()
		return outer
	}

	streamSimple := func(ctx gocontext.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
		// The reference passes the options object straight through, so the
		// embedded StreamOptions (sessionId, cacheRetention, onResponse,
		// context) all survive.
		var streamOpts *src.StreamOptions
		if options != nil {
			streamOpts = &options.StreamOptions
		}
		return stream(ctx, model, chatCtx, streamOpts)
	}

	src.RegisterApiProvider(src.ApiProvider{
		Api:          src.Api(api),
		Stream:       stream,
		StreamSimple: streamSimple,
	}, sourceID)

	return &FauxProviderRegistration{
		Api:    api,
		Models: models,
		State:  state,
		GetModel: func() src.Model[src.Api] {
			return models[0]
		},
		GetModelByID: func(id string) (src.Model[src.Api], bool) {
			for _, m := range models {
				if m.ID == id {
					return m, true
				}
			}
			return src.Model[src.Api]{}, false
		},
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
			src.UnregisterApiProviders(sourceID)
		},
	}
}

// fauxAbortedMessage stamps a partial as aborted (reference
// createAbortedMessage: stopReason "aborted", "Request was aborted").
func fauxAbortedMessage(partial *src.AssistantMessage) *src.AssistantMessage {
	clone := *partial
	clone.StopReason = src.StopReasonAborted
	clone.ErrorMessage = "Request was aborted"
	clone.Timestamp = time.Now().UnixMilli()
	return &clone
}

// streamWithDeltas plays a resolved message back as a synthetic stream with
// the reference event shape (block-level *_start events included) and abort
// checks at the same five points.
func streamWithDeltas(ch chan<- src.AssistantMessageEvent, msg *src.AssistantMessage, minTokenSize, maxTokenSize int, tokensPerSecond float64, aborted func() bool) {
	output := &src.AssistantMessage{
		Role: msg.Role, Content: []src.ContentBlock{}, Api: msg.Api,
		Provider: msg.Provider, Model: msg.Model, Usage: msg.Usage,
		StopReason: msg.StopReason, ErrorMessage: msg.ErrorMessage, Timestamp: msg.Timestamp,
	}
	emitAborted := func() {
		abortedMsg := fauxAbortedMessage(output)
		ch <- src.AssistantMessageEvent{Type: "error", Reason: src.StopReasonAborted, Error: abortedMsg}
	}
	checkAborted := func() bool {
		if aborted != nil && aborted() {
			emitAborted()
			return true
		}
		return false
	}

	if checkAborted() {
		return
	}
	ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

	for i, block := range msg.Content {
		if checkAborted() {
			return
		}
		switch b := block.(type) {
		case *src.ThinkingContent:
			output.Content = append(output.Content, &src.ThinkingContent{Type: "thinking", Thinking: ""})
			ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &i, Partial: output}
			for _, chunk := range splitByTokenSize(b.Thinking, minTokenSize, maxTokenSize) {
				fauxScheduleChunk(chunk, tokensPerSecond)
				if checkAborted() {
					return
				}
				output.Content[i].(*src.ThinkingContent).Thinking += chunk
				ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: &i, Delta: chunk, Partial: output}
			}
			ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: &i, Content: b.Thinking, Partial: output}

		case *src.TextContent:
			output.Content = append(output.Content, &src.TextContent{Type: "text", Text: ""})
			ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &i, Partial: output}
			for _, chunk := range splitByTokenSize(b.Text, minTokenSize, maxTokenSize) {
				fauxScheduleChunk(chunk, tokensPerSecond)
				if checkAborted() {
					return
				}
				output.Content[i].(*src.TextContent).Text += chunk
				ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &i, Delta: chunk, Partial: output}
			}
			ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: &i, Content: b.Text, Partial: output}

		case *src.ToolCall:
			output.Content = append(output.Content, &src.ToolCall{Type: "toolCall", ID: b.ID, Name: b.Name, Arguments: map[string]any{}})
			ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &i, Partial: output}
			for _, chunk := range splitByTokenSize(mustMarshal(b.Arguments), minTokenSize, maxTokenSize) {
				fauxScheduleChunk(chunk, tokensPerSecond)
				if checkAborted() {
					return
				}
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

// fauxScheduleChunk paces emission: >0 tokensPerSecond delays proportional to
// the chunk's estimated tokens; otherwise emit immediately (the reference's
// queueMicrotask).
func fauxScheduleChunk(chunk string, tokensPerSecond float64) {
	if tokensPerSecond <= 0 {
		return
	}
	delayMs := (float64(fauxEstimateTokens(chunk)) / tokensPerSecond) * 1000
	time.Sleep(time.Duration(delayMs) * time.Millisecond)
}

// =============================================================================
// Usage estimation (reference faux.ts serializers)
// =============================================================================

// fauxEstimateTokens mirrors estimateTokens (ceil(length / 4)); the reference
// counts UTF-16 units, Go counts runes.
func fauxEstimateTokens(text string) int {
	n := utf8.RuneCountInString(text)
	return (n + 3) / 4
}

func fauxContentItemToText(block src.ContentBlock) string {
	switch b := block.(type) {
	case *src.TextContent:
		return b.Text
	case *src.ImageContent:
		return fmt.Sprintf("[image:%s:%d]", b.MimeType, len(b.Data))
	}
	return ""
}

func fauxContentToText(content []src.ContentBlock) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		parts = append(parts, fauxContentItemToText(block))
	}
	return strings.Join(parts, "\n")
}

func fauxAssistantContentToText(content []src.ContentBlock) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch b := block.(type) {
		case *src.TextContent:
			parts = append(parts, b.Text)
		case *src.ThinkingContent:
			parts = append(parts, b.Thinking)
		case *src.ToolCall:
			parts = append(parts, fmt.Sprintf("%s:%s", b.Name, mustMarshal(b.Arguments)))
		}
	}
	return strings.Join(parts, "\n")
}

func fauxMessageToText(message src.Message) string {
	switch m := message.(type) {
	case *src.UserMessage:
		return fauxContentToText(m.Content)
	case *src.AssistantMessage:
		return fauxAssistantContentToText(m.Content)
	case *src.ToolResultMessage[any]:
		parts := []string{m.ToolName}
		for _, block := range m.Content {
			parts = append(parts, fauxContentItemToText(block))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// fauxSerializeContext mirrors serializeContext: "system:<prompt>", then
// "<role>:<text>" per message, then "tools:<JSON>", joined by blank lines.
func fauxSerializeContext(context src.ChatContext) string {
	var parts []string
	if context.SystemPrompt != "" {
		parts = append(parts, "system:"+context.SystemPrompt)
	}
	for _, message := range context.Messages {
		parts = append(parts, string(msgRole(message))+":"+fauxMessageToText(message))
	}
	if len(context.Tools) > 0 {
		parts = append(parts, "tools:"+mustMarshal(context.Tools))
	}
	return strings.Join(parts, "\n\n")
}

func fauxCommonPrefixLength(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := len(ra)
	if len(rb) < n {
		n = len(rb)
	}
	i := 0
	for i < n && ra[i] == rb[i] {
		i++
	}
	return i
}

// =============================================================================
// Chunking helpers
// =============================================================================

func splitByTokenSize(text string, minTokenSize, maxTokenSize int) []string {
	if text == "" {
		return []string{""}
	}
	var chunks []string
	i := 0
	for i < len(text) {
		tokenSize := minTokenSize + rand.Intn(maxTokenSize-minTokenSize+1)
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
