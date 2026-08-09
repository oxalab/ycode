package modes

import (
	"encoding/json"
	"strings"
	"testing"

	llm "ycode/packages/llm/src"
)

// mustRoundTrip marshals v, requires the raw JSON to contain each wantKey
// substring, then unmarshals into a fresh T and returns it.
func mustRoundTrip[T any](t *testing.T, v T, wantKeys ...string) T {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(data)
	for _, k := range wantKeys {
		if !strings.Contains(raw, k) {
			t.Errorf("wire JSON missing %q: %s", k, raw)
		}
	}
	var got T
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

// TestRpcCommand_WireNames verifies the camelCase wire field names match the TS
// literals exactly (guards against json-tag typos) for two representative
// commands, and that Type/Message/Provider/ModelID round-trip.
func TestRpcCommand_WireNames(t *testing.T) {
	prompt := RpcCommand{
		ID:                "r1",
		Type:              CmdPrompt,
		Message:           "hello",
		StreamingBehavior: StreamingBehaviorFollowUp,
	}
	got := mustRoundTrip(t, prompt, `"type":"prompt"`, `"streamingBehavior":"followUp"`, `"message":"hello"`)
	if got.Type != CmdPrompt || got.Message != "hello" || got.StreamingBehavior != StreamingBehaviorFollowUp {
		t.Errorf("round-trip = %+v", got)
	}

	setModel := RpcCommand{Type: CmdSetModel, Provider: "openai", ModelID: "gpt-4o"}
	got2 := mustRoundTrip(t, setModel, `"type":"set_model"`, `"provider":"openai"`, `"modelId":"gpt-4o"`)
	if got2.Provider != "openai" || got2.ModelID != "gpt-4o" {
		t.Errorf("set_model round-trip = %+v", got2)
	}

	// id:"" and unset optional fields are omitted (omitempty).
	if bare, _ := json.Marshal(RpcCommand{Type: CmdAbort}); strings.Contains(string(bare), `"id"`) {
		t.Errorf("empty id should be omitted: %s", bare)
	}
}

// TestRpcSessionState_RoundTrip verifies the state snapshot's wire names +
// round-trip, including a populated Model.
func TestRpcSessionState_RoundTrip(t *testing.T) {
	st := RpcSessionState{
		Model:                 &llm.Model[llm.Api]{ID: "gpt-4o", Provider: "openai"},
		ThinkingLevel:         llm.ThinkingLevel("high"),
		IsStreaming:           true,
		IsCompacting:          false,
		SteeringMode:          ModeOneAtATime,
		FollowUpMode:          ModeAll,
		SessionID:             "abc",
		AutoCompactionEnabled: true,
		MessageCount:          5,
		PendingMessageCount:   1,
	}
	got := mustRoundTrip(t, st,
		`"thinkingLevel":"high"`, `"steeringMode":"one-at-a-time"`, `"followUpMode":"all"`,
		`"sessionId":"abc"`, `"autoCompactionEnabled":true`, `"messageCount":5`,
		`"pendingMessageCount":1`)
	if got.ThinkingLevel != "high" || got.SteeringMode != ModeOneAtATime || got.MessageCount != 5 {
		t.Errorf("state round-trip = %+v", got)
	}
	if got.Model == nil || got.Model.ID != "gpt-4o" {
		t.Errorf("model round-trip = %+v", got.Model)
	}
}

// TestRpcResponse_Envelope verifies success (with/without data) and error
// envelopes, plus the helper constructors.
func TestRpcResponse_Envelope(t *testing.T) {
	// Success without data → no "data" key.
	noData := NewSuccessResponse("r1", CmdAbort, nil)
	raw, _ := json.Marshal(noData)
	if !strings.Contains(string(raw), `"success":true`) || strings.Contains(string(raw), `"data"`) {
		t.Errorf("no-data success envelope: %s", raw)
	}

	// Success with data.
	withData := NewSuccessResponse("r2", CmdExportHTML, map[string]string{"path": "/o.html"})
	got := mustRoundTrip(t, withData, `"command":"export_html"`, `"data":{"path":"/o.html"}`)
	if !got.Success {
		t.Errorf("success envelope lost Success: %+v", got)
	}

	// Error envelope.
	errResp := NewErrorResponse("r3", CmdBash, "boom")
	gotE := mustRoundTrip(t, errResp, `"success":false`, `"error":"boom"`, `"command":"bash"`)
	if gotE.Success || gotE.Error != "boom" {
		t.Errorf("error envelope = %+v", gotE)
	}
}

// TestRpcSlashCommand_WireNames verifies source/sourceInfo/description wire shape.
func TestRpcSlashCommand_WireNames(t *testing.T) {
	cmd := RpcSlashCommand{
		Name:        "plan",
		Description: "Plan mode",
		Source:      SourceExtension,
		SourceInfo:  map[string]string{"path": "/x"},
	}
	got := mustRoundTrip(t, cmd, `"name":"plan"`, `"source":"extension"`, `"sourceInfo":{"path":"/x"}`)
	if got.Source != SourceExtension {
		t.Errorf("source round-trip = %q", got.Source)
	}
}

// TestRpcExtensionUIRequest_WireNames verifies a couple of method variants use the
// right discriminator + fields (notify + setWidget).
func TestRpcExtensionUIRequest_WireNames(t *testing.T) {
	notify := RpcExtensionUIRequest{Type: ExtUIRequestType, ID: "u1", Method: MethodNotify, Message: "hi", NotifyType: NotifyWarning}
	got := mustRoundTrip(t, notify, `"type":"extension_ui_request"`, `"method":"notify"`, `"notifyType":"warning"`)
	if got.Method != MethodNotify || got.NotifyType != NotifyWarning {
		t.Errorf("notify round-trip = %+v", got)
	}

	widget := RpcExtensionUIRequest{
		Type: ExtUIRequestType, ID: "u2", Method: MethodSetWidget,
		WidgetKey: "k", WidgetLines: []string{"a", "b"}, WidgetPlacement: WidgetPlacementBelowEditor,
	}
	gotW := mustRoundTrip(t, widget, `"method":"setWidget"`, `"widgetKey":"k"`, `"widgetPlacement":"belowEditor"`)
	if len(gotW.WidgetLines) != 2 || gotW.WidgetPlacement != WidgetPlacementBelowEditor {
		t.Errorf("widget round-trip = %+v", gotW)
	}
}

// TestRpcExtensionUIResponse_ConfirmedDistinguishesFalse verifies Confirmed is
// *bool so a confirm:false reply survives (a plain bool + omitempty would drop
// it and make false indistinguishable from absent).
func TestRpcExtensionUIResponse_ConfirmedDistinguishesFalse(t *testing.T) {
	// confirm:false → marshals "confirmed":false, unmarshals to a non-nil *bool
	// pointing at false.
	falseConfirm := RpcExtensionUIResponse{Type: ExtUIResponseType, ID: "u", Confirmed: boolPtr(false)}
	raw, err := json.Marshal(falseConfirm)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"confirmed":false`) {
		t.Errorf("confirm:false was dropped: %s", raw)
	}
	var got RpcExtensionUIResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Confirmed == nil || *got.Confirmed != false {
		t.Errorf("Confirmed = %v, want *false", got.Confirmed)
	}

	// value reply → Confirmed stays nil.
	valReply := RpcExtensionUIResponse{Type: ExtUIResponseType, ID: "u", Value: "answer"}
	gotV := mustRoundTrip(t, valReply, `"value":"answer"`)
	if gotV.Confirmed != nil {
		t.Errorf("value reply should have nil Confirmed: %+v", gotV)
	}

	// cancelled reply.
	cancel := RpcExtensionUIResponse{Type: ExtUIResponseType, ID: "u", Cancelled: true}
	gotC := mustRoundTrip(t, cancel, `"cancelled":true`)
	if !gotC.Cancelled {
		t.Errorf("cancelled round-trip lost Cancelled")
	}
}

func boolPtr(b bool) *bool { return &b }
