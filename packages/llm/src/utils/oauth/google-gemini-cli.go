package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// Decoded from base64
	geminiCliClientID     = "681255809395-oo8ft2opdrnp9e3aqf6av3shmdib135j.apps.googleusercontent.com"
	geminiCliClientSecret = "GOGCSPX-4uHgPm-1o7Sk-geV6Cu5clXFsxl="
	geminiCliRedirectURI  = "http://localhost:8085/oauth2callback"
	geminiCliAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	geminiCliTokenURL     = "https://oauth2.googleapis.com/token"
	geminiCliEndpoint     = "https://cloudcode-pa.googleapis.com"
	geminiCliCallbackPort = 8085
)

var geminiCliScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

type GeminiCliCredentials struct {
	OAuthCredentials
	ProjectID string `json:"project_id"`
}

const (
	tierFree     = "free-tier"
	tierLegacy   = "legacy-tier"
	tierStandard = "standard-tier"
)

func discoverGeminiCliProject(ctx context.Context, accessToken string, onProgress func(string)) (string, error) {
	envProjectID := ""
	// In Go, you'd use os.Getenv("GOOGLE_CLOUD_PROJECT") instead

	headers := map[string]string{
		"Authorization":     "Bearer " + accessToken,
		"Content-Type":      "application/json",
		"User-Agent":        "google-api-nodejs-client/9.15.1",
		"X-Goog-Api-Client": "gl-node/22.17.0",
	}

	onProgress("Checking for existing Cloud Code Assist project...")

	reqBody := map[string]any{
		"cloudaicompanionProject": envProjectID,
		"metadata": map[string]string{
			"ideType":    "IDE_UNSPECIFIED",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	}
	if envProjectID != "" {
		reqBody["metadata"].(map[string]string)["duetProject"] = envProjectID
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequestWithContext(ctx, "POST", geminiCliEndpoint+"/v1internal:loadCodeAssist", strings.NewReader(string(bodyBytes)))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var data loadCodeAssistPayload
	if resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(&data); err == nil {
			if data.CurrentTier != nil {
				if str, ok := data.CloudaicompanionProject.(string); ok && str != "" {
					return str, nil
				}
				if obj, ok := data.CloudaicompanionProject.(map[string]any); ok {
					if id, ok := obj["id"].(string); ok && id != "" {
						return id, nil
					}
				}
				if envProjectID != "" {
					return envProjectID, nil
				}
				return "", fmt.Errorf("this account requires GOOGLE_CLOUD_PROJECT environment variable")
			}
		}
	}

	tierID := tierFree
	if len(data.AllowedTiers) > 0 {
		for _, t := range data.AllowedTiers {
			if t.IsDefault {
				tierID = t.ID
				break
			}
		}
		if tierID == "" {
			tierID = data.AllowedTiers[0].ID
		}
	}

	if tierID != tierFree && envProjectID == "" {
		return "", fmt.Errorf("this account requires GOOGLE_CLOUD_PROJECT environment variable")
	}

	onProgress("Provisioning Cloud Code Assist project...")

	onboardBody := map[string]any{
		"tierId": tierID,
		"metadata": map[string]string{
			"ideType":    "IDE_UNSPECIFIED",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	}
	if tierID != tierFree && envProjectID != "" {
		onboardBody["cloudaicompanionProject"] = envProjectID
		onboardBody["metadata"].(map[string]string)["duetProject"] = envProjectID
	}
	bodyBytes, _ = json.Marshal(onboardBody)

	req, _ = http.NewRequestWithContext(ctx, "POST", geminiCliEndpoint+"/v1internal:onboardUser", strings.NewReader(string(bodyBytes)))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err = client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("onboardUser failed: %d: %s", resp.StatusCode, string(body))
	}

	var lro longRunningOperation
	if err := json.NewDecoder(resp.Body).Decode(&lro); err != nil {
		return "", err
	}

	for !lro.Done && lro.Name != "" {
		onProgress(fmt.Sprintf("Waiting for project provisioning (operation: %s)...", lro.Name))
		time.Sleep(5 * time.Second)

		req, _ = http.NewRequestWithContext(ctx, "GET", geminiCliEndpoint+"/v1internal/"+lro.Name, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			var newLro longRunningOperation
			if err := json.NewDecoder(resp.Body).Decode(&newLro); err == nil {
				lro = newLro
			}
		}
	}

	if lro.Response != nil && lro.Response.CloudaicompanionProject != nil && lro.Response.CloudaicompanionProject.ID != "" {
		return lro.Response.CloudaicompanionProject.ID, nil
	}

	if envProjectID != "" {
		return envProjectID, nil
	}

	return "", fmt.Errorf("could not discover or provision project, try setting GOOGLE_CLOUD_PROJECT")
}

func refreshGeminiCliToken(ctx context.Context, refreshToken string, projectID string) (*OAuthCredentials, error) {
	form := url.Values{}
	form.Set("client_id", geminiCliClientID)
	form.Set("client_secret", geminiCliClientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")

	req, _ := http.NewRequestWithContext(ctx, "POST", geminiCliTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini cli token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini cli token refresh failed: %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	refresh := data.RefreshToken
	if refresh == "" {
		refresh = refreshToken
	}

	return &OAuthCredentials{
		Refresh: refresh,
		Access:  data.AccessToken,
		Expires: time.Now().UnixMilli() + data.ExpiresIn*1000 - 5*60*1000,
		Extra: map[string]any{
			"project_id": projectID,
		},
	}, nil
}

func LoginGeminiCli(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}

	srv, err := startCallbackServer(ctx, geminiCliCallbackPort, "/oauth2callback", verifier)
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	defer srv.server.Close()

	params := url.Values{
		"client_id":             {geminiCliClientID},
		"response_type":         {"code"},
		"redirect_uri":          {geminiCliRedirectURI},
		"scope":                 {strings.Join(geminiCliScopes, " ")},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {verifier},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}

	callbacks.OnAuth(OAuthAuthInfo{
		URL:          fmt.Sprintf("%s?%s", geminiCliAuthURL, params.Encode()),
		Instructions: "Complete the sign-in in your browser.",
	})

	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Waiting for OAuth callback...")
	}

	result, err := srv.waitForCode()
	if err != nil {
		return nil, err
	}

	var code string
	if result != nil && result.Code != "" {
		if result.State != verifier {
			return nil, fmt.Errorf("OAuth state mismatch")
		}
		code = result.Code
	}

	if code == "" {
		if callbacks.OnPrompt == nil {
			return nil, fmt.Errorf("missing authorization code")
		}
		input, err := callbacks.OnPrompt(OAuthPrompt{
			Message:     "Paste the authorization code or full redirect URL:",
			Placeholder: geminiCliRedirectURI,
		})
		if err != nil {
			return nil, err
		}
		parsed := parseAuthorizationInput(input)
		if parsed == nil || parsed.Code == "" {
			return nil, fmt.Errorf("missing authorization code")
		}
		if parsed.State != "" && parsed.State != verifier {
			return nil, fmt.Errorf("OAuth state mismatch")
		}
		code = parsed.Code
	}

	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for tokens...")
	}

	form := url.Values{}
	form.Set("client_id", geminiCliClientID)
	form.Set("client_secret", geminiCliClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", geminiCliRedirectURI)
	form.Set("code_verifier", verifier)

	req, _ := http.NewRequestWithContext(ctx, "POST", geminiCliTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed: %d: %s", resp.StatusCode, string(body))
	}

	var tokenData struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenData); err != nil {
		return nil, err
	}

	if tokenData.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token received")
	}

	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Discovering project...")
	}

	projectID, err := discoverGeminiCliProject(ctx, tokenData.AccessToken, callbacks.OnProgress)
	if err != nil {
		return nil, fmt.Errorf("failed to discover project: %w", err)
	}

	return &OAuthCredentials{
		Refresh: tokenData.RefreshToken,
		Access:  tokenData.AccessToken,
		Expires: time.Now().UnixMilli() + tokenData.ExpiresIn*1000 - 5*60*1000,
		Extra: map[string]any{
			"project_id": projectID,
		},
	}, nil
}

func RefreshGeminiCliToken(ctx context.Context, credentials OAuthCredentials) (*OAuthCredentials, error) {
	projectID := ""
	if val, ok := credentials.Extra["project_id"].(string); ok {
		projectID = val
	}
	if projectID == "" {
		return nil, fmt.Errorf("gemini cli credentials missing project_id")
	}
	return refreshGeminiCliToken(ctx, credentials.Refresh, projectID)
}
