package oauth

import (
	"fmt"
	"html"
	"strings"
)

func escapeHTML(s string) string {
	return html.EscapeString(s)
}

func renderPage(title, heading, message string, details ...string) string {
	d := strings.Join(details, "")
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>%s</title>
  <style>
    :root {
      --text: #fafafa;
      --text-dim: #a1a1aa;
      --page-bg: #09090b;
      --font-sans: ui-sans-serif, system-ui, -apple-system, sans-serif;
      --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    }
    * { box-sizing: border-box; }
    html { color-scheme: dark; }
    body {
      margin: 0; min-height: 100vh;
      display: flex; align-items: center; justify-content: center;
      padding: 24px; background: var(--page-bg); color: var(--text);
      font-family: var(--font-sans); text-align: center;
    }
    main { width: 100%%; max-width: 560px; }
    h1 { margin: 0 0 10px; font-size: 28px; font-weight: 650; }
    p { margin: 0; line-height: 1.7; color: var(--text-dim); font-size: 15px; }
    .details {
      margin-top: 16px; font-family: var(--font-mono); font-size: 13px;
      color: var(--text-dim); white-space: pre-wrap; word-break: break-word;
    }
  </style>
</head>
<body>
  <main>
    <h1>%s</h1>
    <p>%s</p>
    %s
  </main>
</body>
</html>`, escapeHTML(title), escapeHTML(heading), escapeHTML(message), d)
}

// OAuthSuccessHTML: Returns an HTML page for successful OAuth completion
func OAuthSuccessHTML(message string) string {
	return renderPage("Authentication successful", "Authentication successful", message)
}

// OAuthErrorHTML: Returns an HTML page for OAuth Errors
func OAuthErrorHTML(message string, details ...string) string {
	escaped := ""
	for _, d := range details {
		if d != "" {
			escaped = fmt.Sprintf(`<div class="details">%s</div>`, escapeHTML(d))
			break
		}
	}
	return renderPage("Authentication failed", "Authentication failed", message, escaped)
}