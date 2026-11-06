package providers

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	src "ycode/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

// Port of google-vertex.ts. The reference delegates auth and transport to the
// @google/genai SDK; this port implements the same REST wire contract:
//
//	express:  https://aiplatform.googleapis.com/v1/publishers/google/models/{m}:streamGenerateContent?alt=sse
//	          (x-goog-api-key header)
//	service:  https://{location}-aiplatform.googleapis.com/v1/projects/{p}/locations/{l}/publishers/google/models/{m}:streamGenerateContent?alt=sse
//	          (Bearer token from a service account or ADC credentials)
const (
	// gcpVertexCredentialsMarker is a sentinel some UIs put in the key field
	// when credentials live elsewhere (google-vertex.ts:50).
	gcpVertexCredentialsMarker = "gcp-vertex-credentials"

	vertexAPIVersion        = "v1"
	cloudPlatformScope      = "https://www.googleapis.com/auth/cloud-platform"
	defaultVertexHost       = "https://aiplatform.googleapis.com"
	defaultVertexTokenURL   = "https://oauth2.googleapis.com/token"
	vertexExpressPublishers = "publishers/google/"
)

// =============================================================================
// Types
// =============================================================================

// GoogleVertexOptions mirrors GoogleVertexOptions in google-vertex.ts plus
// the Go Context seam. Credentials are read from
// GOOGLE_APPLICATION_CREDENTIALS / ADC like the SDK does.
type GoogleVertexOptions struct {
	ProjectID   string
	Location    string
	APIKey      string
	MaxTokens   *int
	Temperature *float64
	ToolChoice  *string
	Thinking    *GoogleThinkingConfig
	Headers     map[string]string
	Context     context.Context
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// isPlaceholderApiKey mirrors google-vertex.ts isPlaceholderApiKey:
// "<placeholder>"-style strings are rejected.
func isPlaceholderApiKey(key string) bool {
	trimmed := strings.TrimSpace(key)
	return strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && len(trimmed) >= 2
}

// resolveVertexApiKey mirrors resolveApiKey: options → env, with the
// credentials marker and placeholder strings rejected.
func resolveVertexApiKey(options *GoogleVertexOptions) string {
	key := ""
	if options != nil {
		key = strings.TrimSpace(options.APIKey)
	}
	if key == "" {
		key = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_API_KEY"))
	}
	if key == "" || key == gcpVertexCredentialsMarker || isPlaceholderApiKey(key) {
		return ""
	}
	return key
}

func vertexContext(options *GoogleVertexOptions) context.Context {
	if options != nil {
		return options.Context
	}
	return nil
}

// =============================================================================
// Streaming
// =============================================================================

// StreamGoogleVertex creates an async streaming function for Google Vertex AI.
func StreamGoogleVertex(model src.Model[src.Api], context src.ChatContext, options *GoogleVertexOptions) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			output := &src.AssistantMessage{
				Role:       src.RoleAssistant,
				Content:    []src.ContentBlock{},
				Api:        model.Api,
				Provider:   model.Provider,
				Model:      model.ID,
				Usage:      src.Usage{Cost: src.UsageCost{}},
				StopReason: src.StopReasonStop,
			}

			handleError := func(err error) {
				if ctx := vertexContext(options); ctx != nil && ctx.Err() != nil {
					output.StopReason = src.StopReasonAborted
				} else {
					output.StopReason = src.StopReasonError
				}
				output.ErrorMessage = err.Error()
				ch <- src.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
			}

			params := buildGoogleVertexParams(model, context, options)
			if options != nil && options.OnPayload != nil {
				nextParams, e := options.OnPayload(params, model)
				if e != nil {
					handleError(e)
					return
				}
				if nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doGoogleVertexRequest(model, params, options)
			if err != nil {
				handleError(err)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status":  resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				resp.Body.Close()
				handleError(fmt.Errorf("Vertex AI API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(excerpt))))
				return
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// No signal support beyond request-bound Context (P15), so a
			// mid-stream read error is an error, never an abort.
			if err := processGoogleStream(resp.Body, ch, output, model); err != nil {
				handleError(err)
				return
			}
			resp.Body.Close()

			if ctx := vertexContext(options); ctx != nil && ctx.Err() != nil {
				handleError(fmt.Errorf("Request was aborted"))
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleError(fmt.Errorf("An unknown error occurred"))
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// =============================================================================
// Request handling
// =============================================================================

// resolveVertexProject mirrors resolveProject: options →
// GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT → error.
func resolveVertexProject(options *GoogleVertexOptions) (string, error) {
	project := ""
	if options != nil && options.ProjectID != "" {
		project = options.ProjectID
	}
	if project == "" {
		project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if project == "" {
		project = os.Getenv("GCLOUD_PROJECT")
	}
	if project == "" {
		return "", fmt.Errorf("Vertex AI requires a project ID. Set GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT or pass project in options.")
	}
	return project, nil
}

// resolveVertexLocation mirrors resolveLocation: options →
// GOOGLE_CLOUD_LOCATION → error.
func resolveVertexLocation(options *GoogleVertexOptions) (string, error) {
	location := ""
	if options != nil && options.Location != "" {
		location = options.Location
	}
	if location == "" {
		location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	}
	if location == "" {
		return "", fmt.Errorf("Vertex AI requires a location. Set GOOGLE_CLOUD_LOCATION or pass location in options.")
	}
	return location, nil
}

// buildGoogleVertexEndpoint assembles the streaming URL. Express mode (API
// key) targets the global aiplatform host; service mode targets the regional
// host with the project/location resource path. A custom base replaces only
// the host; a "{location}" template base is ignored (google-vertex.ts
// resolveCustomBaseUrl).
func buildGoogleVertexEndpoint(model src.Model[src.Api], options *GoogleVertexOptions, apiKey, project, location string) (string, error) {
	customBase := strings.TrimSpace(model.BaseURL)
	useCustom := customBase != "" && !strings.Contains(customBase, "{location}")

	resourcePath := vertexExpressPublishers + googleModelPath(model.ID)
	if apiKey == "" {
		host := fmt.Sprintf("https://%s-aiplatform.googleapis.com", location)
		if useCustom {
			host = strings.TrimRight(customBase, "/")
		}
		return fmt.Sprintf("%s/%s/projects/%s/locations/%s/%s:streamGenerateContent?alt=sse",
			host, vertexAPIVersion, project, location, resourcePath), nil
	}

	host := defaultVertexHost
	if useCustom {
		host = strings.TrimRight(customBase, "/")
	}
	return fmt.Sprintf("%s/%s/%s:streamGenerateContent?alt=sse", host, vertexAPIVersion, resourcePath), nil
}

func doGoogleVertexRequest(model src.Model[src.Api], params interface{}, options *GoogleVertexOptions) (*http.Response, error) {
	apiKey := resolveVertexApiKey(options)

	project := ""
	location := ""
	if apiKey == "" {
		// Service/ADC mode needs project + location.
		var err error
		if project, err = resolveVertexProject(options); err != nil {
			return nil, err
		}
		if location, err = resolveVertexLocation(options); err != nil {
			return nil, err
		}
	}

	endpoint, err := buildGoogleVertexEndpoint(model, options, apiKey, project, location)
	if err != nil {
		return nil, err
	}

	body, err := marshalGoogleWireBody(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if ctx := vertexContext(options); ctx != nil {
		req = req.WithContext(ctx)
	}

	req.Header.Set("Content-Type", "application/json")

	if apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	} else {
		token, err := getVertexAccessToken(vertexContext(options))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{}
	return client.Do(req)
}

func handleGoogleVertexError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
	handleGoogleError(ch, output, err, isAborted)
}

// =============================================================================
// Parameter building
// =============================================================================

func buildGoogleVertexParams(model src.Model[src.Api], context src.ChatContext, options *GoogleVertexOptions) interface{} {
	// Vertex uses the same builder as the Gemini API; the only reference
	// divergence is the disabled-thinking config (no Gemma 4 branch).
	return buildGoogleParams(model, context, &GoogleOptions{
		MaxTokens:   options.MaxTokens,
		Temperature: options.Temperature,
		ToolChoice:  options.ToolChoice,
		Thinking:    options.Thinking,
		Headers:     options.Headers,
		OnPayload:   options.OnPayload,
		OnResponse:  options.OnResponse,
	})
}

// =============================================================================
// Authentication (replaces the SDK's google-auth layer)
// =============================================================================

type vertexCachedToken struct {
	token     string
	expiresAt time.Time
}

var (
	vertexTokenMu       sync.Mutex
	vertexServiceToken  vertexCachedToken
	vertexServiceKeyID  string
	vertexADCAdapter    vertexCachedToken
	vertexADCIdentityID string
)

// credentialsFilePath returns GOOGLE_APPLICATION_CREDENTIALS or the gcloud
// ADC default path when that file exists.
func credentialsFilePath() string {
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	adc := filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	if _, err := os.Stat(adc); err == nil {
		return adc
	}
	return ""
}

// getVertexAccessToken returns a cached or freshly-minted Bearer token for
// Vertex AI, from either a service-account key (JWT assertion → token
// exchange) or ADC authorized-user credentials (refresh grant).
func getVertexAccessToken(ctx context.Context) (string, error) {
	path := credentialsFilePath()
	if path == "" {
		return "", fmt.Errorf("Vertex AI authentication required: set GOOGLE_CLOUD_API_KEY for express mode, or GOOGLE_APPLICATION_CREDENTIALS (service account or gcloud ADC credentials).")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read Vertex AI credentials from %s: %v", path, err)
	}

	var fields map[string]interface{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", fmt.Errorf("failed to parse Vertex AI credentials from %s: %v", path, err)
	}

	if _, hasRefresh := fields["refresh_token"]; hasRefresh {
		return getVertexADCAccessToken(ctx, fields)
	}
	if _, hasKey := fields["private_key"]; hasKey {
		return getVertexServiceAccountToken(ctx, fields)
	}
	return "", fmt.Errorf("unsupported Vertex AI credential type in %s (expected a service account key or gcloud ADC credentials)", path)
}

// getVertexServiceAccountToken signs a JWT with the service account's
// private key and exchanges it at the token endpoint.
func getVertexServiceAccountToken(ctx context.Context, fields map[string]interface{}) (string, error) {
	clientEmail, _ := fields["client_email"].(string)
	privateKeyPEM, _ := fields["private_key"].(string)
	tokenURL, _ := fields["token_uri"].(string)
	if tokenURL == "" {
		tokenURL = defaultVertexTokenURL
	}
	if clientEmail == "" || privateKeyPEM == "" {
		return "", fmt.Errorf("service account credentials missing client_email or private_key")
	}

	vertexTokenMu.Lock()
	if vertexServiceKeyID == clientEmail && time.Now().Before(vertexServiceToken.expiresAt) {
		token := vertexServiceToken.token
		vertexTokenMu.Unlock()
		return token, nil
	}
	vertexTokenMu.Unlock()

	token, expiresAt, err := exchangeVertexJWT(ctx, clientEmail, privateKeyPEM, tokenURL)
	if err != nil {
		return "", err
	}

	vertexTokenMu.Lock()
	vertexServiceKeyID = clientEmail
	vertexServiceToken = vertexCachedToken{token: token, expiresAt: expiresAt}
	vertexTokenMu.Unlock()
	return token, nil
}

// exchangeVertexJWT builds + signs the RS256 JWT assertion and posts it to
// the token endpoint.
func exchangeVertexJWT(ctx context.Context, clientEmail, privateKeyPEM, tokenURL string) (string, time.Time, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", time.Time{}, fmt.Errorf("service account private_key is not PEM-encoded")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if key1, err1 := x509.ParsePKCS1PrivateKey(block.Bytes); err1 == nil {
			key = key1
		} else {
			return "", time.Time{}, fmt.Errorf("unsupported service account private key format: %v", err)
		}
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", time.Time{}, fmt.Errorf("service account private key is not RSA")
	}

	now := time.Now()
	header, err := json.Marshal(map[string]interface{}{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", time.Time{}, err
	}
	claims, err := json.Marshal(map[string]interface{}{
		"iss":   clientEmail,
		"scope": cloudPlatformScope,
		"aud":   tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}

	b64 := base64.RawURLEncoding.EncodeToString
	signingInput := b64(header) + "." + b64(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", time.Time{}, err
	}
	assertion := signingInput + "." + b64(signature)

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	return postVertexTokenRequest(ctx, tokenURL, form)
}

// getVertexADCAccessToken refreshes ADC authorized-user credentials.
func getVertexADCAccessToken(ctx context.Context, fields map[string]interface{}) (string, error) {
	clientID, _ := fields["client_id"].(string)
	clientSecret, _ := fields["client_secret"].(string)
	refreshToken, _ := fields["refresh_token"].(string)
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return "", fmt.Errorf("ADC credentials missing client_id, client_secret or refresh_token")
	}
	// Real ADC files carry no token_uri (the installed-app client posts to
	// Google's well-known endpoint); honor one when present for testability.
	tokenURL, _ := fields["token_uri"].(string)
	if tokenURL == "" {
		tokenURL = defaultVertexTokenURL
	}

	vertexTokenMu.Lock()
	if vertexADCIdentityID == refreshToken && time.Now().Before(vertexADCAdapter.expiresAt) {
		token := vertexADCAdapter.token
		vertexTokenMu.Unlock()
		return token, nil
	}
	vertexTokenMu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("refresh_token", refreshToken)

	token, expiresAt, err := postVertexTokenRequest(ctx, tokenURL, form)
	if err != nil {
		return "", err
	}

	vertexTokenMu.Lock()
	vertexADCIdentityID = refreshToken
	vertexADCAdapter = vertexCachedToken{token: token, expiresAt: expiresAt}
	vertexTokenMu.Unlock()
	return token, nil
}

// postVertexTokenRequest POSTs a token request and returns the access token
// plus its expiry (expiry − 60s safety margin).
func postVertexTokenRequest(ctx context.Context, tokenURL string, form url.Values) (string, time.Time, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Short per-request deadline for the token exchange (NOT an overall
	// client timeout — the streaming client must stay unbounded).
	tokenCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(tokenCtx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("Vertex AI token request failed: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("Vertex AI token request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse Vertex AI token response: %v", err)
	}
	if tokenResp.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("Vertex AI token response carried no access_token")
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)
	if tokenResp.ExpiresIn <= 0 {
		expiresAt = time.Now().Add(5 * time.Minute)
	}
	return tokenResp.AccessToken, expiresAt, nil
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleGoogleVertex provides a simplified streaming interface for
// Google Vertex AI (google-vertex.ts streamSimpleGoogleVertex — no explicit
// key check; credentials resolve at request time).
func StreamSimpleGoogleVertex(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	base := BuildBaseOptions(model, options, "")
	if options == nil || options.Reasoning == "" {
		opts := googleOptionsFromStreamOptions(base)
		opts.Thinking = &GoogleThinkingConfig{Enabled: false}
		return StreamGoogleVertex(model, context, vertexOptionsFromStreamOptions(base, opts))
	}

	clamped := src.ClampThinkingLevel(&model, options.Reasoning)
	effort := string(clamped)
	if clamped == src.Off {
		effort = "high"
	}

	modelID := model.ID
	if isGemini3ProModel(modelID) || isGemini3FlashModel(modelID) {
		level := getVertexGemini3ThinkingLevel(effort, modelID)
		opts := googleOptionsFromStreamOptions(base)
		opts.Thinking = &GoogleThinkingConfig{Enabled: true, Level: &level}
		return StreamGoogleVertex(model, context, vertexOptionsFromStreamOptions(base, opts))
	}

	var customBudgets *src.ThinkingBudgets
	if options != nil {
		customBudgets = options.ThinkingBudgets
	}
	budget := getVertexGoogleBudget(modelID, effort, customBudgets)
	opts := googleOptionsFromStreamOptions(base)
	opts.Thinking = &GoogleThinkingConfig{Enabled: true, BudgetTokens: &budget}
	return StreamGoogleVertex(model, context, vertexOptionsFromStreamOptions(base, opts))
}

func vertexOptionsFromStreamOptions(base src.StreamOptions, g *GoogleOptions) *GoogleVertexOptions {
	return &GoogleVertexOptions{
		MaxTokens:   g.MaxTokens,
		Temperature: g.Temperature,
		Thinking:    g.Thinking,
		Headers:     g.Headers,
		Context:     base.Context,
	}
}

// getVertexGemini3ThinkingLevel ports the vertex-local getGemini3ThinkingLevel
// (no Gemma 4 branch — that is google.ts only).
func getVertexGemini3ThinkingLevel(effort string, modelID string) string {
	if isGemini3ProModel(modelID) {
		switch effort {
		case "minimal", "low":
			return "LOW"
		case "medium", "high":
			return "HIGH"
		}
	}
	switch effort {
	case "minimal":
		return "MINIMAL"
	case "low":
		return "LOW"
	case "medium":
		return "MEDIUM"
	case "high":
		return "HIGH"
	}
	return "MEDIUM"
}

// getVertexGoogleBudget ports the vertex-local getGoogleBudget (no
// flash-lite branch — that is google.ts only).
func getVertexGoogleBudget(modelID string, effort string, customBudgets *src.ThinkingBudgets) int {
	if customBudgets != nil {
		switch effort {
		case "minimal":
			if customBudgets.Minimal != nil {
				return *customBudgets.Minimal
			}
		case "low":
			if customBudgets.Low != nil {
				return *customBudgets.Low
			}
		case "medium":
			if customBudgets.Medium != nil {
				return *customBudgets.Medium
			}
		case "high":
			if customBudgets.High != nil {
				return *customBudgets.High
			}
		}
	}

	lower := strings.ToLower(modelID)
	type budgetTable struct {
		minimal, low, medium, high int
	}
	var table budgetTable
	switch {
	case strings.Contains(lower, "2.5-pro"):
		table = budgetTable{minimal: 128, low: 2048, medium: 8192, high: 32768}
	case strings.Contains(lower, "2.5-flash"):
		table = budgetTable{minimal: 128, low: 2048, medium: 8192, high: 24576}
	default:
		return -1
	}
	switch effort {
	case "minimal":
		return table.minimal
	case "low":
		return table.low
	case "medium":
		return table.medium
	case "high":
		return table.high
	}
	return -1
}
