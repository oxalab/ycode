package providers

import (
	"bytes"
	gocontext "context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	amazonBedrockBaseURLTemplate = "https://bedrock-runtime.%s.amazonaws.com"
	// amazonBedrockEndpointRegionPattern matches standard AWS Bedrock runtime
	// endpoints and captures the region (amazon-bedrock.ts:843-855).
	amazonBedrockStandardEndpointPattern = `^bedrock-runtime(?:-fips)?\.([a-z0-9-]+)\.amazonaws\.com(?:\.cn)?$`
	// amazonBedrockInterleavedBeta is the default thinking beta for non-adaptive
	// Claude models (amazon-bedrock.ts:919-921).
	amazonBedrockInterleavedBeta = "interleaved-thinking-2025-05-14"
)

// amazonBedrockErrorPrefixes maps Bedrock SDK exception names to stable
// human-readable prefixes (amazon-bedrock.ts:287-293). Downstream retry
// logic matches `server.?error` / `service.?unavailable` patterns.
var amazonBedrockErrorPrefixes = map[string]string{
	"InternalServerException":    "Internal server error",
	"ModelStreamErrorException":  "Model stream error",
	"ValidationException":        "Validation error",
	"ThrottlingException":        "Throttling error",
	"ServiceUnavailableException": "Service unavailable",
}

var amazonBedrockStandardEndpointRe = regexp.MustCompile(amazonBedrockStandardEndpointPattern)

// =============================================================================
// Types
// =============================================================================

