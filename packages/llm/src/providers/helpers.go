package providers

import (
	"encoding/json"
	"strings"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Message type assertions
// =============================================================================

// okAssistant type-asserts a message to AssistantMessage, nil if not
func okAssistant(msg src.Message) *src.AssistantMessage {
	if am, ok := msg.(*src.AssistantMessage); ok {
		return am
	}
	return nil
}

// =============================================================================
// API key helpers
// =============================================================================

// getEnvApiKey retrieves API key from environment variable.
// Delegates to the faithful env-api-keys.ts port (env_api_keys.go) — the old
// rewrite guessed %s_API_KEY for unknown providers and missed
// ANTHROPIC_OAUTH_TOKEN precedence and GEMINI_API_KEY.
func getEnvApiKey(provider string) string {
	return src.GetEnvApiKey(provider)
}

// usageCostFromModelCost converts CalculateCost's ModelCost into the
// UsageCost embedded in messages (identical fields, distinct types).
func usageCostFromModelCost(mc src.ModelCost) src.UsageCost {
	return src.UsageCost{Input: mc.Input, Output: mc.Output, CacheRead: mc.CacheRead, CacheWrite: mc.CacheWrite, Total: mc.Total}
}

// =============================================================================
// Streaming JSON parsing
// =============================================================================

// parseStreamingJson parses streaming JSON by merging partial JSON with existing data
// Handles incremental tool argument streaming from providers like Anthropic and OpenAI
//
// Matches the reference parseStreamingJson: strict parse with repair first,
// then a partial-json prefix parse, so in-flight arguments grow progressively.
// Always returns a non-nil map (empty args marshal as {} not null). Callers
// must pass the ACCUMULATED argument string (not the latest delta).
func parseStreamingJson(partialJson string) map[string]interface{} {
	if m := utils.ParseStreamingJSON[map[string]interface{}](partialJson); m != nil {
		return m
	}
	return map[string]interface{}{}
}

// parseStreamingJsonBytes parses JSON from byte data (alternative signature)
func parseStreamingJsonBytes(data []byte, target interface{}) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	return decoder.Decode(target)
}

// =============================================================================
// Float value extraction
// =============================================================================

// getFloatValueHelper extracts a float value from a map (helper function)
func getFloatValueHelper(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case float32:
			return float64(val)
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case json.Number:
			if f, err := val.Float64(); err == nil {
				return f
			}
		}
	}
	return 0
}

// =============================================================================
// Claude code name conversion
// =============================================================================

// claudeCodeTools are the canonical Claude Code 2.x tool names (anthropic.ts:75-93).
var claudeCodeTools = []string{
	"Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion",
	"EnterPlanMode", "ExitPlanMode", "KillShell", "NotebookEdit", "Skill",
	"Task", "TaskOutput", "TodoWrite", "WebFetch", "WebSearch",
}

// toClaudeCodeName maps a tool name to its canonical CC casing
// (case-insensitive list lookup, name unchanged when not a CC tool).
func toClaudeCodeName(name string) string {
	for _, cc := range claudeCodeTools {
		if strings.EqualFold(cc, name) {
			return cc
		}
	}
	return name
}

// fromClaudeCodeName maps a streamed CC-cased name back to the registered
// tool's actual name (case-insensitive first match).
func fromClaudeCodeName(name string, tools []src.Tool[any]) string {
	if len(tools) > 0 {
		for _, tool := range tools {
			if strings.EqualFold(tool.Name, name) {
				return tool.Name
			}
		}
	}
	return name
}

// =============================================================================
// Context helpers
// =============================================================================

// randomStringHelper generates a random string of specified length
func randomStringHelper(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[i%len(charset)]
	}
	return string(result)
}

// hasToolCallsInContentHelper checks if content contains tool calls
func hasToolCallsInContentHelper(content []src.ContentBlock) bool {
	for _, c := range content {
		if _, ok := c.(*src.ToolCall); ok {
			return true
		}
	}
	return false
}

// =============================================================================
// Cache control helpers
// =============================================================================

// buildAnthropicCacheControl builds cache control headers for Anthropic
func buildAnthropicCacheControl(cacheBreakpoints []int, systemPrompt bool) []map[string]interface{} {
	if len(cacheBreakpoints) == 0 && !systemPrompt {
		return nil
	}

	var result []map[string]interface{}

	// Add system prompt cache control
	if systemPrompt {
		result = append(result, map[string]interface{}{
			"type": "system",
		})
	}

	// Add breakpoint cache controls
	for _, bp := range cacheBreakpoints {
		result = append(result, map[string]interface{}{
			"type": "messages",
			"index": bp,
		})
	}

	return result
}

// buildOpenAICacheControl builds cache control for OpenAI
func buildOpenAICacheControl(enabled bool) map[string]interface{} {
	if !enabled {
		return nil
	}
	return map[string]interface{}{
		"type": "ephemeral",
	}
}

// =============================================================================
// Message parameter building
// =============================================================================

// contextMessagesParamHelper extracts messages parameter from chat context
// This is a common helper used by OpenAI-compatible providers
func contextMessagesParamHelper(context src.ChatContext) []src.Message {
	return context.Messages
}
