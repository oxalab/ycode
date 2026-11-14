package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func bedrockTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "anthropic.claude-sonnet-4-5-20250929-v1:0", Name: "Claude Sonnet 4.5 (Bedrock)",
		Api: src.Api("bedrock-converse-stream"), Provider: src.Provider("amazon-bedrock"),
		BaseURL:   "https://bedrock-runtime.us-east-1.amazonaws.com",
		Reasoning: true, Input: []src.InputType{src.InputText, src.InputImage},
		Cost:          src.ModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
		ContextWindow: 200000, MaxTokens: 64000,
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

func clearBedrockEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "")
	t.Setenv("AWS_BEDROCK_FORCE_CACHE", "")
	t.Setenv("PI_CACHE_RETENTION", "")
}

func collectBedrockStream(t *testing.T, m src.Model[src.Api], opts *AmazonBedrockOptions) (*src.AssistantMessage, []src.AssistantMessageEvent, string) {
	t.Helper()
	if opts == nil {
		opts = &AmazonBedrockOptions{}
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.AccessKey == "" && opts.BearerToken == "" && os.Getenv("AWS_ACCESS_KEY_ID") == "" && os.Getenv("AWS_BEARER_TOKEN_BEDROCK") == "" {
		opts.AccessKey = "AKID-TEST"
		opts.SecretKey = "secret-test"
	}
	var final *src.AssistantMessage
	var events []src.AssistantMessageEvent
	errMsg := ""
	for ev := range StreamAmazonBedrock(m, src.ChatContext{}, opts)() {
		events = append(events, ev)
		if ev.Type == "error" && ev.Error != nil {
			final = ev.Error
			errMsg = ev.Error.ErrorMessage
		}
		if ev.Type == "done" {
			final = ev.Message
		}
	}
	return final, events, errMsg
}

// --- Eventstream framing: reference test vector + corruption ---

// int32_header vector from @smithy/eventstream-codec TestVectors.fixture.js
// (v4.2.14) — pins the framing layout AND the CRC32 (IEEE) algorithm.
var eventStreamInt32Vector = []byte{
	0, 0, 0, 45, 0, 0, 0, 16, 65, 196, 36, 184, 10, 101, 118, 101, 110, 116, 45, 116,
	121, 112, 101, 4, 0, 0, 160, 12, 123, 39, 102, 111, 111, 39, 58, 39, 98, 97, 114,
	39, 125, 54, 244, 128, 160,
}

func TestAmazonEventStreamReferenceVector(t *testing.T) {
	var gotHeaders map[string]string
	var gotBody []byte
	err := decodeAmazonEventStream(strings.NewReader(string(eventStreamInt32Vector)), func(headers map[string]string, body []byte) error {
		gotHeaders = headers
		gotBody = body
		return nil
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// "event-type" header: int32 40972 (0xa00c).
	if len(gotHeaders) != 0 {
		t.Fatalf("string headers = %v, want none (int32 header is discarded)", gotHeaders)
	}
	if string(gotBody) != "{'foo':'bar'}" {
		t.Fatalf("body = %q, want {'foo':'bar'}", gotBody)
	}
}

func TestAmazonEventStreamInt32Header(t *testing.T) {
	// Build the same frame with the test encoders and verify decode round-trip.
	frame := encodeAmazonEventStreamFrame(
		encodeAmazonEventStreamInt32Header("event-type", 40972),
		[]byte("{'foo':'bar'}"),
	)
	var sawInt32Header bool
	err := decodeAmazonEventStream(strings.NewReader(string(frame)), func(headers map[string]string, body []byte) error {
		sawInt32Header = headers["event-type"] == ""
		if string(body) != "{'foo':'bar'}" {
			t.Errorf("body = %q", body)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !sawInt32Header {
		t.Fatal("frame with int32 header did not decode cleanly")
	}
}

func TestAmazonEventStreamCorruptedCRC(t *testing.T) {
	corrupted := append([]byte{}, eventStreamInt32Vector...)
	corrupted[len(corrupted)-1] ^= 0xFF // flip a message-CRC byte
	err := decodeAmazonEventStream(strings.NewReader(string(corrupted)), func(headers map[string]string, body []byte) error {
		t.Error("frame must not be delivered on CRC mismatch")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
}

func TestAmazonEventStreamTruncated(t *testing.T) {
	err := decodeAmazonEventStream(strings.NewReader(string(eventStreamInt32Vector[:20])), func(headers map[string]string, body []byte) error {
		t.Error("no frame should be delivered")
		return nil
	})
	if err == nil {
		t.Fatal("truncated frame must error")
	}
}

// --- Frame synthesis for stream tests ---

func bedrockFrame(body string) string {
	return string(encodeAmazonEventStreamFrame(nil, []byte(body)))
}

func bedrockStreamChunks() string {
	var b strings.Builder
	b.WriteString(bedrockFrame(`{"messageStart":{"role":"assistant"}}`))
	b.WriteString(bedrockFrame(`{"contentBlockStart":{"contentBlockIndex":0,"start":{"toolUse":{"toolUseId":"tu_1","name":"doit"}}}}`))
	b.WriteString(bedrockFrame(`{"contentBlockDelta":{"contentBlockIndex":0,"delta":{"toolUse":{"input":"{\"x\":"}}}}`))
	b.WriteString(bedrockFrame(`{"contentBlockDelta":{"contentBlockIndex":0,"delta":{"toolUse":{"input":"1}"}}}}`))
	b.WriteString(bedrockFrame(`{"contentBlockStop":{"contentBlockIndex":0}}`))
	b.WriteString(bedrockFrame(`{"contentBlockDelta":{"contentBlockIndex":1,"delta":{"text":"Hi there"}}}`))
	b.WriteString(bedrockFrame(`{"contentBlockStop":{"contentBlockIndex":1}}`))
	b.WriteString(bedrockFrame(`{"contentBlockDelta":{"contentBlockIndex":2,"delta":{"reasoningContent":{"text":"thinking hard","signature":"sig-1"}}}}`))
	b.WriteString(bedrockFrame(`{"contentBlockDelta":{"contentBlockIndex":2,"delta":{"reasoningContent":{"signature":"sig-2"}}}}`))
	b.WriteString(bedrockFrame(`{"contentBlockStop":{"contentBlockIndex":2}}`))
	b.WriteString(bedrockFrame(`{"messageStop":{"stopReason":"tool_use"}}`))
	b.WriteString(bedrockFrame(`{"metadata":{"usage":{"inputTokens":100,"outputTokens":50,"cacheReadInputTokens":10,"cacheWriteInputTokens":20,"totalTokens":180}}}`))
	return b.String()
}

// --- URL, auth, SigV4 ---

func TestBedrockURLAndAuth(t *testing.T) {
	clearBedrockEnv(t)
	var gotPath, gotAuth, gotDate string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		gotDate = r.Header.Get("X-Amz-Date")
		_, _ = w.Write([]byte(bedrockStreamChunks()))
	}))
	defer srv.Close()

	// The reference's dotted modelId: dots stay literal, the colon becomes %3A
	// (smithy extended-encode for resolved path labels).
	m := bedrockTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	opts := &AmazonBedrockOptions{AccessKey: "AKID", SecretKey: "secret", Region: "us-east-1"}
	final, _, errMsg := collectBedrockStream(t, m, opts)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	wantPath := "/model/anthropic.claude-sonnet-4-5-20250929-v1%3A0/converse-stream"
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q (dots literal, colon encoded)", gotPath, wantPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=AKID/") {
		t.Fatalf("Authorization = %q, want SigV4", gotAuth)
	}
	// host must be a signed header (the P12 headline bug).
	if !strings.Contains(gotAuth, "SignedHeaders=accept;accept-encoding;content-type;host;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("SignedHeaders missing host: %q", gotAuth)
	}
	if gotDate == "" {
		t.Fatal("X-Amz-Date missing")
	}
	if final == nil || final.StopReason != src.StopReasonToolUse {
		t.Fatalf("final/stopReason = %v/%v", final, final.StopReason)
	}
}

func TestBedrockBearerToken(t *testing.T) {
	clearBedrockEnv(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(bedrockFrame(`{"messageStart":{"role":"assistant"}}`) + bedrockFrame(`{"messageStop":{"stopReason":"end_turn"}}`)))
	}))
	defer srv.Close()

	m := bedrockTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	_, _, errMsg := collectBedrockStream(t, m, &AmazonBedrockOptions{BearerToken: "tok-1"})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if gotAuth != "Bearer tok-1" {
		t.Fatalf("Authorization = %q, want Bearer (no SigV4)", gotAuth)
	}
}

func TestBedrockMissingCredentials(t *testing.T) {
	clearBedrockEnv(t)
	errMsg := ""
	for ev := range StreamAmazonBedrock(bedrockTestModel(nil), src.ChatContext{}, &AmazonBedrockOptions{})() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	if !strings.Contains(errMsg, "Could not load credentials") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

// --- Region/endpoint resolution matrix ---

func TestBedrockRegionEndpointMatrix(t *testing.T) {
	clearBedrockEnv(t)

	// Catalog standard base, nothing configured: explicit endpoint = the base
	// URL itself; region defaults to us-east-1.
	region, endpoint, err := resolveBedrockRegionAndEndpoint(bedrockTestModel(nil), nil)
	if err != nil || region != "us-east-1" || endpoint != "https://bedrock-runtime.us-east-1.amazonaws.com" {
		t.Fatalf("default: region=%q endpoint=%q err=%v", region, endpoint, err)
	}

	// AWS_REGION set: standard endpoint is NOT used explicitly (SDK default
	// regional endpoint with the configured region).
	t.Setenv("AWS_REGION", "eu-west-1")
	region, endpoint, err = resolveBedrockRegionAndEndpoint(bedrockTestModel(nil), nil)
	if err != nil || region != "eu-west-1" || endpoint != "https://bedrock-runtime.eu-west-1.amazonaws.com" {
		t.Fatalf("env region: region=%q endpoint=%q err=%v", region, endpoint, err)
	}
	t.Setenv("AWS_REGION", "")

	// Custom (non-standard) endpoint: always used explicitly, region falls to
	// us-east-1.
	m := bedrockTestModel(func(m *src.Model[src.Api]) { m.BaseURL = "https://vpc-endpoint.example.com" })
	region, endpoint, err = resolveBedrockRegionAndEndpoint(m, nil)
	if err != nil || region != "us-east-1" || endpoint != "https://vpc-endpoint.example.com" {
		t.Fatalf("custom: region=%q endpoint=%q err=%v", region, endpoint, err)
	}

	// EU catalog model: endpoint region extracted from the standard host and
	// used as the region when nothing else is configured (TS L145-146).
	eu := bedrockTestModel(func(m *src.Model[src.Api]) {
		m.ID = "eu.anthropic.claude-sonnet-4-6"
		m.BaseURL = "https://bedrock-runtime.eu-central-1.amazonaws.com"
	})
	region, endpoint, err = resolveBedrockRegionAndEndpoint(eu, nil)
	if err != nil || region != "eu-central-1" || endpoint != "https://bedrock-runtime.eu-central-1.amazonaws.com" {
		t.Fatalf("eu: region=%q endpoint=%q err=%v", region, endpoint, err)
	}

	// Profile configured with a region in ~/.aws/config.
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	awsDir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(awsDir, "config"), []byte("[profile work]\nregion = ap-southeast-2\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AWS_PROFILE", "work")
	region, endpoint, err = resolveBedrockRegionAndEndpoint(bedrockTestModel(nil), nil)
	if err != nil || region != "ap-southeast-2" || endpoint != "https://bedrock-runtime.ap-southeast-2.amazonaws.com" {
		t.Fatalf("profile: region=%q endpoint=%q err=%v", region, endpoint, err)
	}

	// Shared credentials file provides signing material.
	if err := os.WriteFile(filepath.Join(awsDir, "credentials"), []byte("[work]\naws_access_key_id = AKID-PROFILE\naws_secret_access_key = secret-profile\naws_session_token = token-profile\n"), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	creds, err := loadBedrockSharedCredentials("work")
	if err != nil || creds == nil || creds.AccessKey != "AKID-PROFILE" || creds.SessionToken != "token-profile" {
		t.Fatalf("shared creds = %+v err=%v", creds, err)
	}
}

// --- Body pins ---

func captureBedrockBody(t *testing.T, m src.Model[src.Api], chatCtx src.ChatContext, opts *AmazonBedrockOptions) (map[string]interface{}, string) {
	t.Helper()
	var body map[string]interface{}
	var errMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		_, _ = w.Write([]byte(bedrockFrame(`{"messageStart":{"role":"assistant"}}`) + bedrockFrame(`{"messageStop":{"stopReason":"end_turn"}}`)))
	}))
	defer srv.Close()

	mutated := m
	mutated.BaseURL = srv.URL
	if opts == nil {
		opts = &AmazonBedrockOptions{}
	}
	if opts.AccessKey == "" && opts.BearerToken == "" {
		opts.AccessKey = "AKID-TEST"
		opts.SecretKey = "secret-test"
	}
	for ev := range StreamAmazonBedrock(mutated, chatCtx, opts)() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	return body, errMsg
}

