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
	anthropicClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicAuthorizeURL = "https://claude.ai/oauth/authorize"
	anthropicTokenURL     = "https://platform.claude.com/v1/oauth/token"
	anthropicCallbackPort = 53692
	anthropicCallbackPath = "/callback"
	anthropicRedirectURI  = "http://localhost:53692/callback"
	anthropicScopes       = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

type callbackServerInfo struct {
	server      *http.Server
	redirectURI string
	cancelWait  func()
	waitForCode func() (*callbackResult, error)
}

type callbackResult struct {
	Code  string
	State string
}

func parseAuthorizationInput(input string) *callbackResult {
	value := strings.TrimSpace(input)
	if value == "" {
		return nil
	}

	if u, err := url.Parse(value); err == nil && u.Scheme != "" {
		return &callbackResult{
			Code:  u.Query().Get("code"),
			State: u.Query().Get("state"),
		}
	}

	if strings.Contains(value, "#") {
		parts := strings.SplitN(value, "#", 2)
		return &callbackResult{Code: parts[0], State: parts[1]}
	}

	if strings.Contains(value, "code=") {
		q, err := url.ParseQuery(value)
		if err == nil {
			return &callbackResult{Code: q.Get("code"), State: q.Get("state")}
		}
	}
	return &callbackResult{Code: value}
}

func startCallbackServer(ctx context.Context, port int, path string, expectedState string) (*callbackServerInfo, error) {
	codeCh := make(chan *callbackResult, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}

	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(400)
			fmt.Fprint(w, OAuthErrorHTML("Authentication did not complete.", "Error: "+errParam))
			return
		}
		code := q.Get("code")
		state := q.Get("state")
		if code == "" || state == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(400)
			fmt.Fprint(w, OAuthErrorHTML("Missing code or state parameter."))
			return
		}
		if state != expectedState {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(400)
			fmt.Fprint(w, OAuthErrorHTML("State mismatch."))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, OAuthSuccessHTML("Authentication completed. You can close this window."))
		select {
		case codeCh <- &callbackResult{Code: code, State: state}:
		default:
		}
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	return &callbackServerInfo{
		server:      srv,
		redirectURI: fmt.Sprintf("http://localhost:%d%s", port, path),
		cancelWait: func() {
			select {
			case codeCh <- nil:
			default:
			}
		},
		waitForCode: func() (*callbackResult, error) {
			select {
			case result := <-codeCh:
				return result, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}, nil
}

func postJSON(ctx context.Context, urlStr string, body map[string]string) (string, error) {
	form := url.Values{}
	for k, v := range body {
		form.Set(k, v)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return string(b), nil
}

func exchangeAuthCode(ctx context.Context, code, state, verifier, redirectURI string) (*OAuthCredentials, error) {
	body := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     anthropicClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	}

	respBody, err := postJSON(ctx, anthropicTokenURL, body)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(respBody), &data); err != nil {
		return nil, fmt.Errorf("invalid token response JSON: %w", err)
	}

	return &OAuthCredentials{
		Refresh: data.RefreshToken,
		Access:  data.AccessToken,
		Expires: time.Now().UnixMilli() + data.ExpiresIn*1000 - 5*60*1000,
	}, nil
}

// LoginAnthropic runs the Anthropic OAuth flow (authorization code + PKCE).
func LoginAnthropic(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}

	srv, err := startCallbackServer(ctx, anthropicCallbackPort, anthropicCallbackPath, verifier)
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	defer srv.server.Close()

	params := url.Values{
		"code":                  {"true"},
		"client_id":             {anthropicClientID},
		"response_type":         {"code"},
		"redirect_uri":          {anthropicRedirectURI},
		"scope":                 {anthropicScopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {verifier},
	}

	callbacks.OnAuth(OAuthAuthInfo{
		URL:          fmt.Sprintf("%s?%s", anthropicAuthorizeURL, params.Encode()),
		Instructions: "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here.",
	})

	result, err := srv.waitForCode()
	if err != nil {
		return nil, err
	}

	var code, state string
	if result != nil && result.Code != "" {
		code = result.Code
		state = result.State
	}

	if code == "" {
		if callbacks.OnPrompt == nil {
			return nil, fmt.Errorf("missing authorization code")
		}
		input, err := callbacks.OnPrompt(OAuthPrompt{
			Message:     "Paste the authorization code or full redirect URL:",
			Placeholder: anthropicRedirectURI,
		})
		if err != nil {
			return nil, err
		}
		parsed := parseAuthorizationInput(input)
		if parsed == nil || parsed.Code == "" {
			return nil, fmt.Errorf("missing authorization code")
		}
		code = parsed.Code
		state = parsed.State
		if state == "" {
			state = verifier
		}
	}

	if callbacks.OnProgress != nil {
		callbacks.OnProgress("Exchanging authorization code for tokens...")
	}

	creds, err := exchangeAuthCode(ctx, code, state, verifier, srv.redirectURI)
	if err != nil {
		return nil, err
	}
	return creds, nil
}

// RefreshAnthropicToken refreshes an Anthropic OAuth token.
func RefreshAnthropicToken(ctx context.Context, refreshToken string) (*OAuthCredentials, error) {
	body := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     anthropicClientID,
		"refresh_token": refreshToken,
	}

	respBody, err := postJSON(ctx, anthropicTokenURL, body)
	if err != nil {
		return nil, fmt.Errorf("anthropic token refresh failed: %w", err)
	}

	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(respBody), &data); err != nil {
		return nil, fmt.Errorf("invalid refresh response JSON: %w", err)
	}

	return &OAuthCredentials{
		Refresh: data.RefreshToken,
		Access:  data.AccessToken,
		Expires: time.Now().UnixMilli() + data.ExpiresIn*1000 - 5*60*1000,
	}, nil
}
