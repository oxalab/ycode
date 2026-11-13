package providers

import (
	gocontext "context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	src "ycode/packages/llm/src"
)

// P15 parity pins for the SDK-style transport shared by the four providers
// whose TS counterparts thread options.timeoutMs / options.maxRetries into
// their SDK clients (openai@6.26.0 client.js defaults: 2 retries, 10-minute
// per-attempt timeout, 408/409/429/>=500 retryable, x-should-retry override,
// retry-after-ms honored).

const sdkTestCompletionsSSE = "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
	"data: [DONE]\n\n"

func collectSDKEvents(t *testing.T, ch <-chan src.AssistantMessageEvent) []src.AssistantMessageEvent {
	t.Helper()
	var events []src.AssistantMessageEvent
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("stream did not finish in time (%d events)", len(events))
		}
	}
}

func sdkTestText(events []src.AssistantMessageEvent) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == "text_delta" {
			sb.WriteString(ev.Delta)
		}
	}
	return sb.String()
}

// =============================================================================
// doSDKStyleRequest unit behavior
// =============================================================================

func TestSDKRequestRetriesRetryableStatus(t *testing.T) {
	var attempts atomic.Int32
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(buf))
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	resp, err := doSDKStyleRequest(sdkRequestOptions{
		Method: http.MethodPost, URL: server.URL,
		Body:   []byte(`{"m":1}`),
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Client: server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2 (one retry after 503)", got)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("body not replayed across attempts: %q vs %q", bodies, bodies)
	}
}

func TestSDKRequestPersistentRetryableExhaustsDefaultRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	resp, err := doSDKStyleRequest(sdkRequestOptions{
		Method: http.MethodPost, URL: server.URL, Client: server.Client(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("status = %d, want 503 (final response returned)", resp.StatusCode)
	}
	resp.Body.Close()
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + SDK default 2 retries)", got)
	}
}

func TestSDKRequestMaxRetriesZero(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	zero := 0
	resp, err := doSDKStyleRequest(sdkRequestOptions{
		Method: http.MethodPost, URL: server.URL,
		Client: server.Client(), MaxRetries: &zero,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (maxRetries=0)", got)
	}
}

func TestSDKRequestXShouldRetryHeaderOverride(t *testing.T) {
	t.Run("false on 503 is not retried", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.Header().Set("x-should-retry", "false")
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		resp, err := doSDKStyleRequest(sdkRequestOptions{Method: http.MethodPost, URL: server.URL, Client: server.Client()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
		if got := attempts.Load(); got != 1 {
			t.Fatalf("attempts = %d, want 1", got)
		}
	})
	t.Run("true on 400 is retried", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if attempts.Add(1) == 1 {
				w.Header().Set("x-should-retry", "true")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte("ok"))
		}))
		defer server.Close()

		resp, err := doSDKStyleRequest(sdkRequestOptions{Method: http.MethodPost, URL: server.URL, Client: server.Client()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := attempts.Load(); got != 2 {
			t.Fatalf("attempts = %d, want 2", got)
		}
	})
}