func TestBedrockBodyPins(t *testing.T) {
	clearBedrockEnv(t)
	maxTok := 4096
	temp := 0.7
	auto := "auto"
	chatCtx := src.ChatContext{
		SystemPrompt: "sys prompt",
		Tools: []src.Tool[any]{
			{Name: "doit", Description: "does it", Parameters: map[string]interface{}{"type": "object"}},
		},
		Messages: []src.Message{
			&src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{
				&src.TextContent{Type: "text", Text: "look"},
				&src.ImageContent{Type: "image", MimeType: "image/png", Data: "aGVsbG8="},
			}},
			&src.AssistantMessage{
				Role: src.RoleAssistant, Provider: src.Provider("amazon-bedrock"), Api: src.Api("bedrock-converse-stream"), Model: "anthropic.claude-sonnet-4-5-20250929-v1:0",
				Content: []src.ContentBlock{
					&src.ThinkingContent{Type: "thinking", Thinking: "pondering", ThinkingSignature: "sig-9"},
					&src.ToolCall{Type: "toolCall", ID: "tu-call-1", Name: "doit", Arguments: map[string]interface{}{"x": float64(1)}},
				},
			},
			&src.ToolResultMessage[any]{Role: src.RoleTool, ToolCallID: "tu-call-1", ToolName: "doit", IsError: true, Content: []src.ContentBlock{
				&src.TextContent{Type: "text", Text: "boom"},
			}},
			&src.ToolResultMessage[any]{Role: src.RoleTool, ToolCallID: "tu-call-2", ToolName: "doit", Content: []src.ContentBlock{
				&src.TextContent{Type: "text", Text: "ok"},
			}},
		},
	}
	body, errMsg := captureBedrockBody(t, bedrockTestModel(nil), chatCtx, &AmazonBedrockOptions{MaxTokens: &maxTok, Temperature: &temp, ToolChoice: auto})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}

	if body["modelId"] != "anthropic.claude-sonnet-4-5-20250929-v1:0" {
		t.Fatalf("modelId = %v (dots must be preserved in the body)", body["modelId"])
	}

	// System: [{text}, {cachePoint}] — Claude 4.5 supports prompt caching.
	system, _ := body["system"].([]interface{})
	if len(system) != 2 {
		t.Fatalf("system = %v (text block + cachePoint)", system)
	}
	if sys0, _ := system[0].(map[string]interface{}); sys0["text"] != "sys prompt" {
		t.Fatalf("system[0] = %v (must be a text block, not a string)", sys0)
	}
	if _, ok := system[1].(map[string]interface{})["cachePoint"]; !ok {
		t.Fatalf("system[1] = %v, want cachePoint", system[1])
	}

	// inferenceConfig: maxTokens + temperature only (no topP/topK).
	inference, _ := body["inferenceConfig"].(map[string]interface{})
	if inference["maxTokens"] != float64(4096) || inference["temperature"] != 0.7 {
		t.Fatalf("inferenceConfig = %v", inference)
	}
	if _, has := inference["topP"]; has {
		t.Fatal("topP must not be sent (TS parity)")
	}

	// toolConfig: {json:} schema wrapper + toolChoice.
	toolConfig, _ := body["toolConfig"].(map[string]interface{})
	tools, _ := toolConfig["tools"].([]interface{})
	tool0, _ := tools[0].(map[string]interface{})
	spec, _ := tool0["toolSpec"].(map[string]interface{})
	schema, _ := spec["inputSchema"].(map[string]interface{})
	if _, ok := schema["json"]; !ok {
		t.Fatalf("inputSchema = %v, want {json: ...} wrapper", spec["inputSchema"])
	}
	if _, ok := toolConfig["toolChoice"].(map[string]interface{})["auto"]; !ok {
		t.Fatalf("toolChoice = %v, want {auto:{}}", toolConfig["toolChoice"])
	}

	// Messages: user (text + image), assistant (reasoning w/ signature + toolUse
	// with toolUseId), coalesced tool results in ONE user message.
	msgs, _ := body["messages"].([]interface{})
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3 (two toolResults coalesced): %v", len(msgs), body)
	}

	user0, _ := msgs[0].(map[string]interface{})
	userContent, _ := user0["content"].([]interface{})
	imgBlock, _ := userContent[1].(map[string]interface{})
	image, _ := imgBlock["image"].(map[string]interface{})
	if image["format"] != "png" {
		t.Fatalf("image = %v", imgBlock)
	}
	source, _ := image["source"].(map[string]interface{})
	if source["bytes"] != "aGVsbG8=" {
		t.Fatalf("image bytes = %v (base64 round-trip)", source["bytes"])
	}

	assistant, _ := msgs[1].(map[string]interface{})
	assistantContent, _ := assistant["content"].([]interface{})
	reasoning, _ := assistantContent[0].(map[string]interface{})
	rc, _ := reasoning["reasoningContent"].(map[string]interface{})
	rt, _ := rc["reasoningText"].(map[string]interface{})
	if rt["signature"] != "sig-9" || rt["text"] != "pondering" {
		t.Fatalf("reasoningContent = %v (signature must replay for Claude)", reasoning)
	}
	toolUseBlock, _ := assistantContent[1].(map[string]interface{})
	toolUse, _ := toolUseBlock["toolUse"].(map[string]interface{})
	if toolUse["toolUseId"] == nil || toolUse["name"] != "doit" {
		t.Fatalf("toolUse = %v (toolUseId required)", toolUse)
	}

	// Coalesced tool results: one user message, per-block content + statuses.
	results, _ := msgs[2].(map[string]interface{})
	resultContent, _ := results["content"].([]interface{})
	if len(resultContent) != 3 {
		t.Fatalf("coalesced tool results + cachePoint = %d, want 3", len(resultContent))
	}
	first, _ := resultContent[0].(map[string]interface{})
	firstResult, _ := first["toolResult"].(map[string]interface{})
	if firstResult["status"] != "error" || firstResult["toolUseId"] != "tu-call-1" {
		t.Fatalf("first toolResult = %v (status must reflect isError)", firstResult)
	}
	second, _ := resultContent[1].(map[string]interface{})
	secondResult, _ := second["toolResult"].(map[string]interface{})
	if secondResult["status"] != "success" {
		t.Fatalf("second toolResult = %v", secondResult)
	}

	// Claude 4.5 + caching: final cachePoint appended to the last user message.
	if _, ok := resultContent[len(resultContent)-1].(map[string]interface{})["cachePoint"]; !ok {
		t.Fatalf("last user message must end with a cachePoint: %v", resultContent)
	}
}

