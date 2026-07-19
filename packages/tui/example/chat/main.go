// Package main is a port of reference/tui/test/chat-simple.ts — an end-to-end
// smoke of the engine driving Markdown + Loader + Editor + autocomplete together.
//
// Run:  go run ./packages/tui/example/chat
package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
)

func main() {
	terminal := tui.NewProcessTerminal()
	app := tui.NewTUI(terminal)

	// Welcome banner.
	app.AddChild(components.NewText(
		"Welcome to Simple Chat!\n\n"+
			"Type your messages below. Type '/' for commands. Press Ctrl+C to exit.",
		0, 0, nil,
	))

	// Editor with autocomplete (slash commands + @file completion).
	editor := components.NewEditor(app, defaultEditorTheme)

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	commands := []tui.CommandSource{
		{Slash: &tui.SlashCommand{Name: "delete", Description: "Delete the last message"}},
		{Slash: &tui.SlashCommand{Name: "clear", Description: "Clear all messages"}},
	}
	editor.SetAutocompleteProvider(tui.NewCombinedAutocompleteProvider(commands, cwd, ""))
	app.AddChild(editor)
	app.SetFocus(editor)

	// Track whether a bot response is in flight.
	var responding atomic.Bool

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	responses := []string{
		"That's interesting! Tell me more.",
		"I see what you mean.",
		"Fascinating perspective!",
		"Could you elaborate on that?",
		"That makes sense to me.",
		"I hadn't thought of it that way.",
		"Great point!",
		"Thanks for sharing that.",
	}

	editor.OnSubmit = func(value string) {
		trimmed := strings.TrimSpace(value)

		// /delete — remove the most recent message (second-to-last child).
		// Tree layout: [welcome, ...messages, editor].
		if trimmed == "/delete" {
			if len(app.Children) > 3 {
				idx := len(app.Children) - 2
				app.Children = append(app.Children[:idx], app.Children[idx+1:]...)
			}
			return
		}

		// /clear — remove all but the last message.
		if trimmed == "/clear" {
			count := len(app.Children) - 3
			if count > 0 {
				app.Children = append(app.Children[:2], app.Children[2+count:]...)
			}
			return
		}

		if trimmed == "" {
			return
		}

		// Prevent overlapping responses.
		if !responding.CompareAndSwap(false, true) {
			return
		}
		editor.DisableSubmit = true

		// Insert the user message synchronously (we hold the engine render lock
		// here, so mutate Children directly; the engine re-renders after
		// HandleInput returns — do NOT call RequestRender, it would self-deadlock).
		userMsg := components.NewMarkdown(value, 1, 1, defaultMarkdownTheme, nil)
		app.Children = insertBeforeLast(app.Children, userMsg)

		// Defer the bot reply to a goroutine: Loader construction and tree
		// mutation must happen OUTSIDE the render lock (creating a Loader calls
		// RequestRender, which would deadlock under the held lock).
		go func() {
			loader := components.NewLoader(app, cyan, dim, "Thinking...", nil)
			app.InsertChildBeforeLast(loader) // [welcome, userMsg, loader, editor]

			time.Sleep(time.Second)

			loader.Stop()
			app.RemoveChildSafe(loader)

			botMsg := components.NewMarkdown(responses[rng.Intn(len(responses))], 1, 1, defaultMarkdownTheme, nil)
			app.InsertChildBeforeLast(botMsg)

			editor.DisableSubmit = false
			responding.Store(false)
		}()
	}

	// The editor swallows Ctrl+C (tui.input.copy). Honor the "Ctrl+C to exit"
	// banner via an input listener that defers Stop+exit to a goroutine (Stop
	// takes the render lock, which handleInput already holds).
	app.AddInputListener(func(data string) *tui.InputListenerResult {
		if tui.MatchesKey(data, "ctrl+c") {
			go func() {
				app.Stop()
				fmt.Println("\nExiting...")
				os.Exit(0)
			}()
			return &tui.InputListenerResult{Consume: true}
		}
		return nil
	})

	app.Start()

	// Start() returns immediately (input is driven by a reader goroutine); block
	// the main goroutine until the process exits.
	select {}
}

// insertBeforeLast inserts child immediately before the slice's final element.
func insertBeforeLast(children []tui.Component, child tui.Component) []tui.Component {
	n := len(children)
	if n == 0 {
		return append(children, child)
	}
	children = append(children, nil)
	copy(children[n+1:], children[n:])
	children[n] = child
	return children
}