func TestSDKRequestNonRetryableStatusReturned(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	resp, err := doSDKStyleRequest(sdkRequestOptions{Method: http.MethodPost, URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (400 is not retryable)", got)
	}
}

func TestSDKRequestRetryAfterMsHonored(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("retry-after-ms", "40")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	start := time.Now()
	resp, err := doSDKStyleRequest(sdkRequestOptions{Method: http.MethodPost, URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if elapsed := time.Since(start); elapsed < 35*time.Millisecond {
		t.Fatalf("retry fired too early: %v (retry-after-ms: 40 not honored)", elapsed)
	}
}

func TestSDKRequestAttemptTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	zero := 0
	_, err := doSDKStyleRequest(sdkRequestOptions{
		Method: http.MethodPost, URL: server.URL,
		Client: server.Client(), TimeoutMs: 60, MaxRetries: &zero,
	})
	if !errors.Is(err, errSDKRequestTimeout) {
		t.Fatalf("err = %v, want Request timed out.", err)
	}
}

func TestSDKRequestCallerAbort(t *testing.T) {
	t.Run("before request", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
		}))
		defer server.Close()

		ctx, cancel := gocontext.WithCancel(gocontext.Background())
		cancel()
		_, err := doSDKStyleRequest(sdkRequestOptions{
			Method: http.MethodPost, URL: server.URL, Context: ctx, Client: server.Client(),
		})
		if !errors.Is(err, errSDKRequestAborted) {
			t.Fatalf("err = %v, want Request was aborted", err)
		}
		if got := attempts.Load(); got != 0 {
			t.Fatalf("attempts = %d, want 0 (never reached the server)", got)
		}
	})
	t.Run("during retry sleep", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.Header().Set("retry-after-ms", "5000")
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		ctx, cancel := gocontext.WithCancel(gocontext.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		_, err := doSDKStyleRequest(sdkRequestOptions{
			Method: http.MethodPost, URL: server.URL, Context: ctx, Client: server.Client(),
		})
		if !errors.Is(err, errSDKRequestAborted) {
			t.Fatalf("err = %v, want Request was aborted", err)
		}
	})
}

func TestSDKRetryDelayFormulaBounds(t *testing.T) {
	// calculateDefaultRetryTimeoutMillis: min(0.5 * 2^attempt, 8) seconds with
	// up to 25% downward jitter.
	for i := 0; i < 50; i++ {
		if d := sdkRetryDelaySeconds(nil, 0); d < 0.375 || d > 0.5 {
			t.Fatalf("attempt 0 delay %v outside [0.375, 0.5]", d)
		}
		if d := sdkRetryDelaySeconds(nil, 10); d < 6.0 || d > 8.0 {
			t.Fatalf("attempt 10 delay %v outside [6.0, 8.0] (8s cap with jitter)", d)
		}
	}
}

// =============================================================================
// Provider-level wiring
// =============================================================================

func TestStreamOpenAICompletionsRetriesOn503(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sdkTestCompletionsSSE))
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID: "test-model", Name: "Test Model",
		Api: src.Api("openai-completions"), Provider: src.Provider("test-provider"),
		BaseURL: server.URL,
	}
	stream := StreamOpenAICompletions(model, src.ChatContext{Messages: []src.Message{}},
		&OpenAICompletionsOptions{APIKey: "test-key"})
	events := collectSDKEvents(t, stream())
	if text := sdkTestText(events); text != "hi" {
		t.Fatalf("text = %q, want %q", text, "hi")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Fatalf("terminal event = %s, want done", last.Type)
	}
}

func TestStreamOpenAICompletionsAttemptTimeoutIsErrorNotAbort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID: "test-model", Name: "Test Model",
		Api: src.Api("openai-completions"), Provider: src.Provider("test-provider"),
		BaseURL: server.URL,
	}
	zero := 0
	stream := StreamOpenAICompletions(model, src.ChatContext{Messages: []src.Message{}},
		&OpenAICompletionsOptions{APIKey: "test-key", TimeoutMs: 60, MaxRetries: &zero})
	events := collectSDKEvents(t, stream())
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last := events[len(events)-1]
	if last.Type != "error" || last.Error == nil {
		t.Fatalf("terminal event = %+v, want error", last)
	}
	if last.Error.StopReason != src.StopReasonError {
		t.Fatalf("stopReason = %s, want error (an attempt timeout is not a caller abort)", last.Error.StopReason)
	}
	if last.Error.ErrorMessage != "Request timed out." {
		t.Fatalf("errorMessage = %q, want %q", last.Error.ErrorMessage, "Request timed out.")
	}
}