func TestBedrockAdditionalModelRequestFields(t *testing.T) {
	clearBedrockEnv(t)

	// No reasoning / non-reasoning model → nil.
	if got := buildBedrockAdditionalModelRequestFields(bedrockTestModel(nil), &AmazonBedrockOptions{}); got != nil {
		t.Fatalf("empty reasoning = %v", got)
	}
	nonClaude := bedrockTestModel(func(m *src.Model[src.Api]) {
		m.ID = "deepseek.r1-v1:0"
		m.Name = "DeepSeek-R1" // detection also checks the name for "claude"
	})
	if got := buildBedrockAdditionalModelRequestFields(nonClaude, &AmazonBedrockOptions{Reasoning: src.Medium}); got != nil {
		t.Fatalf("non-Claude model must not get additional fields: %v", got)
	}
	nonReasoning := bedrockTestModel(func(m *src.Model[src.Api]) { m.Reasoning = false })
	if got := buildBedrockAdditionalModelRequestFields(nonReasoning, &AmazonBedrockOptions{Reasoning: src.Medium}); got != nil {
		t.Fatalf("model without reasoning flag = %v", got)
	}

	// Adaptive model (sonnet-4-6): thinking adaptive + output_config.effort.
	adaptive := bedrockTestModel(func(m *src.Model[src.Api]) { m.ID = "anthropic.claude-sonnet-4-6" })
	got := buildBedrockAdditionalModelRequestFields(adaptive, &AmazonBedrockOptions{Reasoning: src.Medium})
	thinking, _ := got["thinking"].(map[string]interface{})
	if thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Fatalf("adaptive thinking = %v", thinking)
	}
	oc, _ := got["output_config"].(map[string]interface{})
	if oc["effort"] != "medium" {
		t.Fatalf("output_config = %v", oc)
	}
	if _, has := got["anthropic_beta"]; has {
		t.Fatal("adaptive models must not carry anthropic_beta")
	}

	// Adaptive + native xhigh (opus-4-7) + thinkingLevelMap "max" (opus-4-6).
	opus47 := bedrockTestModel(func(m *src.Model[src.Api]) { m.ID = "anthropic.claude-opus-4-7" })
	got = buildBedrockAdditionalModelRequestFields(opus47, &AmazonBedrockOptions{Reasoning: src.XHigh})
	if oc, _ := got["output_config"].(map[string]interface{}); oc["effort"] != "xhigh" {
		t.Fatalf("opus-4-7 xhigh effort = %v, want xhigh (native)", oc)
	}
	opus46 := bedrockTestModel(func(m *src.Model[src.Api]) {
		m.ID = "anthropic.claude-opus-4-6-v1"
		m.ThinkingLevelMap = map[string]*src.ThinkingLevel{"xhigh": src.ThinkingLevelPtr(src.ThinkingLevel("max"))}
	})
	got = buildBedrockAdditionalModelRequestFields(opus46, &AmazonBedrockOptions{Reasoning: src.XHigh})
	if oc, _ := got["output_config"].(map[string]interface{}); oc["effort"] != "max" {
		t.Fatalf("opus-4-6 xhigh effort = %v, want max (thinkingLevelMap)", oc)
	}

	// Non-adaptive Claude: enabled + default budgets + anthropic_beta default-on.
	got = buildBedrockAdditionalModelRequestFields(bedrockTestModel(nil), &AmazonBedrockOptions{Reasoning: src.Medium})
	thinking, _ = got["thinking"].(map[string]interface{})
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != 8192 {
		t.Fatalf("enabled thinking = %v", thinking)
	}
	if beta, ok := got["anthropic_beta"].([]string); !ok || len(beta) != 1 || beta[0] != amazonBedrockInterleavedBeta {
		t.Fatalf("anthropic_beta = %v", got["anthropic_beta"])
	}

	// xhigh clamps to high for budget lookup (default 16384).
	got = buildBedrockAdditionalModelRequestFields(bedrockTestModel(nil), &AmazonBedrockOptions{Reasoning: src.XHigh})
	if thinking["budget_tokens"] = got["thinking"].(map[string]interface{})["budget_tokens"]; thinking["budget_tokens"] != 16384 {
		t.Fatalf("xhigh budget = %v, want 16384 (clamped to high)", thinking["budget_tokens"])
	}

	// Custom budgets override defaults.
	high := 4096
	got = buildBedrockAdditionalModelRequestFields(bedrockTestModel(nil), &AmazonBedrockOptions{
		Reasoning:       src.High,
		ThinkingBudgets: &src.ThinkingBudgets{High: &high},
	})
	if got["thinking"].(map[string]interface{})["budget_tokens"] != 4096 {
		t.Fatalf("custom budget = %v", got["thinking"])
	}

	// GovCloud omits display.
	gov := bedrockTestModel(func(m *src.Model[src.Api]) { m.ID = "us-gov.anthropic.claude-sonnet-4-5" })
	got = buildBedrockAdditionalModelRequestFields(gov, &AmazonBedrockOptions{Reasoning: src.Medium})
	if _, has := got["thinking"].(map[string]interface{})["display"]; has {
		t.Fatal("GovCloud target must omit thinking.display")
	}

	// interleavedThinking=false disables the beta.
	falsy := false
	got = buildBedrockAdditionalModelRequestFields(bedrockTestModel(nil), &AmazonBedrockOptions{Reasoning: src.Medium, InterleavedThinking: &falsy})
	if _, has := got["anthropic_beta"]; has {
		t.Fatal("anthropic_beta must be omitted with interleavedThinking=false")
	}
}

