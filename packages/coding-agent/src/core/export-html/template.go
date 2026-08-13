package exporthtml

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed template.html
var templateHTML string

//go:embed template.css
var templateCSS string

//go:embed template.js
var templateJS string

//go:embed vendor/marked.min.js
var markedJS string

//go:embed vendor/highlight.min.js
var highlightJS string

// SessionExportData is the JSON data embedded in the exported HTML.
type SessionExportData struct {
	Entries   []json.RawMessage `json:"entries"`
	Cwd       string            `json:"cwd"`
	SessionID string            `json:"sessionId"`
	AppName   string            `json:"appName"`
}

// RenderTemplate fills the HTML template with the session data and static assets.
func RenderTemplate(data SessionExportData) (string, error) {
	sessionJSON, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	html := templateHTML
	html = strings.Replace(html, "{{CSS}}", templateCSS, 1)
	html = strings.Replace(html, "{{SESSION_DATA}}", string(sessionJSON), 1)
	html = strings.Replace(html, "{{MARKED_JS}}", markedJS, 1)
	html = strings.Replace(html, "{{HIGHLIGHT_JS}}", highlightJS, 1)
	html = strings.Replace(html, "{{JS}}", templateJS, 1)

	return html, nil
}
