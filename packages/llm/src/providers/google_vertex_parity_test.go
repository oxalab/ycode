package providers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	src "ycode/packages/llm/src"
)

func vertexTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Api: src.Api("google-vertex"),
		Provider: src.Provider("google-vertex"), BaseURL: "https://{location}-aiplatform.googleapis.com",
		Reasoning: true, MaxTokens: 65536,
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

func clearVertexEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_CLOUD_API_KEY", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
}

func collectVertexStream(t *testing.T, m src.Model[src.Api], opts *GoogleVertexOptions) (*src.AssistantMessage, string) {
	t.Helper()
	if opts == nil {
		opts = &GoogleVertexOptions{}
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	var final *src.AssistantMessage
	errMsg := ""
	for ev := range StreamGoogleVertex(m, src.ChatContext{}, opts)() {
		if ev.Type == "error" && ev.Error != nil {
			final = ev.Error
			errMsg = ev.Error.ErrorMessage
		}
		if ev.Type == "done" {
			final = ev.Message
		}
	}
	return final, errMsg
}

func vertexSSEResponse() string {
	return "data: {\"responseId\":\"v1\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"vertex says hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":7,\"candidatesTokenCount\":3,\"totalTokenCount\":10}}\n\n"
}

// --- Express mode (API key) ---

func TestVertexExpressMode(t *testing.T) {
	clearVertexEnv(t)
	var requestPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path + "?" + r.URL.RawQuery
		gotKey = r.Header.Get("x-goog-api-key")
		_, _ = w.Write([]byte(vertexSSEResponse()))
	}))
	defer srv.Close()

	// Express mode needs neither project nor location.
	m := vertexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	final, errMsg := collectVertexStream(t, m, &GoogleVertexOptions{APIKey: "vkey"})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if requestPath != "/v1/publishers/google/models/gemini-2.5-pro:streamGenerateContent?alt=sse" {
		t.Fatalf("path = %q", requestPath)
	}
	if gotKey != "vkey" {
		t.Fatalf("x-goog-api-key = %q", gotKey)
	}
	if final == nil || final.Usage.Input != 7 || final.Usage.Output != 3 {
		t.Fatalf("final usage = %+v", final.Usage)
	}
}

// The {location} catalog BaseURL template is ignored; service mode builds the
// regional resource path from the resolved location. A non-template custom
// base replaces the host.
func TestVertexLocationTemplateBase(t *testing.T) {
	// Catalog template base + no key: built from the resolved location.
	endpoint, err := buildGoogleVertexEndpoint(vertexTestModel(nil), nil, "", "proj-1", "us-central1")
	if err != nil {
		t.Fatalf("endpoint: %v", err)
	}
	if endpoint != "https://us-central1-aiplatform.googleapis.com/v1/projects/proj-1/locations/us-central1/publishers/google/models/gemini-2.5-pro:streamGenerateContent?alt=sse" {
		t.Fatalf("service endpoint = %q", endpoint)
	}

	// Custom base (no template) replaces the host only.
	m := vertexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = "https://private-endpoint.example.com" })
	endpoint, err = buildGoogleVertexEndpoint(m, nil, "", "proj-1", "us-central1")
	if err != nil {
		t.Fatalf("custom endpoint: %v", err)
	}
	if !strings.HasPrefix(endpoint, "https://private-endpoint.example.com/v1/projects/proj-1") {
		t.Fatalf("custom endpoint = %q", endpoint)
	}

	// Express mode: global host, no project path.
	endpoint, err = buildGoogleVertexEndpoint(vertexTestModel(nil), nil, "k", "", "")
	if err != nil {
		t.Fatalf("express endpoint: %v", err)
	}
	if endpoint != "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-pro:streamGenerateContent?alt=sse" {
		t.Fatalf("express endpoint = %q", endpoint)
	}
}

// --- Project / location resolution ---

