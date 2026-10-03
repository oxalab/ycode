package providers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	src "ycode/packages/llm/src"
)

// The Go openai-completions provider used to set an overall
// http.Client.Timeout (60s). http.Client.Timeout spans the entire response
// body read, so any stream longer than the budget died mid-body with
// "context deadline exceeded (Client.Timeout or context cancellation while
// reading body)". The TS reference sets no timeout at all — hang protection
// is the user's escape-to-interrupt. These tests pin that parity.

// TestStreamOpenAICompletionsSpacedChunksComplete proves a stream whose body
// trickles in over time is delivered whole (no client-level deadline cutting
// the body read).
func TestStreamOpenAICompletionsSpacedChunksComplete(t *testing.T) {
	const chunks = 4
	const chunkGap = 250 * time.Millisecond // total ~1s of body streaming
	const want = "chunkachunkbchunkcchunkd"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"choices\":[{\"delta\":{\"content\":\"chunk" +
				string(rune('a'+i)) + "\"}}]}\n\n"))
			flusher.Flush()
			time.Sleep(chunkGap)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID:       "test-model",
		Name:     "Test Model",
		Api:      src.Api("openai-completions"),
		Provider: src.Provider("test-provider"),
		BaseURL:  server.URL,
	}

	stream := StreamOpenAICompletions(model, src.ChatContext{Messages: []src.Message{}},
		&OpenAICompletionsOptions{APIKey: "test-key"})
	ch := stream()

	var text strings.Builder
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if got := text.String(); got != want {
					t.Fatalf("stream text incomplete: %q, want %q", got, want)
				}
				return
			}
			if ev.Type == "text_delta" {
				text.WriteString(ev.Delta)
			}
		case <-deadline:
			t.Fatalf("stream did not finish in time; got %q", text.String())
		}
	}
}

// TestProvidersHaveNoOverallClientTimeout is a source-level guard: no provider
// may put an overall Timeout on its streaming http.Client (it spans the body
// read and kills long streams — the reference sets none).
func TestProvidersHaveNoOverallClientTimeout(t *testing.T) {
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range matches {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "http.Client{Timeout") {
			t.Errorf("%s sets an overall http.Client timeout — streaming providers must not (TS parity)", f)
		}
	}
}