// --- Stream events / errors ---

func TestBedrockStreamHappyPath(t *testing.T) {
	clearBedrockEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bedrockStreamChunks()))
	}))
	defer srv.Close()

	m := bedrockTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	final, events, errMsg := collectBedrockStream(t, m, nil)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if final == nil {
		t.Fatal("no done event")
	}
	if final.StopReason != src.StopReasonToolUse {
		t.Fatalf("stopReason = %q, want toolUse (tool_use)", final.StopReason)
	}

	// Usage + cost (3/1e6*100 + 15/1e6*50; cache terms 0.3/1e6*10, 3.75/1e6*20).
	if final.Usage.Input != 100 || final.Usage.Output != 50 || final.Usage.TotalTokens != 180 {
		t.Fatalf("usage = %+v (converse-era keys)", final.Usage)
	}
	if final.Usage.CacheRead != 10 || final.Usage.CacheWrite != 20 {
		t.Fatalf("cache usage = %v/%v", final.Usage.CacheRead, final.Usage.CacheWrite)
	}
	wantIn := (3/1e6)*100 + (15/1e6)*50 + (0.3/1e6)*10 + (3.75/1e6)*20
	if fmt.Sprintf("%.12f", final.Usage.Cost.Total) != fmt.Sprintf("%.12f", wantIn) {
		t.Fatalf("cost total = %v, want %v", final.Usage.Cost.Total, wantIn)
	}

	// Content: toolCall block first (index 0), text block (index 1), thinking
	// block (index 2) — in contentBlock order.
	if len(final.Content) != 3 {
		t.Fatalf("content blocks = %d", len(final.Content))
	}
	tc, ok := final.Content[0].(*src.ToolCall)
	if !ok || tc.ID != "tu_1" || tc.Name != "doit" || tc.Arguments["x"] != float64(1) {
		t.Fatalf("toolCall block = %+v (args must accumulate across deltas)", final.Content[0])
	}
	text, ok := final.Content[1].(*src.TextContent)
	if !ok || text.Text != "Hi there" {
		t.Fatalf("text block = %+v", final.Content[1])
	}
	th, ok := final.Content[2].(*src.ThinkingContent)
	if !ok || th.Thinking != "thinking hard" || th.ThinkingSignature != "sig-1sig-2" {
		t.Fatalf("thinking block = %+v (signature deltas accumulate)", final.Content[2])
	}

	// Event sequence: start (on messageStart), toolcall_start/delta/end,
	// text_start/delta/end, thinking events.
	sawStart, sawToolStart, sawToolEnd, sawTextDelta, sawThinkingDelta := false, false, false, false, false
	for _, ev := range events {
		switch ev.Type {
		case "start":
			sawStart = true
		case "toolcall_start":
			sawToolStart = true
		case "toolcall_end":
			sawToolEnd = true
		case "text_delta":
			sawTextDelta = true
		case "thinking_delta":
			sawThinkingDelta = true
		}
	}
	if !sawStart || !sawToolStart || !sawToolEnd || !sawTextDelta || !sawThinkingDelta {
		t.Fatalf("missing events: start=%v toolStart=%v toolEnd=%v textDelta=%v thinkingDelta=%v",
			sawStart, sawToolStart, sawToolEnd, sawTextDelta, sawThinkingDelta)
	}
}