// AmazonBedrockOptions mirrors BedrockOptions in amazon-bedrock.ts plus the
// Go Context seam. HTTP/1.1 transport and proxy-agent support are accepted
// divergences (the reference uses NodeHttp2Handler by default; Bedrock
// accepts HTTP/1.1 for converse-stream).
type AmazonBedrockOptions struct {
	Region              string
	Profile             string
	ToolChoice          interface{} // "auto" | "any" | "none" | {type:"tool", name:string}
	Reasoning           src.ThinkingLevel
	ThinkingBudgets     *src.ThinkingBudgets
	InterleavedThinking *bool
	ThinkingDisplay     string // "summarized" | "omitted"
	RequestMetadata     map[string]string
	BearerToken         string
	// Explicit credentials (Go seam; the reference resolves via the SDK chain).
	AccessKey    string
	SecretKey    string
	SessionToken string
	CacheRetention src.CacheRetention
	MaxTokens      *int
	Temperature    *float64
	Headers        map[string]string
	Context        gocontext.Context
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

type bedrockResolvedCredentials struct {
	AccessKey    string
	SecretKey    string
	SessionToken string
}

type bedrockBlockState struct {
	Index       int
	Type        string // "text" | "thinking" | "toolCall"
	Text        string
	Thinking    string
	Signature   string
	ToolUseID   string
	Name        string
	PartialJSON string
	ContentIdx  int
}

// bedrockStateToContent materializes the src content block for a state.
func bedrockStateToContent(state *bedrockBlockState) src.ContentBlock {
	switch state.Type {
	case "thinking":
		return &src.ThinkingContent{Type: "thinking", Thinking: state.Thinking, ThinkingSignature: state.Signature}
	case "toolCall":
		return &src.ToolCall{Type: "toolCall", ID: state.ToolUseID, Name: state.Name, Arguments: map[string]interface{}{}}
	default:
		return &src.TextContent{Type: "text", Text: ""}
	}
}

// =============================================================================
// Streaming
// =============================================================================

// StreamAmazonBedrock creates an async streaming function for Amazon Bedrock
func StreamAmazonBedrock(model src.Model[src.Api], context src.ChatContext, options *AmazonBedrockOptions) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			ctx := gocontext.Background()
			if options != nil && options.Context != nil {
				ctx = options.Context
			}

			output := &src.AssistantMessage{
				Role:       src.RoleAssistant,
				Content:    []src.ContentBlock{},
				Api:        model.Api,
				Provider:   model.Provider,
				Model:      model.ID,
				Usage:      src.Usage{Cost: src.UsageCost{}},
				StopReason: src.StopReasonStop,
			}

			// Region + endpoint resolution (amazon-bedrock.ts:115-189).
			region, endpoint, err := resolveBedrockRegionAndEndpoint(model, options)
			if err != nil {
				handleAmazonBedrockError(ch, output, err, false)
				return
			}

			// Auth: bearer token bypasses SigV4 entirely (TS L134-136, 191-194).
			skipAuth := os.Getenv("AWS_BEDROCK_SKIP_AUTH") == "1"
			bearerToken := ""
			if options != nil && options.BearerToken != "" {
				bearerToken = options.BearerToken
			}
			if bearerToken == "" {
				bearerToken = os.Getenv("AWS_BEARER_TOKEN_BEDROCK")
			}
			useBearer := bearerToken != "" && !skipAuth

			var creds *bedrockResolvedCredentials
			if !useBearer {
				creds, err = resolveBedrockCredentials(options, skipAuth)
				if err != nil {
					handleAmazonBedrockError(ch, output, err, false)
					return
				}
			}

			cacheRetention := resolveBedrockCacheRetention(options.CacheRetention)
			params, err := buildAmazonBedrockParams(model, context, options, cacheRetention)
			if err != nil {
				handleAmazonBedrockError(ch, output, err, false)
				return
			}

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doAmazonBedrockRequest(ctx, model, params, options, endpoint, region, creds, useBearer, bearerToken)
			if err != nil {
				if ctx.Err() != nil {
					handleAmazonBedrockError(ch, output, fmt.Errorf("Request was aborted"), true)
				} else {
					handleAmazonBedrockError(ch, output, err, false)
				}
				return
			}
			defer resp.Body.Close()

			if options != nil {
				responseHeaders := map[string]interface{}{}
				if reqID := resp.Header.Get("X-Amzn-Requestid"); reqID != "" {
					responseHeaders["x-amzn-requestid"] = reqID
				}
				// The reference surfaces onResponse only when the SDK reports a
				// status code; the raw HTTP response always has one.
				if options.OnResponse != nil {
					options.OnResponse(map[string]interface{}{
						"status":  resp.StatusCode,
						"headers": responseHeaders,
					}, model)
				}
			}

			// The reference pushes "start" when the messageStart event arrives,
			// not before the request.
			if err := processAmazonBedrockStream(resp.Body, ch, output, model, ctx); err != nil {
				if ctx.Err() != nil {
					handleAmazonBedrockError(ch, output, fmt.Errorf("Request was aborted"), true)
				} else {
					handleAmazonBedrockError(ch, output, err, false)
				}
				return
			}

			if ctx.Err() != nil {
				handleAmazonBedrockError(ch, output, fmt.Errorf("Request was aborted"), true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleAmazonBedrockError(ch, output, fmt.Errorf("An unknown error occurred"), false)
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// StreamSimpleAmazonBedrock maps provider-agnostic SimpleStreamOptions to
// Bedrock options (streamSimpleBedrock in amazon-bedrock.ts:311-353).
func StreamSimpleAmazonBedrock(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	base := BuildBaseOptions(model, options, "")

	reasoning := src.ThinkingLevel("")
	thinkingBudgets := (*src.ThinkingBudgets)(nil)
	if options != nil {
		reasoning = options.Reasoning
		thinkingBudgets = options.ThinkingBudgets
	}

	// No reasoning requested: pass through with reasoning explicitly unset.
	if reasoning == "" {
		opts := bedrockOptionsFromBase(base)
		opts.Reasoning = ""
		return StreamAmazonBedrock(model, context, opts)
	}

	if isAnthropicClaudeModel(model) {
		if bedrockSupportsAdaptiveThinking(model) {
			opts := bedrockOptionsFromBase(base)
			opts.Reasoning = reasoning
			opts.ThinkingBudgets = thinkingBudgets
			return StreamAmazonBedrock(model, context, opts)
		}

		baseMaxTokens := 0
		if base.MaxTokens != nil {
			baseMaxTokens = *base.MaxTokens
		}
		adjusted := AdjustMaxTokensForThinking(baseMaxTokens, model.MaxTokens, reasoning, thinkingBudgets)

		// Merge custom budgets with the adjusted budget for the clamped level.
		merged := src.ThinkingBudgets{}
		if thinkingBudgets != nil {
			merged = *thinkingBudgets
		}
		budget := adjusted.ThinkingBudget
		switch ClampReasoning(reasoning) {
		case src.Minimal:
			merged.Minimal = &budget
		case src.Low:
			merged.Low = &budget
		case src.Medium:
			merged.Medium = &budget
		default:
			merged.High = &budget
		}

		opts := bedrockOptionsFromBase(base)
		opts.MaxTokens = &adjusted.MaxTokens
		opts.Reasoning = reasoning
		opts.ThinkingBudgets = &merged
		return StreamAmazonBedrock(model, context, opts)
	}

	// Non-Claude: pass reasoning/budgets through (budget resolution happens in
	// buildAdditionalModelRequestFields).
	opts := bedrockOptionsFromBase(base)
	opts.Reasoning = reasoning
	opts.ThinkingBudgets = thinkingBudgets
	return StreamAmazonBedrock(model, context, opts)
}

func bedrockOptionsFromBase(base src.StreamOptions) *AmazonBedrockOptions {
	return &AmazonBedrockOptions{
		CacheRetention: base.CacheRetention,
		MaxTokens:      base.MaxTokens,
		Temperature:    base.Temperature,
		Headers:        base.Headers,
		Context:        base.Context,
	}
}

// =============================================================================
// Request handling
// =============================================================================

func doAmazonBedrockRequest(ctx gocontext.Context, model src.Model[src.Api], params interface{}, options *AmazonBedrockOptions, endpoint, region string, creds *bedrockResolvedCredentials, useBearer bool, bearerToken string) (*http.Response, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	endpointURL := endpoint + "/model/" + escapeBedrockURI(model.ID) + "/converse-stream"
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// All headers must be set BEFORE signing (SigV4 covers what it signs).
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	req.Header["Accept-Encoding"] = []string{"identity"}

	if options != nil {
		for k, v := range model.Headers {
			req.Header.Set(k, v)
		}
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	if useBearer {
		// Bedrock API key auth: Authorization: Bearer, no SigV4.
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	} else {
		awsSignV4(req, creds.AccessKey, creds.SecretKey, creds.SessionToken, region, "bedrock", body)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, formatAmazonBedrockHTTPError(resp.StatusCode, raw)
	}

	return resp, nil
}

// formatAmazonBedrockHTTPError mirrors formatBedrockError: AWS JSON error
// bodies carry the exception name in "type"/"__type" and a "message".
func formatAmazonBedrockHTTPError(status int, raw []byte) error {
	var parsed map[string]interface{}
	name := ""
	message := ""
	if err := json.Unmarshal(raw, &parsed); err == nil {
		name, _ = parsed["type"].(string)
		if name == "" {
			name, _ = parsed["__type"].(string)
		}
		if idx := strings.LastIndex(name, "#"); idx >= 0 {
			name = name[idx+1:]
		}
		if m, ok := parsed["message"].(string); ok {
			message = m
		} else if m, ok := parsed["Message"].(string); ok {
			message = m
		}
	}
	if name != "" {
		return fmt.Errorf("%s", bedrockFormatError(name, message))
	}
	bodyText := strings.TrimSpace(string(raw))
	if bodyText == "" {
		return fmt.Errorf("Bedrock API error (%d): Response returned an error code", status)
	}
	return fmt.Errorf("Bedrock API error (%d): %s", status, bodyText)
}

// bedrockFormatError applies the human-readable prefix table to an exception
// name + message (formatBedrockError in amazon-bedrock.ts:302-309).
func bedrockFormatError(name, message string) string {
	prefix, ok := amazonBedrockErrorPrefixes[name]
	if !ok {
		prefix = name
	}
	if message == "" {
		return prefix
	}
	return prefix + ": " + message
}

func handleAmazonBedrockError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
	output.StopReason = src.StopReasonError
	if isAborted {
		output.StopReason = src.StopReasonAborted
	}
	output.ErrorMessage = err.Error()
	ch <- src.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
}

// =============================================================================
// Credentials / region / endpoint
// =============================================================================

// resolveBedrockRegionAndEndpoint ports the region/endpoint resolution
// (amazon-bedrock.ts:115-189, 827-868). Returns (region, endpoint, error).
func resolveBedrockRegionAndEndpoint(model src.Model[src.Api], options *AmazonBedrockOptions) (string, string, error) {
	// getConfiguredBedrockRegion: option > env AWS_REGION > env AWS_DEFAULT_REGION.
	configuredRegion := ""
	if options != nil {
		configuredRegion = options.Region
	}
	if configuredRegion == "" {
		configuredRegion = os.Getenv("AWS_REGION")
	}
	if configuredRegion == "" {
		configuredRegion = os.Getenv("AWS_DEFAULT_REGION")
	}

	// hasConfiguredBedrockProfile: env AWS_PROFILE only (TS parity — the
	// options.profile field is not consulted here).
	hasProfile := os.Getenv("AWS_PROFILE") != ""

	endpointRegion := ""
	if model.BaseURL != "" {
		endpointRegion = getStandardBedrockEndpointRegion(model.BaseURL)
	}
	useExplicit := shouldUseExplicitBedrockEndpoint(model.BaseURL, configuredRegion, hasProfile)

	region := ""
	if configuredRegion != "" {
		region = configuredRegion
	} else if endpointRegion != "" && useExplicit {
		region = endpointRegion
	} else if !hasProfile {
		region = "us-east-1"
	} else {
		// Profile configured without an explicit region: resolve from the
		// shared config file (the SDK's default-chain equivalent).
		profile := os.Getenv("AWS_PROFILE")
		region = loadBedrockProfileRegion(profile)
		if region == "" {
			region = "us-east-1"
		}
	}

	endpoint := ""
	if useExplicit && model.BaseURL != "" {
		endpoint = model.BaseURL
	} else {
		endpoint = fmt.Sprintf(amazonBedrockBaseURLTemplate, region)
	}

	return region, endpoint, nil
}

func getStandardBedrockEndpointRegion(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	m := amazonBedrockStandardEndpointRe.FindStringSubmatch(strings.ToLower(u.Hostname()))
	if m == nil {
		return ""
	}
	return m[1]
}

func shouldUseExplicitBedrockEndpoint(baseURL, configuredRegion string, hasProfile bool) bool {
	endpointRegion := getStandardBedrockEndpointRegion(baseURL)
	if endpointRegion == "" {
		return true
	}
	return configuredRegion == "" && !hasProfile
}

// resolveBedrockCredentials walks the credential chain: explicit options →
// env vars → AWS_PROFILE shared-credentials file → skip-auth dummy creds.
func resolveBedrockCredentials(options *AmazonBedrockOptions, skipAuth bool) (*bedrockResolvedCredentials, error) {
	if options != nil && options.AccessKey != "" && options.SecretKey != "" {
		return &bedrockResolvedCredentials{AccessKey: options.AccessKey, SecretKey: options.SecretKey, SessionToken: options.SessionToken}, nil
	}

	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return &bedrockResolvedCredentials{
			AccessKey:    os.Getenv("AWS_ACCESS_KEY_ID"),
			SecretKey:    os.Getenv("AWS_SECRET_ACCESS_KEY"),
			SessionToken: os.Getenv("AWS_SESSION_TOKEN"),
		}, nil
	}

	profile := ""
	if options != nil && options.Profile != "" {
		profile = options.Profile
	} else if os.Getenv("AWS_PROFILE") != "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile != "" {
		if creds, err := loadBedrockSharedCredentials(profile); err == nil && creds != nil {
			return creds, nil
		}
	}

	// AWS_BEDROCK_SKIP_AUTH=1: sign with dummy credentials so the request can
	// reach anonymous/mocked endpoints (TS L152-157).
	if skipAuth {
		return &bedrockResolvedCredentials{AccessKey: "dummy-access-key", SecretKey: "dummy-secret-key"}, nil
	}

	return nil, fmt.Errorf("Could not load credentials from any providers: set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, AWS_PROFILE, or AWS_BEARER_TOKEN_BEDROCK")
}

// loadBedrockSharedCredentials reads an INI section from ~/.aws/credentials.
func loadBedrockSharedCredentials(profile string) (*bedrockResolvedCredentials, error) {
	path, err := bedrockSharedCredentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	section := parseIniSection(string(data), profile)
	access := section["aws_access_key_id"]
	secret := section["aws_secret_access_key"]
	if access == "" || secret == "" {
		return nil, fmt.Errorf("profile %q has no credentials in %s", profile, path)
	}
	return &bedrockResolvedCredentials{AccessKey: access, SecretKey: secret, SessionToken: section["aws_session_token"]}, nil
}

// loadBedrockProfileRegion reads the region for a profile from ~/.aws/config
// (sections are named "[profile <name>]" except "[default]").
func loadBedrockProfileRegion(profile string) string {
	path, err := bedrockSharedConfigPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sectionName := profile
	if sectionName != "default" {
		sectionName = "profile " + profile
	}
	return parseIniSection(string(data), sectionName)["region"]
}

func bedrockSharedCredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "credentials"), nil
}

func bedrockSharedConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "config"), nil
}

// parseIniSection extracts key/value pairs from one INI section.
func parseIniSection(content, section string) map[string]string {
	result := map[string]string{}
	current := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if current != section {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		result[key] = value
	}
	return result
}

// escapeBedrockURI ports the smithy extended-encode for resolved path
// labels (escapeUri = encodeURIComponent + !'()* uppercase-hex): keep
// A-Za-z0-9-_.~ literal, percent-encode everything else.
func escapeBedrockURI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// =============================================================================
// Parameter building
// =============================================================================

func buildAmazonBedrockParams(model src.Model[src.Api], context src.ChatContext, options *AmazonBedrockOptions, cacheRetention src.CacheRetention) (interface{}, error) {
	transformedMessages := TransformMessages(context.Messages, model, func(id string, _ src.Model[src.Api], _ src.AssistantMessage) string {
		return normalizeBedrockToolCallId(id)
	})

	messages, err := convertBedrockMessages(transformedMessages, model, cacheRetention)
	if err != nil {
		return nil, err
	}

	system, err := buildBedrockSystemPrompt(context.SystemPrompt, model, cacheRetention)
	if err != nil {
		return nil, err
	}

	inferenceConfig := map[string]interface{}{}
	if options != nil && options.MaxTokens != nil {
		inferenceConfig["maxTokens"] = *options.MaxTokens
	}
	if options != nil && options.Temperature != nil {
		inferenceConfig["temperature"] = *options.Temperature
	}

	toolChoice := ""
	var toolChoiceObj map[string]interface{}
	if options != nil {
		if s, ok := options.ToolChoice.(string); ok {
			toolChoice = s
		} else if m, ok := options.ToolChoice.(map[string]interface{}); ok {
			toolChoiceObj = m
		}
	}
	toolConfig := convertBedrockToolConfig(context.Tools, toolChoice, toolChoiceObj)

	params := map[string]interface{}{
		"modelId":         model.ID,
		"messages":        messages,
		"inferenceConfig": inferenceConfig,
	}

	if system != nil {
		params["system"] = system
	}
	if toolConfig != nil {
		params["toolConfig"] = toolConfig
	}

	additionalFields := buildBedrockAdditionalModelRequestFields(model, options)
	if additionalFields != nil {
		params["additionalModelRequestFields"] = additionalFields
	}

	if options != nil && options.RequestMetadata != nil {
		params["requestMetadata"] = options.RequestMetadata
	}

	return params, nil
}