func TestVertexProjectLocationErrors(t *testing.T) {
	clearVertexEnv(t)
	m := vertexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = "" })

	_, errMsg := collectVertexStream(t, m, &GoogleVertexOptions{})
	if !strings.Contains(errMsg, "Vertex AI requires a project ID. Set GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT or pass project in options.") {
		t.Fatalf("project errMsg = %q", errMsg)
	}

	opts := &GoogleVertexOptions{ProjectID: "p"}
	_, errMsg = collectVertexStream(t, m, opts)
	if !strings.Contains(errMsg, "Vertex AI requires a location. Set GOOGLE_CLOUD_LOCATION or pass location in options.") {
		t.Fatalf("location errMsg = %q", errMsg)
	}

	// Env fallbacks satisfy both.
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "europe-west4")
	if got, err := resolveVertexProject(nil); err != nil || got != "env-proj" {
		t.Fatalf("env project = %q err %v", got, err)
	}
	if got, err := resolveVertexLocation(nil); err != nil || got != "europe-west4" {
		t.Fatalf("env location = %q err %v", got, err)
	}
}

// --- Key resolution: marker + placeholders ---

func TestVertexKeyMarkerAndPlaceholder(t *testing.T) {
	if got := resolveVertexApiKey(&GoogleVertexOptions{APIKey: "gcp-vertex-credentials"}); got != "" {
		t.Fatalf("marker key = %q", got)
	}
	if got := resolveVertexApiKey(&GoogleVertexOptions{APIKey: "<authenticated>"}); got != "" {
		t.Fatalf("placeholder key = %q", got)
	}
	if got := resolveVertexApiKey(&GoogleVertexOptions{APIKey: " real-key "}); got != "real-key" {
		t.Fatalf("trimmed key = %q", got)
	}
	t.Setenv("GOOGLE_CLOUD_API_KEY", "env-key")
	if got := resolveVertexApiKey(nil); got != "env-key" {
		t.Fatalf("env key = %q", got)
	}
}

// --- Service account auth (JWT → token exchange, cached) ---

func TestVertexServiceAccountFlow(t *testing.T) {
	clearVertexEnv(t)

	// Generate a real RSA key for the service account.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	var tokenHits, streamHits int32
	var lastAuth string
	var assertionClaims map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "alt=sse") || r.Header.Get("x-goog-api-key") != "" {
			atomic.AddInt32(&streamHits, 1)
			lastAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(vertexSSEResponse()))
			return
		}
		// Token endpoint.
		atomic.AddInt32(&tokenHits, 1)
		body, _ := io.ReadAll(r.Body)
		form := strings.Split(string(body), "&")
		grant := ""
		assertion := ""
		for _, kv := range form {
			if strings.HasPrefix(kv, "grant_type=") {
				grant, _ = url.QueryUnescape(strings.TrimPrefix(kv, "grant_type="))
			}
			if strings.HasPrefix(kv, "assertion=") {
				assertion, _ = url.QueryUnescape(strings.TrimPrefix(kv, "assertion="))
			}
		}
		grantTypeSeen = grant
		if grant != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", grant)
		}
		// Decode the JWT claims segment (middle part).
		parts := strings.Split(assertion, ".")
		if len(parts) == 3 {
			if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
				_ = json.Unmarshal(payload, &assertionClaims)
			}
		}
		_, _ = w.Write([]byte(`{"access_token":"sa-token-1","expires_in":3600}`))
	}))
	defer srv.Close()

	creds := map[string]interface{}{
		"type":         "service_account",
		"client_email": "sa@test.iam.gserviceaccount.com",
		"private_key":  pemKey,
		"token_uri":    srv.URL,
	}
	credsPath := filepath.Join(t.TempDir(), "sa.json")
	raw, _ := json.Marshal(creds)
	if err := os.WriteFile(credsPath, raw, 0600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credsPath)

	// Reset caches so the test starts cold.
	vertexTokenMu.Lock()
	vertexServiceKeyID = ""
	vertexServiceToken = vertexCachedToken{}
	vertexADCIdentityID = ""
	vertexADCAdapter = vertexCachedToken{}
	vertexTokenMu.Unlock()

	m := vertexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	opts := &GoogleVertexOptions{ProjectID: "proj", Location: "us-central1"}
	final, errMsg := collectVertexStream(t, m, opts)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}

	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("token hits = %d, want 1", atomic.LoadInt32(&tokenHits))
	}
	if !strings.Contains(grantTypeSeen, "jwt-bearer") {
		t.Fatalf("grant seen = %q", grantTypeSeen)
	}
	if lastAuth != "Bearer sa-token-1" {
		t.Fatalf("Authorization = %q", lastAuth)
	}
	if assertionClaims["iss"] != "sa@test.iam.gserviceaccount.com" {
		t.Fatalf("JWT iss = %v", assertionClaims["iss"])
	}
	if assertionClaims["scope"] != cloudPlatformScope {
		t.Fatalf("JWT scope = %v", assertionClaims["scope"])
	}

	// Second request uses the cached token (no new token exchange).
	_, errMsg = collectVertexStream(t, m, opts)
	if errMsg != "" {
		t.Fatalf("stream2: %s", errMsg)
	}
	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("cached token must not re-exchange, token hits = %d", atomic.LoadInt32(&tokenHits))
	}
	if atomic.LoadInt32(&streamHits) != 2 {
		t.Fatalf("stream hits = %d, want 2", atomic.LoadInt32(&streamHits))
	}
	_ = final
}

