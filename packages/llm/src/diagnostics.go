package llm

/*
Port of pi/packages/ai/src/utils/diagnostics.ts — redacted message-level
diagnostics for provider/runtime failures and recoveries (TS types.ts:285
`AssistantMessage.diagnostics?`).
*/

import "time"

type DiagnosticErrorInfo struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
	Code    any    `json:"code,omitempty"` // string | number
}

type AssistantMessageDiagnostic struct {
	Type      string               `json:"type"`
	Timestamp int64                `json:"timestamp"`
	Error     *DiagnosticErrorInfo `json:"error,omitempty"`
	Details   map[string]any       `json:"details,omitempty"`
}

// FormatThrownValue is the TS formatThrownValue port (Error → message||name).
func FormatThrownValue(value error) string {
	if value == nil {
		return ""
	}
	if value.Error() != "" {
		return value.Error()
	}
	return "error"
}

// ExtractDiagnosticError is the TS extractDiagnosticError port.
func ExtractDiagnosticError(err error) DiagnosticErrorInfo {
	if err == nil {
		return DiagnosticErrorInfo{Name: "ThrownValue", Message: ""}
	}
	info := DiagnosticErrorInfo{
		Name:    "Error",
		Message: err.Error(),
	}
	type coder interface{ DiagnosticCode() any }
	if c, ok := err.(coder); ok {
		info.Code = c.DiagnosticCode()
	}
	type named interface{ DiagnosticName() string }
	if n, ok := err.(named); ok {
		info.Name = n.DiagnosticName()
	}
	return info
}

// CreateAssistantMessageDiagnostic is the TS createAssistantMessageDiagnostic port.
func CreateAssistantMessageDiagnostic(diagType string, err error, details map[string]any) AssistantMessageDiagnostic {
	info := ExtractDiagnosticError(err)
	return AssistantMessageDiagnostic{
		Type:      diagType,
		Timestamp: time.Now().UnixMilli(),
		Error:     &info,
		Details:   details,
	}
}

// AppendAssistantMessageDiagnostic is the TS appendAssistantMessageDiagnostic port.
func AppendAssistantMessageDiagnostic(message *AssistantMessage, diagnostic AssistantMessageDiagnostic) {
	message.Diagnostics = append(message.Diagnostics, diagnostic)
}