// resolveBedrockCacheRetention ports resolveCacheRetention: option >
// PI_CACHE_RETENTION=long env > "short".
func resolveBedrockCacheRetention(retention src.CacheRetention) src.CacheRetention {
	if retention != "" {
		return retention
	}
	if os.Getenv("PI_CACHE_RETENTION") == "long" {
		return src.CacheRetentionLong
	}
	return src.CacheRetentionShort
}

func buildBedrockSystemPrompt(systemPrompt string, model src.Model[src.Api], cacheRetention src.CacheRetention) ([]map[string]interface{}, error) {
	if systemPrompt == "" {
		return nil, nil
	}

	blocks := []map[string]interface{}{
		{"text": utils.SanitizeSurrogates(systemPrompt)},
	}

	// Cache point for supported Claude models when caching is enabled.
	if cacheRetention != src.CacheRetentionNone && bedrockSupportsPromptCaching(model) {
		blocks = append(blocks, bedrockCachePointBlock(cacheRetention))
	}

	return blocks, nil
}

func bedrockCachePointBlock(cacheRetention src.CacheRetention) map[string]interface{} {
	cachePoint := map[string]interface{}{"type": "default"}
	if cacheRetention == src.CacheRetentionLong {
		cachePoint["ttl"] = "1h"
	}
	return map[string]interface{}{"cachePoint": cachePoint}
}

// normalizeBedrockToolCallId ports normalizeToolCallId (amazon-bedrock.ts:617-620).
func normalizeBedrockToolCallId(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	sanitized := b.String()
	if len(sanitized) > 64 {
		return sanitized[:64]
	}
	return sanitized
}