func TestStreamOpenAICompletionsCallerCancelMidStreamAborts(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		flusher.Flush()
		<-release
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	defer cancel()
	model := src.Model[src.Api]{
		ID: "test-model", Name: "Test Model",
		Api: src.Api("openai-completions"), Provider: src.Provider("test-provider"),
		BaseURL: server.URL,
	}
	stream := StreamOpenAICompletions(model, src.ChatContext{Messages: []src.Message{}},
		&OpenAICompletionsOptions{APIKey: "test-key", Context: ctx})
	ch := stream()

	// Wait for the first delta, then cancel the caller context.
	deadline := time.After(5 * time.Second)
	gotFirst := false
	for !gotFirst {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream closed before abort")
			}
			if ev.Type == "text_delta" {
				gotFirst = true
			}
		case <-deadline:
			t.Fatal("never received the first delta")
		}
	}
	cancel()

	events := collectSDKEvents(t, ch)
	if len(events) == 0 {
		t.Fatal("no terminal event after abort")
	}
	last := events[len(events)-1]
	if last.Type != "error" || last.Error == nil {
		t.Fatalf("terminal event = %+v, want error", last)
	}
	if last.Error.StopReason != src.StopReasonAborted {
		t.Fatalf("stopReason = %s, want aborted", last.Error.StopReason)
	}
	if last.Error.ErrorMessage != "Request was aborted" {
		t.Fatalf("errorMessage = %q, want %q", last.Error.ErrorMessage, "Request was aborted")
	}
}

func TestStreamAnthropicRetriesOn503(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{}}}\n\n" +
			"data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID: "claude-test", Name: "Claude Test",
		Api: src.AnthropicMessages, Provider: src.Provider("anthropic"),
		BaseURL: server.URL,
	}
	stream := StreamAnthropic(model, src.ChatContext{Messages: []src.Message{}},
		&AnthropicOptions{APIKey: "test-key"})
	events := collectSDKEvents(t, stream())
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Fatalf("terminal event = %s (%+v), want done", last.Type, last)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestStreamOpenAIResponsesRetriesOn429(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("retry-after-ms", "10")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"usage\":{}}}\n\n"))
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID: "gpt-test", Name: "GPT Test",
		Api: src.OpenAIResponses, Provider: src.Provider("openai"),
		BaseURL: server.URL,
	}
	stream := StreamOpenAIResponses(model, src.ChatContext{Messages: []src.Message{}},
		&OpenAIResponsesOptions{APIKey: "test-key"})
	events := collectSDKEvents(t, stream())
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Fatalf("terminal event = %s (%+v), want done", last.Type, last)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestStreamAzureOpenAIResponsesRetriesOn500(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{}}}\n\n"))
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID: "gpt-test", Name: "GPT Test",
		Api: src.AzureOpenAIResponses, Provider: src.Provider("azure-openai-responses"),
		BaseURL: server.URL,
	}
	stream := StreamAzureOpenAIResponses(model, src.ChatContext{Messages: []src.Message{}},
		&AzureOpenAIResponsesOptions{APIKey: "test-key"})
	events := collectSDKEvents(t, stream())
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Fatalf("terminal event = %s (%+v), want done", last.Type, last)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestStreamOpenAICompletionsErrorStopReasonRethrowsInnerMessage(t *testing.T) {
	// finish_reason "length" maps to StopReasonLength, not error, so this pin
	// exercises the fallback wording via a server that never finishes the SSE
	// stream cleanly: an unknown finish reason maps to error and the
	// reference rethrows the inner errorMessage.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"a\"},\"finish_reason\":\"content_filter\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID: "test-model", Name: "Test Model",
		Api: src.Api("openai-completions"), Provider: src.Provider("test-provider"),
		BaseURL: server.URL,
	}
	stream := StreamOpenAICompletions(model, src.ChatContext{Messages: []src.Message{}},
		&OpenAICompletionsOptions{APIKey: "test-key"})
	events := collectSDKEvents(t, stream())
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last := events[len(events)-1]
	if last.Type != "error" || last.Error == nil {
		t.Fatalf("terminal event = %+v, want error", last)
	}
	if !strings.Contains(last.Error.ErrorMessage, "content_filter") {
		t.Fatalf("errorMessage = %q, want the inner error stop-reason message rethrown", last.Error.ErrorMessage)
	}
}
