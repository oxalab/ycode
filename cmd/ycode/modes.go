package main

// This file dispatches the non-interactive run modes. print/json share the
// single-shot print runner (text vs JSON event stream); rpc runs the headless
// JSONL protocol. Each dispatches over the already-built AgentSessionRuntime and
// exits with the appropriate code, mirroring the TS main() mode branches
// (runPrintMode / runRpcMode).
//
// Deferred: the package/config CLI dispatcher (ycode install/remove/update/
// list/config) is still not intercepted — no handlePackageCommand exists.

import (
	"fmt"
	"os"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/modes"
	cli "ycode/packages/coding-agent/src/cli"
	llm "ycode/packages/llm/src"
)

// runPrintDispatch runs single-shot print mode (text or JSON event stream) over
// the runtime and exits with the resulting code (TS main() else-branch). The
// output mode is derived from the app mode: JSON → json, print → text.
func runPrintDispatch(runtime *core.AgentSessionRuntime, m appMode, parsed cli.Args, initial cli.InitialMessageResult) {
	exitCode, err := modes.RunPrintMode(runtime, modes.PrintModeOptions{
		Mode:           string(toPrintOutputMode(m)), // "text" | "json"
		Messages:       parsed.Messages,               // follow-ups (initial was shifted off)
		InitialMessage: initial.InitialMessage,
		InitialImages:  toAnyImages(initial.Images),
	})
	if err != nil {
		// Prompt/rebind failures surface here for logging (TS catches + exit 1);
		// a stopReason error/aborted is logged inside RunPrintMode as (1, nil).
		fmt.Fprintln(os.Stderr, ansiRed+"Error: "+err.Error()+ansiReset)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

// runRPCDispatch runs headless RPC mode over the runtime (TS main() rpc branch).
// It blocks for the process lifetime (shutting down via signal or stdin EOF); it
// returns only if the initial rebind fails.
func runRPCDispatch(runtime *core.AgentSessionRuntime) {
	if err := modes.RunRPCMode(runtime); err != nil {
		fmt.Fprintln(os.Stderr, ansiRed+"Error: "+err.Error()+ansiReset)
		os.Exit(1)
	}
}

// toAnyImages widens []*llm.ImageContent to the []any the mode/TUI APIs expect.
// A nil/empty input yields nil (treated as absent, matching the TS).
func toAnyImages(images []*llm.ImageContent) []any {
	if len(images) == 0 {
		return nil
	}
	out := make([]any, len(images))
	for i, img := range images {
		out[i] = img
	}
	return out
}
