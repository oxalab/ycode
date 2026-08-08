package interactive

// commands_extra.go ports the remaining slash commands the reference handles
// but the Go port was missing:
//   - /import    handleImportCommand    (:4903) — JSONL session import + confirm
//   - /share     handleShareCommand     (:4953) — export to HTML + secret gist via gh
//   - /changelog handleChangelogCommand (:5119) — render CHANGELOG.md entries
//   - /debug     handleDebugCommand     (:5289) — dump rendered lines + widths + msgs to debug log
//
// Sources: reference/coding-agent/src/modes/interactive/interactive-mode.ts.
// Each handler runs from routeCommand (slash.go) under the engine render lock;
// blocking work launches a goroutine where Mutate/RequestRender are safe.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ycode/packages/coding-agent/src/config"
	"ycode/packages/tui"
	"ycode/packages/tui/components"
)

// --- /import ---

// cmdImport imports a session from a JSONL file (reference handleImportCommand).
// Confirms before replacing the current session; runs on a goroutine since
// ImportFromJsonl does file I/O and Confirm blocks on user input.
func (o *Orchestrator) cmdImport(rest string) {
	path := parsePathArg(rest)
	if path == "" {
		o.addNotice(o.th.Fg("error", "Usage: /import <path.jsonl>"))
		return
	}
	go func() {
		// Confirm via the wired UI context (matches reference's confirm dialog).
		var confirmed bool
		if o.uiCtx.Confirm != nil {
			confirmed, _ = o.uiCtx.Confirm(fmt.Sprintf("Replace current session with %s?", path), 0)
		}
		if !confirmed {
			o.engine.Mutate(func() { o.addNotice(o.th.Fg("dim", "Import cancelled")) })
			o.engine.RequestRender()
			return
		}
		result, err := o.runtime.ImportFromJsonl(path, "")
		if err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to import session: "+err.Error()))
			})
			o.engine.RequestRender()
			return
		}
		if result == nil || result.Cancelled {
			o.engine.Mutate(func() { o.addNotice(o.th.Fg("dim", "Import cancelled")) })
			o.engine.RequestRender()
			return
		}
		o.engine.Mutate(func() {
			o.addNotice(o.th.Fg("dim", "Session imported from: "+path))
		})
		o.engine.RequestRender()
	}()
}

// --- /share ---

// cmdShare exports the session to HTML and creates a secret GitHub gist via gh
// (reference handleShareCommand). Checks `gh auth status` first; runs entirely
// on a goroutine since it shells out to gh.
func (o *Orchestrator) cmdShare() {
	go func() {
		// Check gh availability + auth.
		if _, err := exec.LookPath("gh"); err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "GitHub CLI (gh) is not installed. Install it from https://cli.github.com/"))
			})
			o.engine.RequestRender()
			return
		}
		if err := exec.Command("gh", "auth", "status").Run(); err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "GitHub CLI is not logged in. Run 'gh auth login' first."))
			})
			o.engine.RequestRender()
			return
		}

		// Export to a temp HTML file.
		tmpFile := filepath.Join(os.TempDir(), "ycode-session.html")
		if _, err := o.session.ExportToHtml(tmpFile); err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to export session: "+err.Error()))
			})
			o.engine.RequestRender()
			return
		}

		// Create a secret gist via gh.
		gistOut, err := exec.Command("gh", "gist", "create", tmpFile, "--secret").CombinedOutput()
		_ = os.Remove(tmpFile) // cleanup temp
		if err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to create gist: "+strings.TrimSpace(string(gistOut))))
			})
			o.engine.RequestRender()
			return
		}
		gistURL := strings.TrimSpace(string(gistOut))
		o.engine.Mutate(func() {
			o.addNotice(o.th.Fg("dim", "Session shared: "+gistURL))
		})
		o.engine.RequestRender()
	}()
}

// --- /changelog ---

// changelogVersionRegex matches "## [1.2.3]" or "## 1.2.3" headers in a
// CHANGELOG.md (same regex as reference parseChangelog).
var changelogVersionRegex = regexp.MustCompile(`##\s+\[?(\d+)\.(\d+)\.(\d+)\]?`)

// changelogEntry is one parsed version section (matches reference ChangelogEntry).
type changelogEntry struct {
	major, minor, patch int
	content             string
}