func convertBedrockMessages(transformedMessages []src.Message, model src.Model[src.Api], cacheRetention src.CacheRetention) ([]map[string]interface{}, error) {
	var result []map[string]interface{}

	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]
		role := msgRole(msg)

		switch role {
		case src.RoleUser:
			um, ok := msg.(*src.UserMessage)
			if !ok {
				continue
			}
			var content []map[string]interface{}
			for _, c := range um.Content {
				switch b := c.(type) {
				case *src.TextContent:
					content = append(content, map[string]interface{}{"text": utils.SanitizeSurrogates(b.Text)})
				case *src.ImageContent:
					imageBlock, err := createBedrockImageBlock(b.MimeType, b.Data)
					if err != nil {
						return nil, err
					}
					content = append(content, map[string]interface{}{"image": imageBlock})
				default:
					return nil, fmt.Errorf("Unknown user content type")
				}
			}
			result = append(result, map[string]interface{}{
				"role":    "user",
				"content": content,
			})

		case src.RoleAssistant:
			am := okAssistant(msg)
			if am == nil {
				continue
			}
			// Skip assistant messages with empty content (e.g. aborted
			// requests): Bedrock rejects empty content arrays.
			if len(am.Content) == 0 {
				continue
			}
			var contentBlocks []map[string]interface{}
			for _, c := range am.Content {
				switch b := c.(type) {
				case *src.TextContent:
					// Skip empty text blocks.
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					contentBlocks = append(contentBlocks, map[string]interface{}{"text": utils.SanitizeSurrogates(b.Text)})
				case *src.ToolCall:
					contentBlocks = append(contentBlocks, map[string]interface{}{
						"toolUse": map[string]interface{}{
							"toolUseId": b.ID,
							"name":      b.Name,
							"input":     b.Arguments,
						},
					})
				case *src.ThinkingContent:
					// Skip empty thinking blocks.
					if strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					if supportsBedrockThinkingSignature(model) {
						// Signatures arrive after thinking deltas; a replayed
						// reasoning block without one is rejected by Bedrock.
						// Fall back to plain text, matching Anthropic.
						if b.ThinkingSignature == "" || strings.TrimSpace(b.ThinkingSignature) == "" {
							contentBlocks = append(contentBlocks, map[string]interface{}{"text": utils.SanitizeSurrogates(b.Thinking)})
						} else {
							contentBlocks = append(contentBlocks, map[string]interface{}{
								"reasoningContent": map[string]interface{}{
									"reasoningText": map[string]interface{}{
										"text":      utils.SanitizeSurrogates(b.Thinking),
										"signature": b.ThinkingSignature,
									},
								},
							})
						}
					} else {
						// Other models reject reasoningContent.reasoningText.signature.
						contentBlocks = append(contentBlocks, map[string]interface{}{
							"reasoningContent": map[string]interface{}{
								"reasoningText": map[string]interface{}{
									"text": utils.SanitizeSurrogates(b.Thinking),
								},
							},
						})
					}
				default:
					return nil, fmt.Errorf("Unknown assistant content type")
				}
			}
			// Skip if all content blocks were filtered out.
			if len(contentBlocks) == 0 {
				continue
			}
			result = append(result, map[string]interface{}{
				"role":    "assistant",
				"content": contentBlocks,
			})

		case src.RoleTool:
			tr, ok := msg.(*src.ToolResultMessage[any])
			if !ok {
				continue
			}
			// Consecutive toolResult messages coalesce into ONE user message:
			// Bedrock requires all tool results in a single message.
			toolResults := []map[string]interface{}{}
			resultBlock, err := buildBedrockToolResultBlock(tr)
			if err != nil {
				return nil, err
			}
			toolResults = append(toolResults, resultBlock)

			j := i + 1
			for j < len(transformedMessages) && msgRole(transformedMessages[j]) == src.RoleTool {
				nextMsg, ok := transformedMessages[j].(*src.ToolResultMessage[any])
				if !ok {
					break
				}
				nextBlock, err := buildBedrockToolResultBlock(nextMsg)
				if err != nil {
					return nil, err
				}
				toolResults = append(toolResults, nextBlock)
				j++
			}
			i = j - 1

			result = append(result, map[string]interface{}{
				"role":    "user",
				"content": toolResults,
			})

		default:
			return nil, fmt.Errorf("Unknown message role")
		}
	}

	// Cache point on the last message when it is USER and caching is enabled.
	if cacheRetention != src.CacheRetentionNone && bedrockSupportsPromptCaching(model) && len(result) > 0 {
		lastMessage := result[len(result)-1]
		if lastMessage["role"] == "user" {
			if content, ok := lastMessage["content"].([]map[string]interface{}); ok {
				lastMessage["content"] = append(content, bedrockCachePointBlock(cacheRetention))
			}
		}
	}

	return result, nil
}

func buildBedrockToolResultBlock(tr *src.ToolResultMessage[any]) (map[string]interface{}, error) {
	var content []map[string]interface{}
	for _, c := range tr.Content {
		switch b := c.(type) {
		case *src.ImageContent:
			imageBlock, err := createBedrockImageBlock(b.MimeType, b.Data)
			if err != nil {
				return nil, err
			}
			content = append(content, map[string]interface{}{"image": imageBlock})
		default:
			if tc, ok := c.(*src.TextContent); ok {
				content = append(content, map[string]interface{}{"text": utils.SanitizeSurrogates(tc.Text)})
			}
		}
	}
	status := "success"
	if tr.IsError {
		status = "error"
	}
	return map[string]interface{}{
		"toolResult": map[string]interface{}{
			"toolUseId": tr.ToolCallID,
			"content":   content,
			"status":    status,
		},
	}, nil
}

