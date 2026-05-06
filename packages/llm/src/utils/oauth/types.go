package oauth

import (
	"context"

	"ycode/packages/llm/src"
)

type OAuthCredentials struct {
	Refresh 	string 		`json:"refresh"`
	Access 		string 		`json:"access"`
	Expires 	int64 		`json:"expires"`
	Extra 		map[string]any 	`json:"extra"`
}

type OAuthPrompt struct {
	Message 	string 		`json:"message"`
	Placeholder string 		`json:"placeholder,omitempty"`
	AllowEmpty 	bool 		`json:"allowEmpty,omitempty"`
}

type OAuthAuthInfo struct {
	URL 			string 		`json:"url"`
	Instructions	string 		`json:"instructions,omitempty"`
}

type OAuthLoginCallbacks struct {
	OnAuth 		func(info OAuthAuthInfo)
	OnPrompt  	func(prompt OAuthPrompt) (string, error)
	OnProgress 	func(message string)
	OnManualCodeInput func() (string, error)
	Cancel 		context.Context //Replaces TS Abort Signal
}

type OAuthProviderInterface interface {
	ID() string
	Name() string
	Login(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error)
	RefreshTokens(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error)
	GetApiKey(credentials OAuthCredentials) string
	UsesCallbackServer() bool
	ModifyModels(models []llm.Model[llm.Api], credentials OAuthCredentials) []llm.Model[llm.Api]
}

