package providers

import (
	gocontext "context"
	"net/http"
	"strings"
	"testing"
	"time"

	src "ycode/packages/llm/src"
)

// P15 parity pins for the faux provider (reference providers/faux.ts):
// block-level start events, abort checks, usage estimation with the
// session-keyed prompt-cache simulation, the onResponse probe and the
// options-forwarding streamSimple path.

func fauxCall(reg *FauxProviderRegistration, chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
	provider := src.GetApiProvider(src.Api(reg.Api))
	if provider == nil {
		ch := make(chan src.AssistantMessageEvent, 1)
		ch <- src.AssistantMessageEvent{Type: "error"}
		close(ch)
		return ch
	}
	return provider.Stream(gocontext.Background(), reg.GetModel(), chatCtx, options)
}

func fauxEvents(t *testing.T, ch <-chan src.AssistantMessageEvent) []src.AssistantMessageEvent {
	return collectSDKEvents(t, ch)
}

func TestFauxEventOrderIncludesBlockStartEvents(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(&RegisterFauxProviderOptions{
		Models: []FauxModelDefinition{{ID: "m1", Name: "M1"}},
	})
	defer reg.Unregister()
	reg.SetResponses([]FauxResponseStep{
		func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage {
			return FauxAssistantMessage([]src.ContentBlock{
				FauxThinking("thoughts"),
				FauxText("hello"),
				FauxToolCall("read", map[string]any{"path": "a"}, ""),
			}, src.StopReasonStop, "")
		},
	})

	events := fauxEvents(t, fauxCall(reg, src.ChatContext{Messages: []src.Message{}}, nil))

	firstOfType := map[string]int{}
	for i, ev := range events {
		if _, seen := firstOfType[ev.Type]; !seen {
			firstOfType[ev.Type] = i
		}
	}
	order := []string{
		"start", "thinking_start", "thinking_delta", "thinking_end",
		"text_start", "text_delta", "text_end",
		"toolcall_start", "toolcall_delta", "toolcall_end",
		"done",
	}
	prev := -1
	for _, typ := range order {
		idx, ok := firstOfType[typ]
		if !ok {
			t.Fatalf("missing event %s (got %d events)", typ, len(events))
		}
		if idx <= prev {
			t.Fatalf("event %s out of order (index %d, previous %d)", typ, idx, prev)
		}
		prev = idx
	}
	last := events[len(events)-1]
	if last.Type != "done" || last.Message == nil {
		t.Fatalf("terminal event = %+v, want done", last)
	}
}

func TestFauxUsageEstimateAndPromptCache(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(nil)
	defer reg.Unregister()
	step := func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage {
		return FauxAssistantMessage([]src.ContentBlock{FauxText("abcd")}, src.StopReasonStop, "")
	}
	reg.SetResponses([]FauxResponseStep{step})
	// Serialized prompt = "system:0123456789ABCDEF" = 23 runes = 6 tokens.
	chatCtx := src.ChatContext{SystemPrompt: "0123456789ABCDEF"}

	first := fauxEvents(t, fauxCall(reg, chatCtx, &src.StreamOptions{SessionID: "s1"}))
	usage := first[len(first)-1].Message.Usage
	// First call: full prompt estimated and written to the cache.
	if usage.Input != 6 || usage.Output != 1 || usage.CacheWrite != 6 || usage.CacheRead != 0 {
		t.Fatalf("first usage = %+v, want input 6 output 1 cacheWrite 6 cacheRead 0", usage)
	}
	if usage.TotalTokens != 13 {
		t.Fatalf("totalTokens = %d, want 13", usage.TotalTokens)
	}

	reg.AppendResponses([]FauxResponseStep{step})
	second := fauxEvents(t, fauxCall(reg, chatCtx, &src.StreamOptions{SessionID: "s1"}))
	usage = second[len(second)-1].Message.Usage
	// Second call: the whole prompt is a cached prefix.
	if usage.Input != 0 || usage.CacheRead != 6 || usage.CacheWrite != 0 {
		t.Fatalf("second usage = %+v, want input 0 cacheRead 6 cacheWrite 0", usage)
	}

	reg.AppendResponses([]FauxResponseStep{step})
	// Different session: cache miss again.
	third := fauxEvents(t, fauxCall(reg, chatCtx, &src.StreamOptions{SessionID: "s2"}))
	usage = third[len(third)-1].Message.Usage
	if usage.CacheWrite != 6 || usage.CacheRead != 0 {
		t.Fatalf("new-session usage = %+v, want cacheWrite 6 cacheRead 0", usage)
	}

	reg.AppendResponses([]FauxResponseStep{step})
	// cacheRetention "none" disables the simulation entirely.
	fourth := fauxEvents(t, fauxCall(reg, chatCtx, &src.StreamOptions{SessionID: "s1", CacheRetention: src.CacheRetentionNone}))
	usage = fourth[len(fourth)-1].Message.Usage
	if usage.CacheRead != 0 || usage.CacheWrite != 0 || usage.Input != 6 {
		t.Fatalf("cacheRetention none usage = %+v, want no cache activity", usage)
	}
}

func TestFauxAbortBeforeStart(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(nil)
	defer reg.Unregister()
	reg.SetResponses([]FauxResponseStep{
		func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage {
			return FauxAssistantMessage([]src.ContentBlock{FauxText("hi")}, src.StopReasonStop, "")
		},
	})
	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	cancel()

	events := fauxEvents(t, fauxCall(reg, src.ChatContext{Messages: []src.Message{}},
		&src.StreamOptions{Context: ctx}))
	if len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("events = %+v, want a single error event (no start)", events)
	}
	if events[0].Reason != src.StopReasonAborted {
		t.Fatalf("reason = %v, want aborted", events[0].Reason)
	}
	if events[0].Error.ErrorMessage != "Request was aborted" {
		t.Fatalf("errorMessage = %q, want Request was aborted", events[0].Error.ErrorMessage)
	}
}