// parseChangelog reads CHANGELOG.md and splits it into version-keyed entries.
// Faithful port of reference utils/changelog.ts:parseChangelog.
func parseChangelog(path string) []changelogEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var entries []changelogEntry
	var currentLines []string
	var currentMajor, currentMinor, currentPatch int
	currentValid := false

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if currentValid && len(currentLines) > 0 {
				entries = append(entries, changelogEntry{
					major:   currentMajor,
					minor:   currentMinor,
					patch:   currentPatch,
					content: strings.TrimSpace(strings.Join(currentLines, "\n")),
				})
			}
			if m := changelogVersionRegex.FindStringSubmatch(line); m != nil {
				currentMajor = atoiSafe(m[1])
				currentMinor = atoiSafe(m[2])
				currentPatch = atoiSafe(m[3])
				currentLines = []string{line}
				currentValid = true
			} else {
				currentValid = false
				currentLines = nil
			}
		} else if currentValid {
			currentLines = append(currentLines, line)
		}
	}
	if currentValid && len(currentLines) > 0 {
		entries = append(entries, changelogEntry{
			major:   currentMajor,
			minor:   currentMinor,
			patch:   currentPatch,
			content: strings.TrimSpace(strings.Join(currentLines, "\n")),
		})
	}
	return entries
}

// cmdChangelog renders the CHANGELOG.md in the chat (reference
// handleChangelogCommand). Entries are reversed (newest first) and joined.
func (o *Orchestrator) cmdChangelog() {
	changelogPath := config.GetChangelogPath()
	entries := parseChangelog(changelogPath)

	var markdown string
	if len(entries) > 0 {
		// Reverse (newest first).
		reversed := make([]string, len(entries))
		for i, e := range entries {
			reversed[len(entries)-1-i] = e.content
		}
		markdown = strings.Join(reversed, "\n\n")
	} else {
		markdown = "No changelog entries found."
	}

	// Append a bordered "What's New" block with the markdown content to the chat.
	o.chat.AddChild(components.NewSpacer(1))
	o.chat.AddChild(newDynamicBorder(fgStyle(o.th, "border")))
	o.chat.AddChild(components.NewText(o.th.Bold(o.th.Fg("accent", "What's New")), 1, 0, nil))
	o.chat.AddChild(components.NewSpacer(1))
	o.chat.AddChild(components.NewMarkdown(markdown, 1, 1, buildMarkdownTheme(o.th), nil))
	o.chat.AddChild(newDynamicBorder(fgStyle(o.th, "border")))
	o.engine.RequestRender()
}

// --- /debug ---

// cmdDebug writes the current rendered lines (with visible widths) + the agent
// messages as JSONL to the debug log path (reference handleDebugCommand).
// Useful for diagnosing rendering issues.
func (o *Orchestrator) cmdDebug() {
	width := o.engine.Terminal.Columns()
	height := o.engine.Terminal.Rows()

	// Render the current tree to get all lines (the engine's Container.Render
	// produces the same lines the differential renderer compares).
	lines := o.engine.Render(width)
	debugPath := config.GetDebugLogPath()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Debug output at %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
	b.WriteString(fmt.Sprintf("Terminal: %dx%d\n", width, height))
	b.WriteString(fmt.Sprintf("Total lines: %d\n\n", len(lines)))
	b.WriteString("=== All rendered lines with visible widths ===\n")
	for i, line := range lines {
		vw := tui.VisibleWidth(line)
		escaped, _ := json.Marshal(line)
		b.WriteString(fmt.Sprintf("[%d] (w=%d) %s\n", i, vw, string(escaped)))
	}
	b.WriteString("\n=== Agent messages (JSONL) ===\n")
	for _, msg := range o.session.Agent.Messages() {
		j, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	b.WriteString("\n")

	// Ensure the directory exists, then write.
	_ = os.MkdirAll(filepath.Dir(debugPath), 0755)
	if err := os.WriteFile(debugPath, []byte(b.String()), 0644); err != nil {
		o.addNotice(o.th.Fg("error", "Failed to write debug log: "+err.Error()))
		return
	}
	o.addNotice(o.th.Fg("accent", "✓ Debug log written") + "\n" +
		o.th.Fg("muted", debugPath))
}

// --- helpers ---

// atoiSafe parses an int from a string, returning 0 on failure.
func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
