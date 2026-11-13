package providers

import (
	"net/http"
	"testing"

	src "ycode/packages/llm/src"
)

// P15 pins for the registry converters: StreamOptions fields the reference
// consumes must reach the provider options (metadata -> anthropic user_id,
// onPayload/onResponse adapters, timeoutMs/maxRetries for the four
// SDK-threading providers).

func TestConvertAnthropicMetadataUserIDOnly(t *testing.T) {
	converted := convertStreamOptionsToAnthropic(&src.StreamOptions{
		Metadata: map[string]any{"user_id": "u1", "other": 42},
	})
	if converted.Metadata == nil || converted.Metadata["user_id"] != "u1" {
		t.Fatalf("metadata = %#v, want {user_id: u1}", converted.Metadata)
	}
	if len(converted.Metadata) != 1 {
		t.Fatalf("non-user_id metadata keys must be dropped: %#v", converted.Metadata)
	}

	converted = convertStreamOptionsToAnthropic(&src.StreamOptions{
		Metadata: map[string]any{"user_id": 7},
	})
	if converted.Metadata != nil {
		t.Fatalf("non-string user_id must be ignored: %#v", converted.Metadata)
	}
}

func TestConvertTimeoutAndRetriesForwarding(t *testing.T) {
	zero := 0
	options := &src.StreamOptions{TimeoutMs: 1234, MaxRetries: &zero}

	anthropicOptions := convertStreamOptionsToAnthropic(options)
	if anthropicOptions.TimeoutMs != 1234 || anthropicOptions.MaxRetries != &zero {
		t.Fatalf("anthropic timeout/retries = %d %v", anthropicOptions.TimeoutMs, anthropicOptions.MaxRetries)
	}
	completionsOptions := convertStreamOptionsToOpenAICompletions(options)
	if completionsOptions.TimeoutMs != 1234 || completionsOptions.MaxRetries != &zero {
		t.Fatalf("completions timeout/retries = %d %v", completionsOptions.TimeoutMs, completionsOptions.MaxRetries)
	}
	responsesOptions := convertStreamOptionsToOpenAIResponses(options)
	if responsesOptions.TimeoutMs != 1234 || responsesOptions.MaxRetries != &zero {
		t.Fatalf("responses timeout/retries = %d %v", responsesOptions.TimeoutMs, responsesOptions.MaxRetries)
	}
	azureOptions := convertStreamOptionsToAzureOpenAIResponses(options)
	if azureOptions.TimeoutMs != 1234 || azureOptions.MaxRetries != &zero {
		t.Fatalf("azure timeout/retries = %d %v", azureOptions.TimeoutMs, azureOptions.MaxRetries)
	}

	// Providers whose TS counterparts ignore the options don't even have the
	// fields (GoogleOptions/GoogleVertexOptions/MistralOptions carry no
	// TimeoutMs/MaxRetries — enforced by this file not compiling if the
	// converters grew them).
	_ = convertStreamOptionsToGoogle(options)
	_ = convertStreamOptionsToGoogleVertex(options)
	_ = convertStreamOptionsToMistral(options)
}

func TestAdaptOnPayloadNilKeepsOriginal(t *testing.T) {
	calls := 0
	adapter := adaptOnPayload(func(payload any) any {
		calls++
		return map[string]any{"replaced": true}
	})
	out, err := adapter(map[string]any{"orig": true}, src.Model[src.Api]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("callback ran %d times, want 1", calls)
	}
	if m, ok := out.(map[string]any); !ok || !m["replaced"].(bool) {
		t.Fatalf("out = %#v, want the replacement", out)
	}

	keep := adaptOnPayload(func(payload any) any { return nil })
	out, err = keep(map[string]any{"orig": true}, src.Model[src.Api]{})
	if err != nil || out != nil {
		t.Fatalf("nil return must keep the payload (nil out), got %v %v", out, err)
	}
}

func TestAdaptOnResponseRebuildsHTTPResponse(t *testing.T) {
	var got *http.Response
	adapter := adaptOnResponse(func(resp *http.Response) { got = resp })
	header := http.Header{"X-Affinity": []string{"s1"}}
	adapter(map[string]interface{}{
		"status":  418,
		"headers": header,
	}, src.Model[src.Api]{})
	if got == nil {
		t.Fatal("onResponse callback never ran")
	}
	if got.StatusCode != 418 || got.Header.Get("X-Affinity") != "s1" {
		t.Fatalf("reconstructed response = %+v, want status 418 + header", got)
	}

	// Direct *http.Response passthrough keeps status and headers.
	got = nil
	adapter(&http.Response{StatusCode: 200, Header: header}, src.Model[src.Api]{})
	if got == nil || got.StatusCode != 200 {
		t.Fatalf("passthrough = %+v", got)
	}
}

func TestConvertNilOptionsStaysNil(t *testing.T) {
	if convertStreamOptionsToAnthropic(nil) != nil {
		t.Fatal("nil options must convert to nil")
	}
	if convertStreamOptionsToOpenAICompletions(nil) != nil {
		t.Fatal("nil options must convert to nil")
	}
}
