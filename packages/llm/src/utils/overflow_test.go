package utils

import (
	"testing"

	llm "ycode/packages/llm/src"
)

func overflowMessage(stopReason llm.StopReason, errorMessage string, input, output, cacheRead int) llm.AssistantMessage {
	return llm.AssistantMessage{
		StopReason:   stopReason,
		ErrorMessage: errorMessage,
		Usage:        llm.Usage{Input: input, Output: output, CacheRead: cacheRead},
	}
}

// The Together AI pattern was missing from the Go port (P15).
func TestOverflowTogetherAIPattern(t *testing.T) {
	msg := overflowMessage(llm.StopReasonError,
		"The input (5000 tokens) is longer than the model's context length (4096 tokens).", 0, 0, 0)
	if !IsContextOverflow(msg, 4096) {
		t.Fatal("apostrophe variant not detected")
	}
	msg.ErrorMessage = "The input (5000 tokens) is longer than the models context length (4096 tokens)."
	if !IsContextOverflow(msg, 4096) {
		t.Fatal("no-apostrophe variant not detected")
	}
	msg.ErrorMessage = "input (500 tokens) is longer than the models context length (bad)"
	if IsContextOverflow(msg, 4096) {
		t.Fatal("malformed Together message must not match (requires the token counts)")
	}
}

// Case 3 (Xiaomi MiMo silent truncation): stopReason "length" + output 0 +
// input+cacheRead filling at least 99% of the context window.
func TestOverflowLengthStopCase(t *testing.T) {
	at := overflowMessage(llm.StopReasonLength, "", 4060, 0, 0) // >= 4096*0.99 = 4055.04
	if !IsContextOverflow(at, 4096) {
		t.Fatal("length-stop overflow not detected at 0.99 threshold")
	}
	cacheVariant := overflowMessage(llm.StopReasonLength, "", 2000, 0, 2060)
	if !IsContextOverflow(cacheVariant, 4096) {
		t.Fatal("cacheRead must count toward the filled window")
	}
	justUnder := overflowMessage(llm.StopReasonLength, "", 4055, 0, 0) // 4055 < 4055.04
	if IsContextOverflow(justUnder, 4096) {
		t.Fatal("below-threshold input must not be flagged")
	}
	withOutput := overflowMessage(llm.StopReasonLength, "", 4096, 5, 0)
	if IsContextOverflow(withOutput, 4096) {
		t.Fatal("length stop with output tokens is a real length stop, not overflow")
	}
	noWindow := overflowMessage(llm.StopReasonLength, "", 4096, 0, 0)
	if IsContextOverflow(noWindow, 0) {
		t.Fatal("no contextWindow passed: length-stop detection must be skipped")
	}
}

// The Throttling/Service-unavailable prefixes exclude a message even when it
// also matches an overflow pattern (e.g. "too many tokens").
func TestOverflowNonOverflowPrecedence(t *testing.T) {
	throttled := overflowMessage(llm.StopReasonError,
		"Throttling error: Too many tokens, please wait before trying again.", 0, 0, 0)
	if IsContextOverflow(throttled, 128000) {
		t.Fatal("Throttling error prefix must win over the too-many-tokens pattern")
	}
	rateLimited := overflowMessage(llm.StopReasonError, "rate limit exceeded", 0, 0, 0)
	if IsContextOverflow(rateLimited, 128000) {
		t.Fatal("rate limit errors are not overflow")
	}
	plain := overflowMessage(llm.StopReasonError, "prompt is too long: 213462 tokens > 200000 maximum", 0, 0, 0)
	if !IsContextOverflow(plain, 200000) {
		t.Fatal("anthropic prompt-is-too-long must be detected")
	}
}

// Case 2 (z.ai silent overflow): successful stop but usage beyond the window.
func TestOverflowSilentUsageCase(t *testing.T) {
	msg := overflowMessage(llm.StopReasonStop, "", 130000, 10, 0)
	if !IsContextOverflow(msg, 128000) {
		t.Fatal("input beyond the context window on a successful stop must be flagged")
	}
	within := overflowMessage(llm.StopReasonStop, "", 127000, 10, 0)
	if IsContextOverflow(within, 128000) {
		t.Fatal("input within the window must not be flagged")
	}
}
