package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openaiCodexClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	openaiCodexAuthorizeURL  = "https://auth.openai.com/oauth/authorize"
	openaiCodexTokenURL      = "https://auth.openai.com/oauth/token"
	openaiCodexRedirectURI   = "http://localhost:1455/auth/callback"
	openaiCodexScope         = "openid profile email offline_access"
	openaiCodexCallbackPort  = 1455
	openaiCodexJWTClaimPath  = "https://api.openai.com/auth"
)

type OpenaiCodexCredentials struct {
	OAuthCredentials
	AccountID string `json:"account_id"`
}

type tokenSuccess struct {
	Access  string
	Refresh string
	Expires int64
}

type tokenResult struct {
	Type    string // "success" or "failed"
	Success *tokenSuccess
}

func createOpenAIState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func decodeJwt(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	payload := parts[1]
	if payload == "" {
		return nil, fmt.Errorf("empty JWT payload")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(decoded, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JWT payload: %w", err)
	}

	return result, nil
}

func getOpenAIAccountID(accessToken string) (string, error) {
	payload, err := decodeJwt(accessToken)
	if err != nil {
		return "", err
	}

	auth, ok := payload[openaiCodexJWTClaimPath].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing auth claim")
	}

	accountID, ok := auth["chatgpt_account_id"].(string)
	if !ok || accountID == "" {
		return "", fmt.Errorf("missing account_id")
	}

	return accountID, nil
}

func exchangeOpenAICode(ctx context.Context, code, verifier, redirectURI string) (*tokenResult, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", openaiCodexClientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)

	req, _ := http.NewRequestWithContext(ctx, "POST", openaiCodexTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return &tokenResult{Type: "failed"}, nil
	}

	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return &tokenResult{Type: "failed"}, nil
	}

	if data.AccessToken == "" || data.RefreshToken == "" || data.ExpiresIn == 0 {
		return &tokenResult{Type: "failed"}, nil
	}

	return &tokenResult{
		Type: "success",
		Success: &tokenSuccess{
			Access:  data.AccessToken,
			Refresh: data.RefreshToken,
			Expires: time.Now().UnixMilli() + data.ExpiresIn*1000,
		},
	}, nil
}

func refreshOpenAICode(ctx context.Context, refreshToken string) (*tokenResult, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", openaiCodexClientID)

	req, _ := http.NewRequestWithContext(ctx, "POST", openaiCodexTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return &tokenResult{Type: "failed"}, nil
	}

	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return &tokenResult{Type: "failed"}, nil
	}

	if data.AccessToken == "" || data.RefreshToken == "" || data.ExpiresIn == 0 {
		return &tokenResult{Type: "failed"}, nil
	}

	return &tokenResult{
		Type: "success",
		Success: &tokenSuccess{
			Access:  data.AccessToken,
			Refresh: data.RefreshToken,
			Expires: time.Now().UnixMilli() + data.ExpiresIn*1000,
		},
	}, nil
}

func LoginOpenAICodex(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}

	state, err := createOpenAIState()
	if err != nil {
		return nil, err
	}

	srv, err := startCallbackServer(ctx, openaiCodexCallbackPort, "/auth/callback", state)
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	defer srv.server.Close()

	params := url.Values{
		"response_type":               {"code"},
		"client_id":                   {openaiCodexClientID},
		"redirect_uri":                {openaiCodexRedirectURI},
		"scope":                       {openaiCodexScope},
		"code_challenge":              {challenge},
		"code_challenge_method":       {"S256"},
		"state":                       {state},
		"id_token_add_organizations":  {"true"},
		"codex_cli_simplified_flow":   {"true"},
		"originator":                  {"pi"},
	}

	callbacks.OnAuth(OAuthAuthInfo{
		URL:          fmt.Sprintf("%s?%s", openaiCodexAuthorizeURL, params.Encode()),
		Instructions: "A browser window should open. Complete login to finish.",
	})

	result, err := srv.waitForCode()
	if err != nil {
		return nil, err
	}

	var code string
	if result != nil && result.Code != "" {
		code = result.Code
	}

	if code == "" {
		if callbacks.OnPrompt == nil {
			return nil, fmt.Errorf("missing authorization code")
		}
		input, err := callbacks.OnPrompt(OAuthPrompt{
			Message: "Paste the authorization code (or full redirect URL):",
		})
		if err != nil {
			return nil, err
		}
		parsed := parseAuthorizationInput(input)
		if parsed == nil || parsed.Code == "" {
			return nil, fmt.Errorf("missing authorization code")
		}
		code = parsed.Code
	}

	tokenResult, err := exchangeOpenAICode(ctx, code, verifier, openaiCodexRedirectURI)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	if tokenResult.Type != "success" {
		return nil, fmt.Errorf("token exchange failed")
	}

	accountID, err := getOpenAIAccountID(tokenResult.Success.Access)
	if err != nil {
		return nil, fmt.Errorf("failed to extract account_id from token: %w", err)
	}

	return &OAuthCredentials{
		Refresh: tokenResult.Success.Refresh,
		Access:  tokenResult.Success.Access,
		Expires: tokenResult.Success.Expires,
		Extra: map[string]any{
			"account_id": accountID,
		},
	}, nil
}

func RefreshOpenAICodexToken(ctx context.Context, credentials OAuthCredentials) (*OAuthCredentials, error) {
	result, err := refreshOpenAICode(ctx, credentials.Refresh)
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}

	if result.Type != "success" {
		return nil, fmt.Errorf("token refresh failed")
	}

	accountID, err := getOpenAIAccountID(result.Success.Access)
	if err != nil {
		return nil, fmt.Errorf("failed to extract account_id from token: %w", err)
	}

	return &OAuthCredentials{
		Refresh: result.Success.Refresh,
		Access:  result.Success.Access,
		Expires: result.Success.Expires,
		Extra: map[string]any{
			"account_id": accountID,
		},
	}, nil
}
