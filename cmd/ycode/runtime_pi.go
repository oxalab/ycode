package main

// runtime_pi.go is the pi-tui interactive launch path (the DEFAULT per the
// runtime.go gate); YCODE_TUI=bt opts into the legacy Bubble-Tea TUI
// (launchInteractive in runtime.go), kept until it is deleted after a soak.

import (
	"sync"

	cli "ycode/packages/coding-agent/src/cli"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/interactive"
	tools "ycode/packages/coding-agent/src/core/tools"
)

// launchInteractivePi runs the pi-tui interactive TUI. It intentionally mirrors
// launchInteractive's signature so the gate in launchInteractive can dispatch to
// either. Consumes runtime, settingsManager, and the CLI initial prompts
// (finding #33); modelFallback/migratedProviders are still ignored here.
func launchInteractivePi(
	parsed cli.Args,
	runtime *core.AgentSessionRuntime,
	initial cli.InitialMessageResult,
	_ string,
	_ []string,
	settingsManager *core.SettingsManager,
) {
	// C8: pre-fetch fd/rg concurrently, blocking and non-silent like the
	// reference interactive init (Promise.all over ensureTool) — downloads
	// land in ~/.ycode/agent/bin before the TUI takes over the console.
	fdPath := prefetchTools()

	orch := interactive.NewOrchestrator(interactive.Options{
		Runtime:         runtime,
		Settings:        settingsManager,
		InitialMessage:  initial.InitialMessage,
		InitialImages:   initial.Images,
		InitialMessages: parsed.Messages,
		FDPath:          fdPath,
	})
	orch.Run()
}

// prefetchTools ensures fd and rg are available (reference interactive-mode.ts
// init: both needed — fd for autocomplete, rg for the grep tool and bash
// commands) and returns the resolved fd path for the autocomplete provider.
func prefetchTools() string {
	var wg sync.WaitGroup
	fdCh := make(chan string, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		fdCh <- tools.EnsureTool("fd", false)
	}()
	go func() {
		defer wg.Done()
		tools.EnsureTool("rg", false)
	}()
	wg.Wait()
	return <-fdCh
}