func convertBedrockToolConfig(tools []src.Tool[any], toolChoice string, toolChoiceObj map[string]interface{}) map[string]interface{} {
	if len(tools) == 0 || toolChoice == "none" {
		return nil
	}

	bedrockTools := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		bedrockTools[i] = map[string]interface{}{
			"toolSpec": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": map[string]interface{}{
					"json": tool.Parameters,
				},
			},
		}
	}

	var bedrockToolChoice map[string]interface{}
	switch toolChoice {
	case "auto":
		bedrockToolChoice = map[string]interface{}{"auto": map[string]interface{}{}}
	case "any":
		bedrockToolChoice = map[string]interface{}{"any": map[string]interface{}{}}
	default:
		if toolChoiceObj != nil {
			if typ, ok := toolChoiceObj["type"].(string); ok && typ == "tool" {
				if name, ok := toolChoiceObj["name"].(string); ok {
					bedrockToolChoice = map[string]interface{}{
						"tool": map[string]interface{}{"name": name},
					}
				}
			}
		}
	}

	toolConfig := map[string]interface{}{"tools": bedrockTools}
	if bedrockToolChoice != nil {
		toolConfig["toolChoice"] = bedrockToolChoice
	}
	return toolConfig
}

// =============================================================================
// Thinking / additionalModelRequestFields
// =============================================================================

// bedrockModelMatchCandidates ports getModelMatchCandidates: lower-cased
// id/name plus the punctuation-normalized variants (application inference
// profile ARNs don't contain the model name, so both are checked).
func bedrockModelMatchCandidates(modelId, modelName string) []string {
	values := []string{modelId}
	if modelName != "" {
		values = append(values, modelName)
	}
	var candidates []string
	for _, value := range values {
		lower := strings.ToLower(value)
		candidates = append(candidates, lower)
		var b strings.Builder
		for _, r := range lower {
			if r == ' ' || r == '_' || r == '.' || r == ':' {
				b.WriteByte('-')
			} else {
				b.WriteRune(r)
			}
		}
		candidates = append(candidates, b.String())
	}
	return candidates
}

func bedrockCandidatesIncludeAny(candidates []string, needles ...string) bool {
	for _, candidate := range candidates {
		for _, needle := range needles {
			if strings.Contains(candidate, needle) {
				return true
			}
		}
	}
	return false
}

func bedrockSupportsAdaptiveThinking(model src.Model[src.Api]) bool {
	candidates := bedrockModelMatchCandidates(model.ID, model.Name)
	return bedrockCandidatesIncludeAny(candidates, "opus-4-6", "opus-4-7", "sonnet-4-6")
}

func bedrockSupportsNativeXhighEffort(model src.Model[src.Api]) bool {
	candidates := bedrockModelMatchCandidates(model.ID, model.Name)
	return bedrockCandidatesIncludeAny(candidates, "opus-4-7")
}

func isAnthropicClaudeModel(model src.Model[src.Api]) bool {
	id := strings.ToLower(model.ID)
	name := strings.ToLower(model.Name)
	return strings.Contains(id, "anthropic.claude") ||
		strings.Contains(id, "anthropic/claude") ||
		strings.Contains(name, "anthropic.claude") ||
		strings.Contains(name, "anthropic/claude") ||
		strings.Contains(name, "claude")
}

func bedrockSupportsPromptCaching(model src.Model[src.Api]) bool {
	candidates := bedrockModelMatchCandidates(model.ID, model.Name)
	if !bedrockCandidatesIncludeAny(candidates, "claude") {
		// Application inference profiles don't contain the model name in the
		// ARN — allow forcing cache points via the environment.
		if os.Getenv("AWS_BEDROCK_FORCE_CACHE") == "1" {
			return true
		}
		return false
	}
	// Claude 4.x models.
	if bedrockCandidatesIncludeAny(candidates, "-4-") {
		return true
	}
	// Claude 3.7 Sonnet.
	if bedrockCandidatesIncludeAny(candidates, "claude-3-7-sonnet") {
		return true
	}
	// Claude 3.5 Haiku.
	if bedrockCandidatesIncludeAny(candidates, "claude-3-5-haiku") {
		return true
	}
	return false
}

func supportsBedrockThinkingSignature(model src.Model[src.Api]) bool {
	return isAnthropicClaudeModel(model)
}

func isGovCloudBedrockTarget(model src.Model[src.Api], options *AmazonBedrockOptions) bool {
	region := ""
	if options != nil {
		region = options.Region
	}
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if strings.HasPrefix(strings.ToLower(region), "us-gov-") {
		return true
	}
	modelId := strings.ToLower(model.ID)
	return strings.HasPrefix(modelId, "us-gov.") || strings.HasPrefix(modelId, "arn:aws-us-gov:")
}

// mapBedrockThinkingLevelToEffort ports mapThinkingLevelToEffort.
func mapBedrockThinkingLevelToEffort(model src.Model[src.Api], level src.ThinkingLevel) string {
	if level == src.XHigh && bedrockSupportsNativeXhighEffort(model) {
		return "xhigh"
	}

	if level != "" && model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[string(level)]; ok && mapped != nil {
			return string(*mapped)
		}
	}

	switch level {
	case src.Minimal, src.Low:
		return "low"
	case src.Medium:
		return "medium"
	case src.High:
		return "high"
	default:
		return "high"
	}
}