var grantTypeSeen string

// --- ADC authorized-user flow ---

func TestVertexADCFlow(t *testing.T) {
	clearVertexEnv(t)

	var tokenHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "alt=sse") {
			_, _ = w.Write([]byte(vertexSSEResponse()))
			return
		}
		atomic.AddInt32(&tokenHits, 1)
		body, _ := io.ReadAll(r.Body)
		grantTypeSeen = string(body)
		_, _ = w.Write([]byte(`{"access_token":"adc-token-1","expires_in":3600}`))
	}))
	defer srv.Close()

	creds := map[string]interface{}{
		"type":          "authorized_user",
		"client_id":     "cid",
		"client_secret": "csec",
		"refresh_token": "rtok",
		"token_uri":     srv.URL, // real ADC files omit this; default is Google's endpoint
	}
	credsPath := filepath.Join(t.TempDir(), "adc.json")
	raw, _ := json.Marshal(creds)
	if err := os.WriteFile(credsPath, raw, 0600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credsPath)

	vertexTokenMu.Lock()
	vertexServiceKeyID = ""
	vertexServiceToken = vertexCachedToken{}
	vertexADCIdentityID = ""
	vertexADCAdapter = vertexCachedToken{}
	vertexTokenMu.Unlock()

	m := vertexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	opts := &GoogleVertexOptions{ProjectID: "proj", Location: "us-central1"}
	if _, errMsg := collectVertexStream(t, m, opts); errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("token hits = %d", atomic.LoadInt32(&tokenHits))
	}
	if !strings.Contains(grantTypeSeen, "grant_type=refresh_token") {
		t.Fatalf("grant body = %q", grantTypeSeen)
	}
}

// --- Simple wrapper ---

func TestVertexSimpleWrapper(t *testing.T) {
	clearVertexEnv(t)

	// NO key check on the vertex simple wrapper (TS parity): it proceeds and
	// fails later at project resolution when nothing is configured.
	errMsg := ""
	for ev := range StreamSimpleGoogleVertex(vertexTestModel(nil), src.ChatContext{}, &src.SimpleStreamOptions{})() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	if !strings.Contains(errMsg, "Vertex AI requires a project ID") {
		t.Fatalf("errMsg = %q", errMsg)
	}

	// Budget tables: vertex variant has NO flash-lite branch — "2.5-flash-lite"
	// matches the flash table (128).
	if got := getVertexGoogleBudget("gemini-2.5-pro", "high", nil); got != 32768 {
		t.Fatalf("pro high = %d", got)
	}
	if got := getVertexGoogleBudget("gemini-2.5-flash-lite", "minimal", nil); got != 128 {
		t.Fatalf("flash-lite minimal = %d, want 128 (vertex has no lite branch)", got)
	}
	if got := getVertexGoogleBudget("gemini-1.5-pro", "high", nil); got != -1 {
		t.Fatalf("unknown = %d", got)
	}

	// Level mapping: gemini-3 pro minimal → LOW; no gemma4 branch (falls to
	// the default switch → MINIMAL).
	if got := getVertexGemini3ThinkingLevel("minimal", "gemini-3-pro-preview"); got != "LOW" {
		t.Fatalf("pro minimal = %q", got)
	}
	if got := getVertexGemini3ThinkingLevel("minimal", "gemma-4-27b-it"); got != "MINIMAL" {
		t.Fatalf("gemma (default switch) = %q", got)
	}
}
