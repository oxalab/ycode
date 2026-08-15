package cli

// This file combines stdin content, @file text, and the first CLI positional
// message into a single initial prompt for non-interactive mode. It is the Go
// port of reference/coding-agent/src/cli/initial-message.ts.

import (
	"strings"

	llm "ycode/packages/llm/src"
)

// InitialMessageInput mirrors the TS InitialMessageInput.
//
// Parsed is a *Args pointer (not a value) so that consuming the first positional
// message — the TS `parsed.messages.shift()` — mutates the caller's Args. The
// remaining messages stay queued on Parsed.Messages for the caller to use (e.g.
// as follow-up turns in interactive mode).
type InitialMessageInput struct {
	Parsed       *Args
	FileText     string
	FileImages   []*llm.ImageContent
	StdinContent string
}

// InitialMessageResult mirrors the TS InitialMessageResult. The TS "optional"
// fields (initialMessage?/initialImages?) map to Go zero values: an empty
// InitialMessage and a nil Images slice both mean "absent". Consumers treat them
// as falsy, exactly as the TS undefined returns.
type InitialMessageResult struct {
	InitialMessage string
	Images         []*llm.ImageContent
}

// BuildInitialMessage concatenates stdin content, @file text, and the first CLI
// positional message (in that order) into the initial prompt, and pops that
// first message off Parsed.Messages (TS buildInitialMessage).
//
// Deviation from the plan sketch ("BuildInitialMessage(args, stdinContent)"):
// the faithful port takes an InitialMessageInput struct bundling parsed args,
// @file text, @file images, and stdin content — matching the reference's
// destructure signature and the outputs of ParseArgs (19.2) and
// ProcessFileArguments (19.3).
func BuildInitialMessage(input InitialMessageInput) InitialMessageResult {
	var parts []string
	if input.StdinContent != "" {
		parts = append(parts, input.StdinContent)
	}
	if input.FileText != "" {
		parts = append(parts, input.FileText)
	}
	// Consume the first positional message (TS: parsed.messages[0] + shift).
	if input.Parsed != nil && len(input.Parsed.Messages) > 0 {
		parts = append(parts, input.Parsed.Messages[0])
		input.Parsed.Messages = input.Parsed.Messages[1:]
	}

	result := InitialMessageResult{}
	if len(parts) > 0 {
		result.InitialMessage = strings.Join(parts, "")
	}
	if len(input.FileImages) > 0 {
		result.Images = input.FileImages
	}
	return result
}