func buildBedrockAdditionalModelRequestFields(model src.Model[src.Api], options *AmazonBedrockOptions) map[string]interface{} {
	if options == nil || options.Reasoning == "" || !model.Reasoning {
		return nil
	}

	if !isAnthropicClaudeModel(model) {
		return nil
	}

	// GovCloud Bedrock rejects the Claude thinking.display field.
	display := ""
	if options.ThinkingDisplay != "" {
		display = options.ThinkingDisplay
	} else {
		display = "summarized"
	}
	if isGovCloudBedrockTarget(model, options) {
		display = ""
	}

	var result map[string]interface{}

	if bedrockSupportsAdaptiveThinking(model) {
		thinking := map[string]interface{}{"type": "adaptive"}
		if display != "" {
			thinking["display"] = display
		}
		result = map[string]interface{}{
			"thinking":     thinking,
			"output_config": map[string]interface{}{
				"effort": mapBedrockThinkingLevelToEffort(model, options.Reasoning),
			},
		}
	} else {
		// Default budgets; Claude doesn't support xhigh, clamp to high.
		defaultBudgets := map[src.ThinkingLevel]int{
			src.Minimal: 1024,
			src.Low:     2048,
			src.Medium:  8192,
			src.High:    16384,
			src.XHigh:   16384,
		}
		level := options.Reasoning
		if level == src.XHigh {
			level = src.High
		}
		budget := 0
		if options.ThinkingBudgets != nil {
			switch level {
			case src.Minimal:
				if options.ThinkingBudgets.Minimal != nil {
					budget = *options.ThinkingBudgets.Minimal
				}
			case src.Low:
				if options.ThinkingBudgets.Low != nil {
					budget = *options.ThinkingBudgets.Low
				}
			case src.Medium:
				if options.ThinkingBudgets.Medium != nil {
					budget = *options.ThinkingBudgets.Medium
				}
			default:
				if options.ThinkingBudgets.High != nil {
					budget = *options.ThinkingBudgets.High
				}
			}
		}
		if budget == 0 {
			// Default budgets are indexed by the ORIGINAL reasoning level.
			if def, ok := defaultBudgets[options.Reasoning]; ok {
				budget = def
			} else {
				budget = 16384
			}
		}
		thinking := map[string]interface{}{
			"type":         "enabled",
			"budget_tokens": budget,
		}
		if display != "" {
			thinking["display"] = display
		}
		result = map[string]interface{}{"thinking": thinking}
	}

	if !bedrockSupportsAdaptiveThinking(model) {
		interleaved := true
		if options.InterleavedThinking != nil {
			interleaved = *options.InterleavedThinking
		}
		if interleaved {
			result["anthropic_beta"] = []string{amazonBedrockInterleavedBeta}
		}
	}

	return result
}

// =============================================================================
// Images
// =============================================================================

func createBedrockImageBlock(mimeType string, data string) (map[string]interface{}, error) {
	var format string
	switch mimeType {
	case "image/jpeg", "image/jpg":
		format = "jpeg"
	case "image/png":
		format = "png"
	case "image/gif":
		format = "gif"
	case "image/webp":
		format = "webp"
	default:
		return nil, fmt.Errorf("Unknown image type: %s", mimeType)
	}

	// The reference decodes the base64 data (atob) and the SDK re-encodes the
	// bytes for the wire — decode + re-encode for parity and validation.
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("invalid image base64 data: %v", err)
	}

	return map[string]interface{}{
		"format": format,
		"source": map[string]interface{}{
			"bytes": base64.StdEncoding.EncodeToString(raw),
		},
	}, nil
}

// =============================================================================
// Stream processing
// =============================================================================

