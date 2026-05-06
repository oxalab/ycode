package sdk

import (
	"context"
	"time"

	"ycode/packages/llm/src"
)

// ThinkingLevel: Represents the reasoning/intensity level for models that support it
type ThinkingLevel string

const (
	ThinkingOff 		ThinkingLevel = "off"
	ThinkingMinimal 	ThinkingLevel = "minimal"
	ThinkingLow 		ThinkingLevel = "low"
	ThinkingMedium 		ThinkingLevel = "medium"
	ThinkingHigh 		ThinkingLevel = "high"
	ThinkingXHigh 		ThinkingLevel = "xhigh"
)

// ToolExecutionMode: Defines how tool calls are executed
type ToolExecutionMode string