func TestFauxAbortMidStreamPreservesPartial(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(&RegisterFauxProviderOptions{
		TokenSizeMin:    1, // 4-char chunks
		TokenSizeMax:    1,
		TokensPerSecond: 1000, // ~1ms per chunk
	})
	defer reg.Unregister()
	long := strings.Repeat("abcdefgh", 60) // 480 chars = 120 chunks
	reg.SetResponses([]FauxResponseStep{
		func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage {
			return FauxAssistantMessage([]src.ContentBlock{FauxText(long)}, src.StopReasonStop, "")
		},
	})

	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	defer cancel()
	ch := fauxCall(reg, src.ChatContext{Messages: []src.Message{}}, &src.StreamOptions{Context: ctx})

	deltas := 0
	var terminal *src.AssistantMessageEvent
	deadline := time.After(10 * time.Second)
	for terminal == nil {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream closed without terminal event")
			}
			if ev.Type == "text_delta" {
				deltas++
				if deltas == 3 {
					cancel()
				}
			}
			if ev.Type == "error" || ev.Type == "done" {
				evCopy := ev
				terminal = &evCopy
			}
		case <-deadline:
			t.Fatal("stream did not terminate")
		}
	}
	if deltas < 3 {
		t.Fatalf("only %d deltas before abort", deltas)
	}
	if terminal.Type != "error" || terminal.Error == nil {
		t.Fatalf("terminal event = %+v, want error", terminal)
	}
	if terminal.Error.StopReason != src.StopReasonAborted || terminal.Error.ErrorMessage != "Request was aborted" {
		t.Fatalf("abort pin diverged: %+v", terminal.Error)
	}
	if len(terminal.Error.Content) == 0 || terminal.Error.Content[0].(*src.TextContent).Text == "" {
		t.Fatal("aborted message must preserve the partial content")
	}
}

func TestFauxOnResponseProbe(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(nil)
	defer reg.Unregister()
	reg.SetResponses([]FauxResponseStep{
		func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage {
			return FauxAssistantMessage([]src.ContentBlock{FauxText("x")}, src.StopReasonStop, "")
		},
	})
	var status int
	options := &src.StreamOptions{OnResponse: func(resp *http.Response) { status = resp.StatusCode }}

	events := fauxEvents(t, fauxCall(reg, src.ChatContext{Messages: []src.Message{}}, options))
	if len(events) == 0 || events[len(events)-1].Type != "done" {
		t.Fatalf("stream did not complete: %+v", events)
	}
	if status != 200 {
		t.Fatalf("onResponse status = %d, want 200", status)
	}
}

func TestFauxEmptyQueueWordingAndUsage(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(nil)
	defer reg.Unregister()

	// "system:0123456789ABCDEF" = 23 runes = 6 estimated tokens.
	events := fauxEvents(t, fauxCall(reg, src.ChatContext{SystemPrompt: "0123456789ABCDEF"}, nil))
	if len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("events = %+v, want one error event", events)
	}
	msg := events[0].Error
	if msg.ErrorMessage != "No more faux responses queued" {
		t.Fatalf("errorMessage = %q, want No more faux responses queued", msg.ErrorMessage)
	}
	// The reference estimates usage even for the empty-queue error.
	if msg.Usage.Input != 6 {
		t.Fatalf("usage.Input = %d, want 6 (estimated from the context)", msg.Usage.Input)
	}
}

func TestFauxStreamSimpleForwardsSessionOptions(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(nil)
	defer reg.Unregister()
	reg.SetResponses([]FauxResponseStep{
		func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage {
			return FauxAssistantMessage([]src.ContentBlock{FauxText("abcd")}, src.StopReasonStop, "")
		},
	})
	provider := src.GetApiProvider(src.Api(reg.Api))
	chatCtx := src.ChatContext{SystemPrompt: "0123456789ABCDEF"}

	step := func(ctx src.ChatContext, options *src.StreamOptions, state *FauxState, model src.Model[src.Api]) *src.AssistantMessage {
		return FauxAssistantMessage([]src.ContentBlock{FauxText("abcd")}, src.StopReasonStop, "")
	}
	reg.SetResponses([]FauxResponseStep{step})
	simple := &src.SimpleStreamOptions{StreamOptions: src.StreamOptions{SessionID: "sx"}}
	first := fauxEvents(t, provider.StreamSimple(gocontext.Background(), reg.GetModel(), chatCtx, simple))
	if len(first) == 0 || first[len(first)-1].Type != "done" {
		t.Fatalf("first simple stream did not complete: %+v", first)
	}
	reg.AppendResponses([]FauxResponseStep{step})
	second := fauxEvents(t, provider.StreamSimple(gocontext.Background(), reg.GetModel(), chatCtx, simple))
	if len(second) == 0 || second[len(second)-1].Type != "done" {
		t.Fatalf("second simple stream did not complete: %+v", second)
	}
	usage := second[len(second)-1].Message.Usage
	// "system:0123456789ABCDEF" = 6 tokens, fully cached on the second call.
	if usage.CacheRead != 6 || usage.Input != 0 {
		t.Fatalf("second simple usage = %+v, want cacheRead 6 (sessionId survived streamSimple)", usage)
	}
}

func TestFauxUnregisterRemovesProvider(t *testing.T) {
	reg := RegisterFauxProviderWithOptions(nil)
	api := src.Api(reg.Api)
	if src.GetApiProvider(api) == nil {
		t.Fatal("provider should be registered")
	}
	reg.Unregister()
	if src.GetApiProvider(api) != nil {
		t.Fatal("provider should be unregistered")
	}
}