// processAmazonBedrockStream decodes the binary AWS eventstream frames and
// dispatches the converse-stream events (amazon-bedrock.ts:226-253,
// 355-477). The "start" event is pushed when messageStart arrives, matching
// the reference (not before the request).
func processAmazonBedrockStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], ctx gocontext.Context) error {
	blockByIndex := make(map[int]*bedrockBlockState)

	syncBlock := func(state *bedrockBlockState) {
		switch b := output.Content[state.ContentIdx].(type) {
		case *src.TextContent:
			b.Text = state.Text
		case *src.ThinkingContent:
			b.Thinking = state.Thinking
			b.ThinkingSignature = state.Signature
		case *src.ToolCall:
			b.Arguments = parseStreamingJson(state.PartialJSON)
		}
	}

	started := false

	err := decodeAmazonEventStream(body, func(headers map[string]string, frameBody []byte) error {
		var event map[string]interface{}
		if err := json.Unmarshal(frameBody, &event); err != nil {
			return fmt.Errorf("Could not parse Bedrock eventstream payload: %v", err)
		}

		if item, ok := event["messageStart"].(map[string]interface{}); ok {
			if role, _ := item["role"].(string); role != "assistant" {
				return fmt.Errorf("Unexpected assistant message start but got user message start instead")
			}
			if !started {
				started = true
				ch <- src.AssistantMessageEvent{Type: "start", Partial: output}
			}
			return nil
		}

		if item, ok := event["contentBlockStart"].(map[string]interface{}); ok {
			index := int(getFloatValue(item, "contentBlockIndex"))
			if start, ok := item["start"].(map[string]interface{}); ok {
				if toolUse, ok := start["toolUse"].(map[string]interface{}); ok {
					state := &bedrockBlockState{Index: index, Type: "toolCall"}
					if id, ok := toolUse["toolUseId"].(string); ok {
						state.ToolUseID = id
					}
					if name, ok := toolUse["name"].(string); ok {
						state.Name = name
					}
					output.Content = append(output.Content, bedrockStateToContent(state))
					state.ContentIdx = len(output.Content) - 1
					blockByIndex[state.Index] = state
					idx := state.ContentIdx
					ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &idx, Partial: output}
				}
			}
			return nil
		}

		if item, ok := event["contentBlockDelta"].(map[string]interface{}); ok {
			contentBlockIndex := int(getFloatValue(item, "contentBlockIndex"))
			delta, _ := item["delta"].(map[string]interface{})
			if delta == nil {
				return nil
			}

			// Locate the block for this contentBlockIndex (findIndex parity).
			state := blockByIndex[contentBlockIndex]

			if text, ok := delta["text"].(string); ok {
				// Text blocks have no contentBlockStart: create one implicitly.
				if state == nil {
					newState := &bedrockBlockState{Index: contentBlockIndex, Type: "text"}
					output.Content = append(output.Content, bedrockStateToContent(newState))
					newState.ContentIdx = len(output.Content) - 1
					blockByIndex[contentBlockIndex] = newState
					state = newState
					idx := state.ContentIdx
					ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}
				}
				if state.Type == "text" {
					state.Text += text
					syncBlock(state)
					idx := state.ContentIdx
					ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &idx, Delta: text, Partial: output}
				}
				return nil
			}

			if toolUse, ok := delta["toolUse"].(map[string]interface{}); ok && state != nil && state.Type == "toolCall" {
				input, _ := toolUse["input"].(string)
				state.PartialJSON += input
				syncBlock(state)
				idx := state.ContentIdx
				ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: &idx, Delta: input, Partial: output}
				return nil
			}

			if reasoning, ok := delta["reasoningContent"].(map[string]interface{}); ok {
				thinkingState := state
				if thinkingState == nil {
					newState := &bedrockBlockState{Index: contentBlockIndex, Type: "thinking"}
					output.Content = append(output.Content, bedrockStateToContent(newState))
					newState.ContentIdx = len(output.Content) - 1
					blockByIndex[contentBlockIndex] = newState
					thinkingState = newState
					idx := newState.ContentIdx
					ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}
				}

				if thinkingState.Type == "thinking" {
					if text, ok := reasoning["text"].(string); ok && text != "" {
						thinkingState.Thinking += text
						syncBlock(thinkingState)
						idx := thinkingState.ContentIdx
						ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: &idx, Delta: text, Partial: output}
					}
					if signature, ok := reasoning["signature"].(string); ok && signature != "" {
						// Signature deltas accumulate silently (no event).
						thinkingState.Signature += signature
						syncBlock(thinkingState)
					}
				}
				return nil
			}

			return nil
		}

		if item, ok := event["contentBlockStop"].(map[string]interface{}); ok {
			contentBlockIndex := int(getFloatValue(item, "contentBlockIndex"))
			state, ok := blockByIndex[contentBlockIndex]
			if !ok || state == nil {
				return nil
			}
			// Delete the index scratch (findIndex parity: stopped blocks are
			// no longer locatable).
			delete(blockByIndex, contentBlockIndex)
			idx := state.ContentIdx

			switch state.Type {
			case "text":
				syncBlock(state)
				ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: &idx, Content: state.Text, Partial: output}
			case "thinking":
				syncBlock(state)
				ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: &idx, Content: state.Thinking, Partial: output}
			case "toolCall":
				// Finalize in-place: re-parse the accumulated buffer and drop
				// the scratch state (contentBlockStop handler parity).
				tc, _ := output.Content[state.ContentIdx].(*src.ToolCall)
				if tc != nil {
					tc.Arguments = parseStreamingJson(state.PartialJSON)
				}
				state.PartialJSON = ""
				ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: &idx, ToolCall: tc, Partial: output}
			}
			return nil
		}

		if item, ok := event["messageStop"].(map[string]interface{}); ok {
			reason, _ := item["stopReason"].(string)
			output.StopReason = mapAmazonBedrockStopReason(reason)
			return nil
		}

		if item, ok := event["metadata"].(map[string]interface{}); ok {
			if usage, ok := item["usage"].(map[string]interface{}); ok {
				output.Usage.Input = int(getFloatValue(usage, "inputTokens"))
				output.Usage.Output = int(getFloatValue(usage, "outputTokens"))
				output.Usage.CacheRead = int(getFloatValue(usage, "cacheReadInputTokens"))
				output.Usage.CacheWrite = int(getFloatValue(usage, "cacheWriteInputTokens"))
				output.Usage.TotalTokens = int(getFloatValue(usage, "totalTokens"))
				if output.Usage.TotalTokens == 0 {
					output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output
				}
				output.Usage.Cost = usageCostFromModelCost(src.CalculateCost(&model, &output.Usage))
			}
			return nil
		}

		// Stream exception events (amazon-bedrock.ts:242-252).
		for _, exceptionName := range []string{
			"internalServerException", "modelStreamErrorException", "validationException",
			"throttlingException", "serviceUnavailableException",
		} {
			if item, ok := event[exceptionName].(map[string]interface{}); ok {
				message := ""
				if m, ok := item["message"].(string); ok {
					message = m
				}
				// Exception names arrive lower-case in the JSON; the prefix
				// table is keyed by the PascalCase SDK name.
				pascal := strings.ToUpper(exceptionName[:1]) + exceptionName[1:]
				return fmt.Errorf("%s", bedrockFormatError(pascal, message))
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	_ = started
	_ = ctx
	return nil
}

func mapAmazonBedrockStopReason(reason string) src.StopReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return src.StopReasonStop
	case "max_tokens", "model_context_window_exceeded":
		return src.StopReasonLength
	case "tool_use":
		return src.StopReasonToolUse
	default:
		return src.StopReasonError
	}
}
