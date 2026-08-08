package interactive

// header.go ports the built-in header from reference
// interactive-mode.ts:579-637 (verified — see mapping/10-chrome.md). It is a
// Container{ Spacer(1), ExpandableText(logo + instructions + onboarding),
// Spacer(1) }, or a single empty Text when startup is quiet.
//
// The instruction lines are keybinding hints resolved from the merged
// KeybindingsManager (installAppKeybindings), so they show real keys.

import (
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// asciiLogo is the multi-line ASCII art banner shown centered at the top of
// the header. Each entry is one line of the art.
var asciiLogo = []string{
	`█    █ ▄▀▀▀▀▄ ▄▀▀▀▀▄ █▀▀▀▀▄ ▄▀▀▀▀▀`,
	`▀ ▀▀▀█ █    ▄ █    █ █    █ █▀▀   `,
	`▀▀▀▀▀    ▀▀▀    ▀▀▀  ▀ ▀▀▀    ▀▀▀▀`,
}

// centeredLogo returns the ASCII art logo centered for the given terminal
// width, themed with the accent color. Each line is padded with leading
// spaces so the art block is horizontally centered.
func centeredLogo(th *theme.Theme, width int) string {
	if width < 1 {
		width = 80
	}
	// Find the widest line.
	maxW := 0
	for _, line := range asciiLogo {
		if w := tui.VisibleWidth(line); w > maxW {
			maxW = w
		}
	}
	// Center: (width - maxW) / 2 spaces of left padding.
	pad := (width - maxW) / 2
	if pad < 0 {
		pad = 0
	}
	indent := strings.Repeat(" ", pad)
	var b strings.Builder
	for i, line := range asciiLogo {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(th.Fg("accent", indent+line))
	}
	return b.String()
}

// centerLine left-pads s with the spaces needed to horizontally center it in
// `width` visible columns. ANSI codes (theme colors) don't count toward the
// visible width — tui.VisibleWidth strips them. Lines already at or beyond
// `width` are returned unchanged (no negative padding, no truncation).
func centerLine(s string, width int) string {
	if width < 1 {
		return s
	}
	visW := tui.VisibleWidth(s)
	if visW >= width {
		return s
	}
	pad := (width - visW) / 2
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// headerWidth returns the current terminal width for header centering, with a
// sane fallback. Read live inside render closures so resize tracks.
func (o *Orchestrator) headerWidth() int {
	if o.engine != nil && o.engine.Terminal != nil {
		if w := o.engine.Terminal.Columns(); w > 0 {
			return w
		}
	}
	return 80
}

// buildHeader builds headerContainer. When quiet, returns a Container with an
// empty Text. Sets o.headerExpandable for the app.tools.expand toggle.
func (o *Orchestrator) buildHeader() *tui.Container {
	c := &tui.Container{}

	// Quiet startup → minimal header (reference :633-637).
	if !o.verbose && o.settings != nil && o.settings.GetQuietStartup() {
		c.AddChild(components.NewText("", 0, 0, nil))
		return c
	}

	logo := o.th.Bold(o.th.Fg("accent", core.AppName)) + o.th.Fg("dim", " v"+core.Version)

	// Expanded instructions (reference :585-605), one hint per line.
	expandedInstructions := strings.Join([]string{
		keyHint(o.th, "app.interrupt", "to interrupt"),
		keyHint(o.th, "app.clear", "to clear"),
		rawKeyHint(o.th, keyText("app.clear")+" twice", "to exit"),
		keyHint(o.th, "app.exit", "to exit (empty)"),
		keyHint(o.th, "app.suspend", "to suspend"),
		keyHint(o.th, "tui.editor.deleteToLineEnd", "to delete to end"),
		keyHint(o.th, "app.thinking.cycle", "to cycle thinking level"),
		rawKeyHint(o.th, keyText("app.model.cycleForward")+"/"+keyText("app.model.cycleBackward"), "to cycle models"),
		keyHint(o.th, "app.model.select", "to select model"),
		keyHint(o.th, "app.tools.expand", "to expand tools"),
		keyHint(o.th, "app.thinking.toggle", "to expand thinking"),
		keyHint(o.th, "app.editor.external", "for external editor"),
		rawKeyHint(o.th, "/", "for commands"),
		rawKeyHint(o.th, "!", "to run bash"),
		rawKeyHint(o.th, "!!", "to run bash (no context)"),
		keyHint(o.th, "app.message.followUp", "to queue follow-up"),
		keyHint(o.th, "app.message.dequeue", "to edit all queued messages"),
		keyHint(o.th, appPasteImage, "to paste image"),
		rawKeyHint(o.th, "drop files", "to attach"),
	}, "\n")

	// Compact instructions (reference :606-612), joined by a muted " · ".
	compactInstructions := strings.Join([]string{
		keyHint(o.th, "app.interrupt", "interrupt"),
		rawKeyHint(o.th, keyText("app.clear")+"/"+keyText("app.exit"), "clear/exit"),
		rawKeyHint(o.th, "/", "commands"),
		rawKeyHint(o.th, "!", "bash"),
		keyHint(o.th, "app.tools.expand", "more"),
	}, o.th.Fg("muted", " · "))

	compactOnboarding := o.th.Fg("dim", "Press "+keyText("app.tools.expand")+" to show full startup help and loaded resources.")
	// Reference onboarding refers to the app by name ("Pi can explain…"); use
	// core.AppName (the app's self-reference) for the ycode build.
	onboarding := o.th.Fg("dim", core.AppName+" can explain its own features and look up its docs. Ask it how to use or extend "+core.AppName+".")

	// Width is read inside the closures so centering tracks terminal resize.
	// Logo, instructions, and onboarding are centered; expanded instructions
	// stay left-aligned (each line centered independently would look ragged
	// because hint lengths vary).
	o.headerExpandable = NewExpandableText(
		func() string {
			w := o.headerWidth()
			return centeredLogo(o.th, w) + "\n" +
				centerLine(logo, w) + "\n" +
				centerLine(compactInstructions, w) + "\n" +
				centerLine(compactOnboarding, w) + "\n\n" +
				centerLine(onboarding, w)
		},
		func() string {
			w := o.headerWidth()
			return centeredLogo(o.th, w) + "\n" +
				centerLine(logo, w) + "\n" +
				expandedInstructions + "\n\n" +
				centerLine(onboarding, w)
		},
		o.headerExpanded, // getStartupExpansionState — default false (collapsed)
		1, 0,
	)

	c.AddChild(components.NewSpacer(1))
	c.AddChild(o.headerExpandable)
	c.AddChild(components.NewSpacer(1))
	return c
}
