package llm

// MergeCompat overlays a compat override (typically the per-model `compat`
// block from models.json) on a base compat (e.g. provider-level compat).
// Semantics mirror the reference's mergeCompat: each field set in the override
// wins, unset fields keep the base value, and the routing sub-objects merge
// per field rather than being replaced wholesale. A sub-struct is non-nil in
// the result when either side had it.
func MergeCompat(base, override Compat) Compat {
	return Compat{
		OpenAICompletions: mergeOpenAICompletionsCompat(base.OpenAICompletions, override.OpenAICompletions),
		OpenAIResponses:   mergeOpenAIResponsesCompat(base.OpenAIResponses, override.OpenAIResponses),
		AnthropicMessages: mergeAnthropicMessagesCompat(base.AnthropicMessages, override.AnthropicMessages),
	}
}

func mergeOpenAICompletionsCompat(base, override *OpenAICompletionsCompat) *OpenAICompletionsCompat {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	merged := *base
	if override.SupportStore != nil {
		merged.SupportStore = override.SupportStore
	}
	if override.SupportsDeveloperRole != nil {
		merged.SupportsDeveloperRole = override.SupportsDeveloperRole
	}
	if override.SupportsReasoningEffort != nil {
		merged.SupportsReasoningEffort = override.SupportsReasoningEffort
	}
	if override.ReasoningEffortMap != nil {
		merged.ReasoningEffortMap = override.ReasoningEffortMap
	}
	if override.SupportsUsageInStreaming != nil {
		merged.SupportsUsageInStreaming = override.SupportsUsageInStreaming
	}
	if override.MaxTokensField != nil {
		merged.MaxTokensField = override.MaxTokensField
	}
	if override.RequireToolResultName != nil {
		merged.RequireToolResultName = override.RequireToolResultName
	}
	if override.RequireAssistantAfterToolResult != nil {
		merged.RequireAssistantAfterToolResult = override.RequireAssistantAfterToolResult
	}
	if override.RequireThinkingAsText != nil {
		merged.RequireThinkingAsText = override.RequireThinkingAsText
	}
	if override.RequireReasoningContentOnAssistantMessages != nil {
		merged.RequireReasoningContentOnAssistantMessages = override.RequireReasoningContentOnAssistantMessages
	}
	if override.ThinkingFormat != nil {
		merged.ThinkingFormat = override.ThinkingFormat
	}
	if override.OpenRouterRouting != nil {
		merged.OpenRouterRouting = mergeOpenRouterRouting(base.OpenRouterRouting, override.OpenRouterRouting)
	}
	if override.VercelGatewayRouting != nil {
		merged.VercelGatewayRouting = mergeVercelGatewayRouting(base.VercelGatewayRouting, override.VercelGatewayRouting)
	}
	if override.ZaiToolStream != nil {
		merged.ZaiToolStream = override.ZaiToolStream
	}
	if override.SupportsStrictMode != nil {
		merged.SupportsStrictMode = override.SupportsStrictMode
	}
	if override.CacheControlFormat != nil {
		merged.CacheControlFormat = override.CacheControlFormat
	}
	if override.SendSessionAffinityHeaders != nil {
		merged.SendSessionAffinityHeaders = override.SendSessionAffinityHeaders
	}
	if override.SupportsLongCacheRetention != nil {
		merged.SupportsLongCacheRetention = override.SupportsLongCacheRetention
	}
	return &merged
}

func mergeOpenAIResponsesCompat(base, override *OpenAIResponsesCompat) *OpenAIResponsesCompat {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	merged := *base
	if override.SendSessionIDHeader != nil {
		merged.SendSessionIDHeader = override.SendSessionIDHeader
	}
	if override.SupportsLongCacheRetention != nil {
		merged.SupportsLongCacheRetention = override.SupportsLongCacheRetention
	}
	return &merged
}

func mergeAnthropicMessagesCompat(base, override *AnthropicMessagesCompat) *AnthropicMessagesCompat {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	merged := *base
	if override.SupportsEagerToolInputStreaming != nil {
		merged.SupportsEagerToolInputStreaming = override.SupportsEagerToolInputStreaming
	}
	if override.SupportsLongCacheRetention != nil {
		merged.SupportsLongCacheRetention = override.SupportsLongCacheRetention
	}
	return &merged
}

func mergeOpenRouterRouting(base, override *OpenRouterRouting) *OpenRouterRouting {
	if base == nil {
		return override
	}
	merged := *base
	if override.AllowFallbacks != nil {
		merged.AllowFallbacks = override.AllowFallbacks
	}
	if override.RequireParameters != nil {
		merged.RequireParameters = override.RequireParameters
	}
	if override.DataCollection != nil {
		merged.DataCollection = override.DataCollection
	}
	if override.Zdr != nil {
		merged.Zdr = override.Zdr
	}
	if override.EnforceDistillableText != nil {
		merged.EnforceDistillableText = override.EnforceDistillableText
	}
	if override.Order != nil {
		merged.Order = override.Order
	}
	if override.Only != nil {
		merged.Only = override.Only
	}
	if override.Ignore != nil {
		merged.Ignore = override.Ignore
	}
	if override.Quantizations != nil {
		merged.Quantizations = override.Quantizations
	}
	if override.Sort != nil {
		merged.Sort = override.Sort
	}
	if override.MaxPrice != nil {
		merged.MaxPrice = override.MaxPrice
	}
	if override.PreferredMinThroughput != nil {
		merged.PreferredMinThroughput = override.PreferredMinThroughput
	}
	if override.PreferredMaxLatency != nil {
		merged.PreferredMaxLatency = override.PreferredMaxLatency
	}
	return &merged
}

func mergeVercelGatewayRouting(base, override *VercelGatewayRouting) *VercelGatewayRouting {
	if base == nil {
		return override
	}
	merged := *base
	if override.Only != nil {
		merged.Only = override.Only
	}
	if override.Order != nil {
		merged.Order = override.Order
	}
	return &merged
}
