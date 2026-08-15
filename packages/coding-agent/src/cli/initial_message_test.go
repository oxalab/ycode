package cli

import (
	"testing"

	llm "ycode/packages/llm/src"
)

// build runs BuildInitialMessage against a fresh Args populated with the given
// messages and returns the result plus the (mutated) remaining messages.
func build(messages []string, fileText string, fileImages []*llm.ImageContent, stdin string) (InitialMessageResult, []string) {
	args := &Args{Messages: messages}
	res := BuildInitialMessage(InitialMessageInput{
		Parsed:       args,
		FileText:     fileText,
		FileImages:   fileImages,
		StdinContent: stdin,
	})
	return res, args.Messages
}

func TestBuildInitialMessage_ConcatenatesAll(t *testing.T) {
	// stdin + fileText + first message, concatenated in that order.
	res, remaining := build(
		[]string{"third"},
		"<file>second</file>",
		nil,
		"first",
	)
	want := "first<file>second</file>third"
	if res.InitialMessage != want {
		t.Errorf("InitialMessage = %q, want %q", res.InitialMessage, want)
	}
	if len(remaining) != 0 {
		t.Errorf("remaining = %v, want empty after shift", remaining)
	}
}

func TestBuildInitialMessage_FirstMessageShifted(t *testing.T) {
	res, remaining := build([]string{"keep1", "keep2", "keep3"}, "", nil, "")
	if res.InitialMessage != "keep1" {
		t.Errorf("InitialMessage = %q, want keep1", res.InitialMessage)
	}
	if len(remaining) != 2 || remaining[0] != "keep2" || remaining[1] != "keep3" {
		t.Errorf("remaining = %v, want [keep2 keep3]", remaining)
	}
}

func TestBuildInitialMessage_EmptyWhenNothingProvided(t *testing.T) {
	res, _ := build(nil, "", nil, "")
	if res.InitialMessage != "" {
		t.Errorf("InitialMessage = %q, want empty", res.InitialMessage)
	}
	if res.Images != nil {
		t.Errorf("Images = %v, want nil", res.Images)
	}
}

func TestBuildInitialMessage_ImagesPassedThrough(t *testing.T) {
	imgs := []*llm.ImageContent{{Type: "image", Data: "abc", MimeType: "image/png"}}
	res, _ := build([]string{"msg"}, "text", imgs, "")
	if len(res.Images) != 1 || res.Images[0] != imgs[0] {
		t.Errorf("Images = %v, want the input images", res.Images)
	}
	// text + message are both present → concatenated.
	if res.InitialMessage != "textmsg" {
		t.Errorf("InitialMessage = %q, want textmsg", res.InitialMessage)
	}
}

func TestBuildInitialMessage_NilParsed(t *testing.T) {
	// A nil Parsed must not panic and must produce an empty message.
	res := BuildInitialMessage(InitialMessageInput{
		Parsed:       nil,
		FileText:     "only file",
		StdinContent: "",
	})
	if res.InitialMessage != "only file" {
		t.Errorf("InitialMessage = %q, want 'only file'", res.InitialMessage)
	}
}
