package components

import (
	"strings"
	"time"

	"ycode/packages/tui"
)

// LoaderIndicatorOptions configures a Loader's animation.
type LoaderIndicatorOptions struct {
	Frames    []string
	IntervalMs int
}

var defaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const defaultLoaderInterval = 80

// Loader is an animated spinner + message. It embeds Text and prepends a blank
// line on render.
type Loader struct {
	*Text
	tui *tui.TUI

	frames                 []string
	interval               time.Duration
	currentFrame           int
	ticker                 *time.Ticker
	done                   chan struct{}
	spinnerColor           tui.StyleFunc
	messageColor           tui.StyleFunc
	message                string
	renderIndicatorVerbatim bool
}

// NewLoader returns a started Loader.
func NewLoader(t *tui.TUI, spinnerColor, messageColor tui.StyleFunc, message string, indicator *LoaderIndicatorOptions) *Loader {
	if message == "" {
		message = "Loading..."
	}
	l := &Loader{
		Text:          NewText("", 1, 0, nil),
		tui:           t,
		frames:        append([]string(nil), defaultFrames...),
		interval:      time.Duration(defaultLoaderInterval) * time.Millisecond,
		spinnerColor:  spinnerColor,
		messageColor:  messageColor,
		message:       message,
	}
	l.SetIndicator(indicator)
	return l
}

// Render prepends a blank line to the text render.
func (l *Loader) Render(width int) []string {
	return append([]string{""}, l.Text.Render(width)...)
}

// Start begins the animation. It sets the initial frame WITHOUT calling
// RequestRender, so it is safe to invoke under the engine's render lock (e.g.
// from inside Mutate). The ticker goroutine started by restartAnimation calls
// updateDisplay (which does request renders) from outside the lock.
func (l *Loader) Start() {
	l.renderText()
	l.restartAnimation()
}

// Stop stops the animation.
func (l *Loader) Stop() {
	if l.ticker != nil {
		l.ticker.Stop()
		l.ticker = nil
	}
	if l.done != nil {
		close(l.done)
		l.done = nil
	}
}

// SetMessage updates the message WITHOUT calling RequestRender, so it is safe
// under the render lock. The ticker re-renders on its next tick.
func (l *Loader) SetMessage(message string) {
	l.message = message
	l.renderText()
}

// SetIndicator changes the animation frames/interval and restarts.
func (l *Loader) SetIndicator(indicator *LoaderIndicatorOptions) {
	l.renderIndicatorVerbatim = indicator != nil
	if indicator != nil && indicator.Frames != nil {
		l.frames = append([]string(nil), indicator.Frames...)
	} else {
		l.frames = append([]string(nil), defaultFrames...)
	}
	if indicator != nil && indicator.IntervalMs > 0 {
		l.interval = time.Duration(indicator.IntervalMs) * time.Millisecond
	} else {
		l.interval = time.Duration(defaultLoaderInterval) * time.Millisecond
	}
	l.currentFrame = 0
	l.Start()
}

func (l *Loader) restartAnimation() {
	l.Stop()
	if len(l.frames) <= 1 {
		return
	}
	l.done = make(chan struct{})
	l.ticker = time.NewTicker(l.interval)
	// Capture the channels in locals so Stop() (which nils l.ticker) can't cause
	// a nil-deref inside the select below while this goroutine is mid-select.
	ticker := l.ticker
	done := l.done
	go func() {
		for {
			select {
			case <-ticker.C:
				l.currentFrame = (l.currentFrame + 1) % len(l.frames)
				l.updateDisplay()
			case <-done:
				return
			}
		}
	}()
}

func (l *Loader) updateDisplay() {
	l.renderText()
	if l.tui != nil {
		l.tui.RequestRender()
	}
}

// renderText writes the current spinner frame + message into the embedded Text.
// It deliberately does NOT call RequestRender, so Start/SetMessage are safe to
// invoke under the engine's render lock (the ticker goroutine calls updateDisplay,
// which does request a render, from outside the lock).
func (l *Loader) renderText() {
	frame := ""
	if l.currentFrame < len(l.frames) {
		frame = l.frames[l.currentFrame]
	}
	rendered := frame
	if !l.renderIndicatorVerbatim && l.spinnerColor != nil {
		rendered = l.spinnerColor(frame)
	}
	indicator := ""
	if frame != "" {
		indicator = rendered + " "
	}
	msg := l.message
	if l.messageColor != nil {
		msg = l.messageColor(l.message)
	}
	l.Text.SetText(indicator + msg)
}

// ensure strings import is used (for future message shaping).
var _ = strings.TrimSpace
