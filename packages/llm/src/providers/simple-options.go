package providers

import (
	src "pigo.dev/packages/llm/src"
)

func BuildBaseOptions(model src.Model[src.Api], options *src.SimpleStreamOptions, apiKey string) src.StreamOptions {
	opts := src.StreamOptions{}
	if options != nil {
		opts = options.StreamOptions
	}
	if apiKey != "" {
		opts.APIKey = apiKey
	}
	maxTokens := 0
	if opts.MaxTokens != nil {
		maxTokens = *opts.MaxTokens
	}
	if maxTokens == 0 && model.MaxTokens > 0 {
		capped := model.MaxTokens
		if capped > 32000 {
			capped = 32000
		}
		maxTokens = capped
		opts.MaxTokens = &capped
	}
	return opts
}

func ClampReasoning(effort src.ThinkingLevel) src.ThinkingLevel {
	if effort == src.XHigh {
		return src.High
	}
	return effort
}

type AdjustedTokens struct {
	MaxTokens      int
	ThinkingBudget int
}

func AdjustMaxTokensForThinking(baseMaxTokens, modelMaxTokens int, reasoning src.ThinkingLevel, customBudgets *src.ThinkingBudgets) AdjustedTokens {
	minimal := 1024
	low := 2048
	medium := 8192
	high := 16384

	if customBudgets != nil {
		if customBudgets.Minimal != nil {
			minimal = *customBudgets.Minimal
		}
		if customBudgets.Low != nil {
			low = *customBudgets.Low
		}
		if customBudgets.Medium != nil {
			medium = *customBudgets.Medium
		}
		if customBudgets.High != nil {
			high = *customBudgets.High
		}
	}

	level := ClampReasoning(reasoning)
	var thinkingBudget int
	switch level {
	case src.Minimal:
		thinkingBudget = minimal
	case src.Low:
		thinkingBudget = low
	case src.Medium:
		thinkingBudget = medium
	case src.High:
		thinkingBudget = high
	default:
		thinkingBudget = high
	}

	const minOutputTokens = 1024
	maxTokens := baseMaxTokens + thinkingBudget
	if maxTokens > modelMaxTokens {
		maxTokens = modelMaxTokens
	}
	if maxTokens <= thinkingBudget {
		thinkingBudget = maxTokens - minOutputTokens
		if thinkingBudget < 0 {
			thinkingBudget = 0
		}
	}

	return AdjustedTokens{MaxTokens: maxTokens, ThinkingBudget: thinkingBudget}
}
