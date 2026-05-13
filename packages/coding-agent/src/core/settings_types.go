package ycode

import (
	"ycode/packages/llm/src"
)

type CompactionSettings struct {
	Enabled 		*bool 	`json:"enabled,omitempty"`
	ReserveTokens 	*int 	`json:"reserveTokens,omitempty"`
	KeepRecentTokens *int 	`json:"keepRecentTokens,omitempty"`
}

type BranchSummarySettings struct {
	ReserveTokens *int 	`json:"reserveTokens,omitempty"`
	SkipPrompt 	  *bool `json:"skipPrompt,omitempty"`
}

type ProviderRetrySettings struct {
	TimeoutMs 		*int `json:"timeoutMs,omitempty"`
	MaxRetries  	*int `json:"maxRetries,omitempty"`
	MaxRetryDelayMs *int `json:"maxRetryDelayMs,omitempty"`
}

type RetrySettings struct {
	Enabled 	*bool  		`json:"enabled,omitempty"`
	MaxRetries 	*int 		`json:"maxRetries,omitempty"`
	BaseDelayMs *int 		`json:"baseDelayMs,omitempty"`
	Provider 	*ProviderRetrySettings `json:"provider,omitempty"`
}

type TerminalSettings struct {
	ShowImages 		*bool `json:"showImages,omitempty"`
	ImageWidthCells *int  `json:"imageWidthCells,omitempty"`
	ClearOnShrink 	*bool `json:"cleanOnShrink,omitempty"`
	ShowTerminalProgress *bool `json:"showTerminalProgress,omitempty"`
}

type ImageSettings struct {
	AutoResize 		*bool 	`json:"autoResize,omitempty"`
	BlockImages 	*bool 	`json:"blockImages,omitempty"`
}

type ThinkingBudgetsSettings struct {
	Minimal *int `json:"minimal,omitempty"`
	Low 	*int `json:"low,omitempty"`
	Medium  *int `json:"medium,omitempty"`
	High	*int `json:"high,omitempty"`
}

type MarkdownSettings struct {
	CodeBlockIndent *string `json:"codeBlockIndent,omitempty"`
}

type WarningSettings struct {
	AnthropicExtraUsage  *bool 	`json:"anthropicExtraUsage,omitempty"`
}

type PackageSourceFilter struct {
	Source 		string  	`json:"source"`
	Extensions	[]string 	`json:"extensions,omitempty"`
	Skills 		[]string 	`json:"skills,omitempty"`
	Prompts 	[]string 	`json:"prompt,omitempty"`
	Themes 		[]string 	`json:"themes,omitempty"`
}

type PackageSource = any   // string or PackageSourceFilter

type Settings struct {
	LastChangelogVersion	*string 		`json:"lastChangelogVersion,omitempty"`
	DefaultProvider 		*string 		`json:"defaultProvider,omitempty"`
	DefaultModel 			*string 		`json:"defaultModel,omitempty"`
	DefaultThinkingLevel 	*string 		`json:"defaultThinkingLevel,omitempty"`
	Transport 				*llm.Transport 	`json:"transport,omitempty"`
	SteeringMode 			*string 		`json:"steeringMode,omitempty"`
	FollowUpMode 			*string 		`json:"followUpMode,omitempty"`
	Theme 					*string 		`json:"theme,omitempty"`
	Compaction 				*CompactionSettings `json:"compaction,omitempty"`
	BranchSummary 			*BranchSummarySettings `json:"branchSummary,omitempty"`
	Retry 					*RetrySettings 	`json:"retry,omitempty"`
	HideThinkingBlock 		*bool 			`json:"hideThinkingBlock,omitempty"`
}