func TestBedrockThrottlingException(t *testing.T) {
	clearBedrockEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bedrockFrame(`{"messageStart":{"role":"assistant"}}`) + bedrockFrame(`{"throttlingException":{"message":"Rate exceeded"}}`)))
	}))
	defer srv.Close()

	_, _, errMsg := collectBedrockStream(t, bedrockTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL }), nil)
	if errMsg != "Throttling error: Rate exceeded" {
		t.Fatalf("errMsg = %q, want Throttling error prefix", errMsg)
	}
}

func TestBedrockStatusErrorFormatting(t *testing.T) {
	clearBedrockEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"type":"AccessDeniedException","message":"not authorized"}`))
	}))
	defer srv.Close()

	_, _, errMsg := collectBedrockStream(t, bedrockTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL }), nil)
	if errMsg != "AccessDeniedException: not authorized" {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestBedrockStopReasons(t *testing.T) {
	cases := map[string]src.StopReason{
		"end_turn":                      src.StopReasonStop,
		"stop_sequence":                 src.StopReasonStop,
		"max_tokens":                    src.StopReasonLength,
		"model_context_window_exceeded": src.StopReasonLength,
		"tool_use":                      src.StopReasonToolUse,
		"anything_else":                 src.StopReasonError,
		"":                              src.StopReasonError,
	}
	for wire, want := range cases {
		if got := mapAmazonBedrockStopReason(wire); got != want {
			t.Fatalf("mapAmazonBedrockStopReason(%q) = %q, want %q", wire, got, want)
		}
	}
}

// --- Simple wrapper ---

func captureBedrockSimpleBody(t *testing.T, m src.Model[src.Api], o *src.SimpleStreamOptions) (map[string]interface{}, string) {
	t.Helper()
	var body map[string]interface{}
	var errMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		_, _ = w.Write([]byte(bedrockFrame(`{"messageStart":{"role":"assistant"}}`) + bedrockFrame(`{"messageStop":{"stopReason":"end_turn"}}`)))
	}))
	defer srv.Close()

	mutated := m
	mutated.BaseURL = srv.URL
	// The simple wrapper resolves credentials from the env chain.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKID-TEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret-test")
	for ev := range StreamSimpleAmazonBedrock(mutated, src.ChatContext{}, o)() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	return body, errMsg
}

func TestBedrockSimpleWrapper(t *testing.T) {
	clearBedrockEnv(t)

	// No reasoning: no additionalModelRequestFields at all.
	body, errMsg := captureBedrockSimpleBody(t, bedrockTestModel(nil), &src.SimpleStreamOptions{})
	if errMsg != "" {
		t.Fatalf("no-reasoning: %s", errMsg)
	}
	if _, has := body["additionalModelRequestFields"]; has {
		t.Fatalf("no-reasoning additionalModelRequestFields = %v", body["additionalModelRequestFields"])
	}

	withReasoning := func(level src.ThinkingLevel) *src.SimpleStreamOptions {
		o := &src.SimpleStreamOptions{}
		o.Reasoning = level
		return o
	}

	// Non-Claude reasoning model: reasoning passed through (budget resolved at
	// request build; deepseek is not Claude so no additional fields either).
	nonClaude := bedrockTestModel(func(m *src.Model[src.Api]) {
		m.ID = "deepseek.r1-v1:0"
		m.Name = "DeepSeek-R1"
		m.Reasoning = true
	})
	body, errMsg = captureBedrockSimpleBody(t, nonClaude, withReasoning(src.High))
	if errMsg != "" {
		t.Fatalf("non-Claude: %s", errMsg)
	}
	if _, has := body["additionalModelRequestFields"]; has {
		t.Fatalf("non-Claude additionalModelRequestFields = %v", body["additionalModelRequestFields"])
	}

	// Claude non-adaptive: maxTokens adjusted (32000 base + 8192 budget) and
	// thinking enabled with the merged budget.
	body, errMsg = captureBedrockSimpleBody(t, bedrockTestModel(nil), withReasoning(src.Medium))
	if errMsg != "" {
		t.Fatalf("claude: %s", errMsg)
	}
	inference, _ := body["inferenceConfig"].(map[string]interface{})
	if inference["maxTokens"] != float64(40192) {
		t.Fatalf("claude maxTokens = %v, want 40192 (32000 + 8192, model cap 64000)", inference["maxTokens"])
	}
	amrf, _ := body["additionalModelRequestFields"].(map[string]interface{})
	if amrf == nil {
		t.Fatal("claude reasoning must produce additionalModelRequestFields")
	}
	thinking, _ := amrf["thinking"].(map[string]interface{})
	if thinking["budget_tokens"] != float64(8192) {
		t.Fatalf("claude budget = %v", thinking)
	}

	// Claude adaptive: budgets passed through unchanged, no maxTokens inflation.
	adaptive := bedrockTestModel(func(m *src.Model[src.Api]) { m.ID = "anthropic.claude-sonnet-4-6" })
	body, errMsg = captureBedrockSimpleBody(t, adaptive, withReasoning(src.Medium))
	if errMsg != "" {
		t.Fatalf("adaptive: %s", errMsg)
	}
	inference, _ = body["inferenceConfig"].(map[string]interface{})
	if inference["maxTokens"] != float64(32000) {
		t.Fatalf("adaptive maxTokens = %v, want 32000 (no adjustment)", inference["maxTokens"])
	}
	amrf, _ = body["additionalModelRequestFields"].(map[string]interface{})
	thinking, _ = amrf["thinking"].(map[string]interface{})
	if thinking["type"] != "adaptive" {
		t.Fatalf("adaptive thinking = %v", thinking)
	}
}

// --- Registry & catalog wiring ---

func TestBedrockRegistered(t *testing.T) {
	if p := src.GetApiProvider("bedrock-converse-stream"); p == nil {
		t.Fatal("bedrock-converse-stream not registered")
	}

	if len(src.Models["amazon-bedrock"]) != 93 {
		t.Fatalf("amazon-bedrock catalog = %d models, want 93", len(src.Models["amazon-bedrock"]))
	}
	reasoning, tlm, eu := 0, 0, 0
	for _, m := range src.Models["amazon-bedrock"] {
		if m.Api != src.Api("bedrock-converse-stream") || m.Provider != src.Provider("amazon-bedrock") {
			t.Fatalf("catalog %s api/provider = %s/%s", m.ID, m.Api, m.Provider)
		}
		if m.BaseURL == "https://bedrock-runtime.eu-central-1.amazonaws.com" {
			eu++
		}
		if m.Reasoning {
			reasoning++
		}
		if m.ThinkingLevelMap != nil {
			tlm++
		}
	}
	if reasoning != 53 {
		t.Fatalf("reasoning models = %d, want 53", reasoning)
	}
	if tlm != 9 {
		t.Fatalf("thinkingLevelMap models = %d, want 9", tlm)
	}
	if eu != 7 {
		t.Fatalf("eu-central-1 models = %d, want 7", eu)
	}

	// Typos must be gone: correct api spelling and claude spelling.
	sonnet := src.Models["amazon-bedrock"]["anthropic.claude-3-5-sonnet-20241022-v2:0"]
	if sonnet == nil {
		t.Fatal("anthropic.claude-3-5-sonnet-20241022-v2:0 missing (cluade typo must be fixed)")
	}
	opus47 := src.Models["amazon-bedrock"]["anthropic.claude-opus-4-7"]
	if opus47 == nil || opus47.ThinkingLevelMap == nil {
		t.Fatal("anthropic.claude-opus-4-7 missing or has no thinkingLevelMap")
	}
}
