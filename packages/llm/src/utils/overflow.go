package utils

import (
	"regexp"

	"ycode/packages/llm/src"
)

var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),                                                             // Anthropic
	regexp.MustCompile(`(?i)request_too_large`),                                                              // Anthropic HTTP 413
	regexp.MustCompile(`(?i)input is too long for requested model`),                                          // Bedrock
	regexp.MustCompile(`(?i)exceeds the context window`),                                                     // OpenAI
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),                                         // Google
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),                                                   // xAI
	regexp.MustCompile(`(?i)reduce the length of the messages`),                                              // Groq
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),                                           // OpenRouter
	regexp.MustCompile(`(?i)input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`), // Together AI
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),                                                       // GitHub Copilot
	regexp.MustCompile(`(?i)exceeds the available context size`),                                             // llama.cpp
	regexp.MustCompile(`(?i)greater than the context length`),                                                // LM Studio
	regexp.MustCompile(`(?i)context window exceeds limit`),                                                   // MiniMax
	regexp.MustCompile(`(?i)exceeded model token limit`),                                                     // Kimi
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context`),                                   // Mistral
	regexp.MustCompile(`(?i)model_context_window_exceeded`),                                                  // z.ai
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),                              // Ollama
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),                                                  // Generic
	regexp.MustCompile(`(?i)too many tokens`),                                                                // Generic
	regexp.MustCompile(`(?i)token limit exceeded`),                                                           // Generic
	regexp.MustCompile(`(?i)^4(?:00|13)\s*(?:status code)?\s*\(no body\)`),                                   // Cerebras
}

var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(Throttling error|Service unavailable):`), // AWS Bedrock
	regexp.MustCompile(`(?i)rate limit`),                               // Generic
	regexp.MustCompile(`(?i)too many requests`),                        // HTTP 429
}

// IsContextOverflow: Checks if an assistant message represents a context overflow error
// Pass contextWindow is to detect silent overflow (e.g. z.ai)
func IsContextOverflow(message llm.AssistantMessage, contextWindow int) bool {
	// Case 1: error message patterns
	if message.StopReason == llm.StopReasonError && message.ErrorMessage != "" {
		if !matchesAny(nonOverflowPatterns, message.ErrorMessage) &&
			matchesAny(overflowPatterns, message.ErrorMessage) {
			return true
		}
	}
	// Case 2: silent overflow - usage exceeds context window
	if contextWindow > 0 && message.StopReason == llm.StopReasonStop {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if inputTokens > contextWindow {
			return true
		}
	}
	// Case 3: length-stop overflow (Xiaomi MiMo style) - the server truncates
	// the oversized input to fit the context window, leaving no room for
	// output. Reports stopReason "length" with output=0 and input+cacheRead
	// filling the context window.
	if contextWindow > 0 && message.StopReason == llm.StopReasonLength && message.Usage.Output == 0 {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if float64(inputTokens) >= float64(contextWindow)*0.99 {
			return true
		}
	}
	return false
}

func matchesAny(patterns []*regexp.Regexp, s string) bool {
	for _, p := range patterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}
