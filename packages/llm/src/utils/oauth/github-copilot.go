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
	// Decoded from base64: "IV1.b507a08c87ecfe98="
	githubClientID = "IV1.b507a08c87ecfe98="

	githubCopilotUserAgent      = "GitHubCopilotChat/0.35.0"
	githubCopilotEditorVersion  = "vscode/1.107.0"
	githubCopilotPluginVersion  = "copilot-chat/0.35.0"
	githubCopilotIntegrationID  = "vscode-chat"
	githubCopilotAPIHostDefault = "api.individual.githubcopilot.com"

	initialPollIntervalMultiplier = 1.2
	slowDownPollIntervalMultiplier = 1.4
)

type GithubCopilotCredentials struct {
	OAuthCredentials
	EnterpriseURL string `json:"enterprise_url,omitempty"`
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type deviceTokenSuccessResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

type deviceTokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
	Interval         int    `json:"interval,omitempty"`
}

func normalizeDomain(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

type githubUrls struct {
	deviceCodeUrl   string
	accessTokenUrl  string
	copilotTokenUrl string
}

func getGitHubUrls(domain string) githubUrls {
	return githubUrls{
		deviceCodeUrl:   fmt.Sprintf("https://%s/login/device/code", domain),
		accessTokenUrl:  fmt.Sprintf("https://%s/login/oauth/access_token", domain),
		copilotTokenUrl: fmt.Sprintf("https://api.%s/copilot_internal/v2/token", domain),
	}
}

func getBaseUrlFromToken(token string) string {
	parts := strings.Split(token, ";")
	for _, part := range parts {
		if strings.HasPrefix(part, "proxy-ep=") {
			proxyHost := strings.TrimPrefix(part, "proxy-ep=")
			apiHost := strings.Replace(proxyHost, "proxy.", "api.", 1)
			return fmt.Sprintf("https://%s", apiHost)
		}
	}
	return ""
}

func getGitHubCopilotBaseUrl(token string, enterpriseDomain string) string {
	if token != "" {
		if urlFromToken := getBaseUrlFromToken(token); urlFromToken != "" {
			return urlFromToken
		}
	}
	if enterpriseDomain != "" {
		return fmt.Sprintf("https://copilot-api.%s", enterpriseDomain)
	}
	return fmt.Sprintf("https://%s", githubCopilotAPIHostDefault)
}

func startDeviceFlow(ctx context.Context, domain string) (*deviceCodeResponse, error) {
	urls := getGitHubUrls(domain)

	form := url.Values{}
	form.Set("client_id", githubClientID)
	form.Set("scope", "read:user")

	req, err := http.NewRequestWithContext(ctx, "POST", urls.deviceCodeUrl, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", githubCopilotUserAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request failed: %d %s: %s", resp.StatusCode, resp.Status, string(body))
	}

	var data deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid device code response: %w", err)
	}

	return &data, nil
}

func pollForGitHubAccessToken(ctx context.Context, domain string, deviceCode string, intervalSeconds int, expiresIn int) (string, error) {
	urls := getGitHubUrls(domain)
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	intervalMs := max(1000, intervalSeconds*1000)
	intervalMultiplier := initialPollIntervalMultiplier
	slowDownResponses := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		remainingMs := time.Until(deadline).Milliseconds()
		waitMs := min(int64(float64(intervalMs)*intervalMultiplier), remainingMs)
		select {
		case <-time.After(time.Duration(waitMs) * time.Millisecond):
		case <-ctx.Done():
			return "", ctx.Err()
		}

		form := url.Values{}
		form.Set("client_id", githubClientID)
		form.Set("device_code", deviceCode)
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

		req, err := http.NewRequestWithContext(ctx, "POST", urls.accessTokenUrl, strings.NewReader(form.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", githubCopilotUserAgent)

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode == 200 {
			var success deviceTokenSuccessResponse
			if err := json.Unmarshal(body, &success); err == nil && success.AccessToken != "" {
				return success.AccessToken, nil
			}
		}

		var errorResp deviceTokenErrorResponse
		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error != "" {
			switch errorResp.Error {
			case "authorization_pending":
				continue
			case "slow_down":
				slowDownResponses++
				if errorResp.Interval > 0 {
					intervalMs = errorResp.Interval * 1000
				} else {
					intervalMs = max(1000, intervalMs+5000)
				}
				intervalMultiplier = slowDownPollIntervalMultiplier
				continue
			default:
				suffix := ""
				if errorResp.ErrorDescription != "" {
					suffix = ": " + errorResp.ErrorDescription
				}
				return "", fmt.Errorf("device flow failed: %s%s", errorResp.Error, suffix)
			}
		}
	}

	if slowDownResponses > 0 {
		return "", fmt.Errorf("device flow timed out after slow_down responses (possible clock drift)")
	}
	return "", fmt.Errorf("device flow timed out")
}

func refreshGitHubCopilotToken(ctx context.Context, refreshToken string, enterpriseDomain string) (*OAuthCredentials, error) {
	domain := enterpriseDomain
	if domain == "" {
		domain = "github.com"
	}
	urls := getGitHubUrls(domain)

	req, err := http.NewRequestWithContext(ctx, "POST", urls.copilotTokenUrl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+refreshToken)
	req.Header.Set("User-Agent", githubCopilotUserAgent)
	req.Header.Set("Editor-Version", githubCopilotEditorVersion)
	req.Header.Set("Editor-Plugin-Version", githubCopilotPluginVersion)
	req.Header.Set("Copilot-Integration-Id", githubCopilotIntegrationID)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("copilot token refresh failed: %d %s: %s", resp.StatusCode, resp.Status, string(body))
	}

	var data struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid copilot token response: %w", err)
	}

	return &OAuthCredentials{
		Refresh: refreshToken,
		Access:  data.Token,
		Expires: data.ExpiresAt*1000 - 5*60*1000,
		Extra: map[string]any{
			"enterprise_url": enterpriseDomain,
		},
	}, nil
}

func LoginGitHubCopilot(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	prompt := OAuthPrompt{
		Message:     "GitHub Enterprise URL/domain (blank for github.com)",
		Placeholder: "company.ghe.com",
		AllowEmpty:  true,
	}
	input, err := callbacks.OnPrompt(prompt)
	if err != nil {
		return nil, err
	}

	enterpriseDomain := normalizeDomain(input)
	trimmed := strings.TrimSpace(input)
	if trimmed != "" && enterpriseDomain == "" {
		return nil, fmt.Errorf("invalid GitHub Enterprise URL/domain")
	}
	domain := enterpriseDomain
	if domain == "" {
		domain = "github.com"
	}

	device, err := startDeviceFlow(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to start device flow: %w", err)
	}

	callbacks.OnAuth(OAuthAuthInfo{
		URL:          device.VerificationURI,
		Instructions: fmt.Sprintf("Enter code: %s", device.UserCode),
	})

	githubAccessToken, err := pollForGitHubAccessToken(ctx, domain, device.DeviceCode, device.Interval, device.ExpiresIn)
	if err != nil {
		return nil, err
	}

	creds, err := refreshGitHubCopilotToken(ctx, githubAccessToken, enterpriseDomain)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange for copilot token: %w", err)
	}

	return creds, nil
}

func RefreshGitHubCopilotToken(ctx context.Context, credentials OAuthCredentials) (*OAuthCredentials, error) {
	enterpriseURL := ""
	if val, ok := credentials.Extra["enterprise_url"].(string); ok {
		enterpriseURL = val
	}
	return refreshGitHubCopilotToken(ctx, credentials.Refresh, enterpriseURL)
}
