package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	src "ycode/packages/llm/src"
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

// getEnvApiKey retrieves API key from environment variable
func getEnvApiKey(provider string) string {
	envKey := ""
	switch strings.ToLower(provider) {
	case "anthropic", "claude":
		envKey = "ANTHROPIC_API_KEY"
	case "openai":
		envKey = "OPENAI_API_KEY"
	case "google", "gemini":
		envKey = "GOOGLE_API_KEY"
	case "mistral":
		envKey = "MISTRAL_API_KEY"
	case "azure":
		envKey = "AZURE_API_KEY"
	case "cohere":
		envKey = "COHERE_API_KEY"
	default:
		// Try provider name directly
		envKey = fmt.Sprintf("%s_API_KEY", strings.ToUpper(provider))
	}
	return os.Getenv(envKey)
}

// =============================================================================
// Streaming JSON parsing
// =============================================================================

// parseStreamingJson parses streaming JSON by merging partial JSON with existing data
// Handles incremental tool argument streaming from providers like Anthropic and OpenAI
func parseStreamingJson(existing map[string]interface{}, partialJson string) map[string]interface{} {
	if existing == nil {
		existing = make(map[string]interface{})
	}

	// Try to parse the accumulated JSON
	accumulated := partialJson
	if len(existing) > 0 {
		// Merge existing with new partial
		existingJson, _ := json.Marshal(existing)
		accumulated = string(existingJson) + partialJson
	}

	// Attempt to parse
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(accumulated), &result); err == nil {
		return result
	}

	// If parsing failed, try to handle partial object
	if strings.HasPrefix(strings.TrimSpace(partialJson), "{") {
		// For partial JSON, keep existing and return it
		// The partial will be accumulated on next call
		return existing
	}

	return existing
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

// toClaudeCodeName converts a tool name to Claude Code format
func toClaudeCodeName(name string) string {
	// Remove common prefixes and convert to lowercase
	re := regexp.MustCompile(`^__`)
	name = re.ReplaceAllString(name, "")

	re = regexp.MustCompile(`[^a-zA-Z0-9_]`)
	name = re.ReplaceAllString(name, "_")

	return strings.ToLower(name)
}

// fromClaudeCodeName converts from Claude Code format to original tool name
func fromClaudeCodeName(name string) string {
	// This is a simplified version - actual implementation may need
	// to track original names separately
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
