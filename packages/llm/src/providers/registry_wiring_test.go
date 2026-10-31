package providers

import (
	"context"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

// --- Registry mismatch: the reference throws a catchable Error; the Go
// wrapper must surface the same failure as an in-band error event, not a
// process-killing panic.

func registryTestProvider(t *testing.T) (restore func()) {
	t.Helper()
	src.ClearApiProviders()
	src.RegisterApiProvider(src.ApiProvider{
		Api: src.Api("openai-completions"),
		Stream: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.StreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 1)
			ch <- src.AssistantMessageEvent{Type: "done"}
			close(ch)
			return ch
		},
		StreamSimple: func(ctx context.Context, model src.Model[src.Api], chatCtx src.ChatContext, options *src.SimpleStreamOptions) <-chan src.AssistantMessageEvent {
			ch := make(chan src.AssistantMessageEvent, 1)
			ch <- src.AssistantMessageEvent{Type: "done"}
			close(ch)
			return ch
		},
	}, "test")
	return func() { ResetApiProviders() }
}

func drainEvents(ch <-chan src.AssistantMessageEvent) []src.AssistantMessageEvent {
	var out []src.AssistantMessageEvent
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestRegistryMismatchEmitsErrorEventNotPanic(t *testing.T) {
	defer registryTestProvider(t)()

	model := src.Model[src.Api]{ID: "m", Api: src.Api("anthropic")} // wrong api on purpose

	evts := drainEvents(src.GetApiProvider("openai-completions").Stream(context.Background(), model, src.ChatContext{}, nil))
	if len(evts) != 1 || evts[0].Type != "error" || evts[0].Error == nil {
		t.Fatalf("stream events = %+v, want single error event", evts)
	}
	if evts[0].Error.ErrorMessage != "Mismatched api: anthropic expected openai-completions" {
		t.Fatalf("errorMessage = %q", evts[0].Error.ErrorMessage)
	}
	if evts[0].Reason != src.StopReasonError {
		t.Fatalf("reason = %v", evts[0].Reason)
	}

	evts = drainEvents(src.GetApiProvider("openai-completions").StreamSimple(context.Background(), model, src.ChatContext{}, nil))
	if len(evts) != 1 || evts[0].Type != "error" || evts[0].Error == nil {
		t.Fatalf("simple events = %+v, want single error event", evts)
	}

	// Matching model passes through to the real stream.
	ok := src.Model[src.Api]{ID: "m", Api: src.Api("openai-completions")}
	evts = drainEvents(src.GetApiProvider("openai-completions").StreamSimple(context.Background(), ok, src.ChatContext{}, nil))
	if len(evts) != 1 || evts[0].Type != "done" {
		t.Fatalf("matching model events = %+v, want done", evts)
	}
}

// --- Env-key resolver: providers must consult the faithful env-api-keys.ts
// port, not the old divergent rewrite.

func TestGetEnvApiKeyProviderPins(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gem-key")
	if got := getEnvApiKey("google"); got != "gem-key" {
		t.Fatalf("google key = %q, want GEMINI_API_KEY value", got)
	}

	// Anthropic: OAuth token takes precedence over the API key.
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-tok")
	t.Setenv("ANTHROPIC_API_KEY", "plain-key")
	if got := getEnvApiKey("anthropic"); got != "oauth-tok" {
		t.Fatalf("anthropic key = %q, want OAuth token", got)
	}

	// Unknown providers return "" (the reference returns undefined; the old
	// rewrite guessed %s_API_KEY).
	t.Setenv("NO-SUCH-PROVIDER-ZZZ_API_KEY", "guessed")
	if got := getEnvApiKey("no-such-provider-zzz"); got != "" {
		t.Fatalf("unknown provider key = %q, want empty", got)
	}

	// moonshotai / together entries exist (previously missing).
	t.Setenv("MOONSHOT_API_KEY", "mk")
	if got := getEnvApiKey("moonshotai"); got != "mk" {
		t.Fatalf("moonshotai key = %q", got)
	}
	t.Setenv("TOGETHER_API_KEY", "tk")
	if got := getEnvApiKey("together"); got != "tk" {
		t.Fatalf("together key = %q", got)
	}
}

// --- Cloudflare provider matching + URL resolution.

func TestIsCloudflareProvider(t *testing.T) {
	if !IsCloudflareProvider("cloudflare-workers-ai") {
		t.Fatal("workers-ai (TS spelling) must match")
	}
	if !IsCloudflareProvider("cloudflare-ai-gateway") {
		t.Fatal("ai-gateway must match")
	}
	if IsCloudflareProvider("openai") || IsCloudflareProvider("cloudflare-worker-ai") {
		t.Fatal("non-cloudflare / old singular spelling must not match")
	}
}

func TestResolveCloudflareBaseUrl(t *testing.T) {
	m := src.Model[src.Api]{Provider: src.Provider("cloudflare-workers-ai")}

	// No placeholder: passthrough.
	m.BaseURL = "https://example.invalid/v1"
	got, err := ResolveCloudflareBaseUrl(m)
	if err != nil || got != m.BaseURL {
		t.Fatalf("passthrough = %q, %v", got, err)
	}

	// Placeholder resolved from env.
	m.BaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "acc123")
	got, err = ResolveCloudflareBaseUrl(m)
	if err != nil || !strings.Contains(got, "accounts/acc123/") {
		t.Fatalf("resolved = %q, %v", got, err)
	}

	// Missing env: error (the old code panicked).
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	_, err = ResolveCloudflareBaseUrl(m)
	if err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_ACCOUNT_ID is required for provider cloudflare-workers-ai") {
		t.Fatalf("err = %v, want missing-env error", err)
	}
}
