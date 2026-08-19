package main

// runtime_pi.go is the YCODE_TUI=pi launch path: it builds the pi-tui-based
// interactive Orchestrator and runs it. The Bubble-Tea TUI (launchInteractive in
// runtime.go) remains the default; this is selected only when the env gate is
// set, so the two coexist until the pi-tui TUI reaches feature parity.

import (
	cli "ycode/packages/coding-agent/src/cli"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/interactive"
)

// launchInteractivePi runs the pi-tui interactive TUI. It intentionally mirrors
// launchInteractive's signature so the gate in launchInteractive can dispatch to
// either. Only runtime + settingsManager are consumed in P0.
func launchInteractivePi(
	_ cli.Args,
	runtime *core.AgentSessionRuntime,
	_ cli.InitialMessageResult,
	_ string,
	_ []string,
	settingsManager *core.SettingsManager,
) {
	orch := interactive.NewOrchestrator(interactive.Options{
		Runtime:  runtime,
		Settings: settingsManager,
	})
	orch.Run()
}
