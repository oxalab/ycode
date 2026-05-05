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
	antigravityClientID     = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	antigravityClientSecret = "GOGCSPX-K58FWR486LdJO1mB8sXCz6qDAf="
	antigravityRedirectURI  = "http://localhost:51121/oauth-callback"
	antigravityAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	antigravityTokenURL     = "https://oauth2.googleapis.com/token"
	antigravityDefaultProject = "rising-fact-p41fc"
	antigravityCallbackPort = 51121
)

var antigravityScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
}

type AntigravityCredentials struct {
	OAuthCredentials
	ProjectID string `json:"project_id"`
}

type loadCodeAssistPayload struct {
	CloudaicompanionProject any     `json:"cloudaicompanionProject"`
	CurrentTier             *struct {
		ID string `json:"id"`
	} `json:"currentTier"`
	AllowedTiers []struct {
		ID        string `json:"id"`
		IsDefault bool   `json:"isDefault"`
	} `json:"allowedTiers"`
}

type longRunningOperation struct {
	Name     string `json:"name"`
	Done     bool   `json:"done"`
	Response *struct {
		CloudaicompanionProject *struct {
			ID string `json:"id"`
		} `json:"cloudaicompanionProject"`
	} `json:"response"`
}

func discoverAntigravityProject(ctx context.Context, accessToken string, onProgress func(string)) (string, error) {
	headers := map[string]string{
		"Authorization":     "Bearer " + accessToken,
		"Content-Type":      "application/json",
		"User-Agent":        "google-api-nodejs-client/9.15.1",
		"X-Goog-Api-Client": "google-cloud-sdk vscode_cloudshelleditor/0.1",
		"Client-Metadata": `{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`,
	}

	endpoints := []string{
		"https://cloudcode-pa.googleapis.com",
		"https://daily-cloudcode-pa.sandbox.googleapis.com",
	}

	onProgress("Checking for existing project...")

	for _, endpoint := range endpoints {
		reqBody := map[string]any{
			"metadata": map[string]string{
				"ideType":    "IDE_UNSPECIFIED",
				"platform":   "PLATFORM_UNSPECIFIED",
				"pluginType": "GEMINI",
			},
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1internal:loadCodeAssist", strings.NewReader(string(bodyBytes)))
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			var data loadCodeAssistPayload
			if err := json.NewDecoder(resp.Body).Decode(&data); err == nil {
				if str, ok := data.CloudaicompanionProject.(string); ok && str != "" {
					return str, nil
				}
				if obj, ok := data.CloudaicompanionProject.(map[string]any); ok {
					if id, ok := obj["id"].(string); ok && id != "" {
						return id, nil
					}
				}
			}
		}
	}

	onProgress("Using default project...")
	return antigravityDefaultProject, nil
}

func getAntigravityUserEmail(ctx context.Context, accessToken string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/oauth2/v1/userinfo?alt=json", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", nil
	}

	var data struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", nil
	}
	return data.Email, nil
}

func refreshAntigravityToken(ctx context.Context, refreshToken string, projectID string) (*OAuthCredentials, error) {
	form := url.Values{}
	form.Set("client_id", antigravityClientID)
	form.Set("client_secret", antigravityClientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")

	req, _ := http.NewRequestWithContext(ctx, "POST", antigravityTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("antigravity token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("antigravity token refresh failed: %d: %s", resp.StatusCode, string(body))
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

func LoginAntigravity(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}

	srv, err := startCallbackServer(ctx, antigravityCallbackPort, "/oauth-callback", verifier)
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	defer srv.server.Close()

	params := url.Values{
		"client_id":             {antigravityClientID},
		"response_type":         {"code"},
		"redirect_uri":          {antigravityRedirectURI},
		"scope":                 {strings.Join(antigravityScopes, " ")},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {verifier},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}

	callbacks.OnAuth(OAuthAuthInfo{
		URL:          fmt.Sprintf("%s?%s", antigravityAuthURL, params.Encode()),
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
			Placeholder: antigravityRedirectURI,
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
	form.Set("client_id", antigravityClientID)
	form.Set("client_secret", antigravityClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", antigravityRedirectURI)
	form.Set("code_verifier", verifier)

	req, _ := http.NewRequestWithContext(ctx, "POST", antigravityTokenURL, strings.NewReader(form.Encode()))
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
		callbacks.OnProgress("Getting user info...")
	}

	email, _ := getAntigravityUserEmail(ctx, tokenData.AccessToken)

	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Discovering project...")
	}

	projectID, err := discoverAntigravityProject(ctx, tokenData.AccessToken, callbacks.OnProgress)
	if err != nil {
		return nil, fmt.Errorf("failed to discover project: %w", err)
	}

	return &OAuthCredentials{
		Refresh: tokenData.RefreshToken,
		Access:  tokenData.AccessToken,
		Expires: time.Now().UnixMilli() + tokenData.ExpiresIn*1000 - 5*60*1000,
		Extra: map[string]any{
			"project_id": projectID,
			"email":      email,
		},
	}, nil
}

func RefreshAntigravityToken(ctx context.Context, credentials OAuthCredentials) (*OAuthCredentials, error) {
	projectID := ""
	if val, ok := credentials.Extra["project_id"].(string); ok {
		projectID = val
	}
	if projectID == "" {
		return nil, fmt.Errorf("antigravity credentials missing project_id")
	}
	return refreshAntigravityToken(ctx, credentials.Refresh, projectID)
}
