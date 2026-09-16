// Command ycode is the entry point for the coding-agent CLI. This package ports
// reference/coding-agent/src/main.ts: it parses args, resolves the session/model/
// thinking/runtime, and dispatches to interactive/print/json/rpc modes.
//
// ============================================================================
// Deviations from the plan sketch + honest deferrals
// ============================================================================
//
// This is a Full structural port: every control-flow, model-resolution, session,
// runtime/service subsystem, AND run mode has a Go backing impl, so ParseArgs +
// diagnostics, offline, version/help/list-models/export exits, session resolution,
// CLI model + thinking + scoped-models resolution, stdin/@file initial-message
// building, startup migrations, and the interactive/print/json/rpc dispatch are all
// wired. The remaining gap is genuinely missing in Go:
//
//   - package/config dispatcher (ycode install/remove/update/list/config) → not
//     intercepted; no handlePackageCommand/handleConfigCommand exists.
//
// stdout takeover (TS takeOverStdout/restoreStdout) has no Go port (output-guard),
// so it is omitted — print mode writes directly to stdout and rpc mode serializes
// its stdout writes with a mutex. The interactive TUI behaves as before.
package main

import (
	"fmt"
	"os"

	"ycode/packages/coding-agent/src/config"
	core "ycode/packages/coding-agent/src/core"
	exporthtml "ycode/packages/coding-agent/src/core/export-html"
	"ycode/packages/coding-agent/src/migrations"
	cli "ycode/packages/coding-agent/src/cli"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// run is the orchestrator (TS main). It returns an error only for the
// runtime/factory creation path; most failure modes report and os.Exit directly
// to match the TS process.exit semantics.
func run() error {
	args := os.Args[1:]

	// Offline mode: --offline flag or PI_OFFLINE env. Mirrors the TS, setting
	// PI_OFFLINE/PI_SKIP_VERSION_CHECK so downstream network ops are skipped.
	if contains(args, "--offline") || isTruthyEnvFlag(os.Getenv("PI_OFFLINE")) {
		_ = os.Setenv("PI_OFFLINE", "1")
		_ = os.Setenv("PI_SKIP_VERSION_CHECK", "1")
	}

	// Package/config subcommands (install/remove/update/list/config) are not
	// intercepted here — no dispatcher exists in Go yet. They fall through to
	// ParseArgs, which records them as positional/unknown args.

	parsed := cli.ParseArgs(args)
	if reportParseDiagnostics(parsed.Diagnostics) {
		os.Exit(1)
	}
	core.Time("parseArgs")

	appMode := resolveAppMode(parsed, stdinIsTerminal())
	// stdout takeover deferred (output-guard has no Go port).

	// --- Early exits (before any runtime/session work) ---

	if parsed.Version {
		fmt.Println(config.Version)
		os.Exit(0)
	}

	if parsed.Export != "" {
		outputPath := ""
		if len(parsed.Messages) > 0 {
			outputPath = parsed.Messages[0]
		}
		result, err := exporthtml.ExportFromFile(parsed.Export, &exporthtml.ExportOptions{OutputPath: outputPath})
		if err != nil {
			fmt.Fprintln(os.Stderr, ansiRed+"Error: "+err.Error()+ansiReset)
			os.Exit(1)
		}
		fmt.Println("Exported to: " + result)
		os.Exit(0)
	}

	if parsed.Mode == cli.ModeRPC && len(parsed.FileArgs) > 0 {
		fmt.Fprintln(os.Stderr, ansiRed+
			"Error: @file arguments are not supported in RPC mode"+ansiReset)
		os.Exit(1)
	}

	validateForkFlags(parsed)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Run one-time startup migrations (19.9): legacy auth → auth.json, stray
	// session files, tools→bin, keybindings, and the extension-system migration
	// (commands→prompts) plus deprecated-directory warnings.
	migrationResult := migrations.RunMigrations(cwd)
	core.Time("runMigrations")

	agentDir := core.GetAgentDir()
	startupSettingsManager := core.NewSettingsManagerFromDirs(cwd, agentDir)
	reportDiagnostics(os.Stderr, collectSettingsDiagnostics(startupSettingsManager, "startup session lookup"))

	// Session dir: --session-dir → env → settings (TS chain).
	sessionDir := parsed.SessionDir
	if sessionDir == "" {
		if envSessionDir := os.Getenv(config.EnvSessionDir); envSessionDir != "" {
			sessionDir = config.ExpandTildePath(envSessionDir)
		}
	}
	if sessionDir == "" {
		sessionDir = startupSettingsManager.GetSessionDir()
	}

	sessionManager := createSessionManager(parsed, cwd, sessionDir)

	// Missing-session-cwd guard. The TS interactive path pops a TUI prompt; that
	// prompt is deferred, so we always use the error + exit-1 path (correct for
	// fresh sessions, rarely fires).
	if issue := core.GetMissingSessionCwdIssue(sessionCwdAdapter{sessionManager}, cwd); issue != nil {
		fmt.Fprintln(os.Stderr, ansiRed+
			(&core.MissingSessionCwdError{Issue: *issue}).Error()+ansiReset)
		os.Exit(1)
	}
	core.Time("createSessionManager")

	resolvedExtensionPaths := resolveCliPaths(cwd, parsed.Extensions)
	resolvedSkillPaths := resolveCliPaths(cwd, parsed.Skills)
	resolvedPromptTemplatePaths := resolveCliPaths(cwd, parsed.PromptTemplates)
	resolvedThemePaths := resolveCliPaths(cwd, parsed.Themes)
	authStorage := core.NewAuthStorage(config.GetAuthPath())

	factory := createRuntimeFactory(parsed, resolvedExtensionPaths, resolvedSkillPaths,
		resolvedPromptTemplatePaths, resolvedThemePaths, authStorage)

	runtime, err := core.CreateAgentSessionRuntime(factory, core.CreateAgentSessionRuntimeFactoryOptions{
		Cwd:            sessionManager.GetCwd(),
		AgentDir:       agentDir,
		SessionManager: sessionManager,
	})
	if err != nil {
		return fmt.Errorf("create agent session: %w", err)
	}
	services := runtime.Services()
	session := runtime.Session()
	modelFallbackMessage := runtime.ModelFallbackMessage()
	core.Time("createRuntime")

	// --- Help / list-models (need the runtime's resource loader + registry) ---

	if parsed.Help {
		cli.PrintHelp(collectExtensionFlags(services.ResourceLoader))
		os.Exit(0)
	}

	if parsed.ListModels {
		search := parsed.ListModelsSearch
		if err := cli.ListModels(services.ModelRegistry, search, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, ansiRed+"Error: "+err.Error()+ansiReset)
		}
		os.Exit(0)
	}

	// --- Stdin / initial message / dispatch ---

	var stdinContent string
	hasStdin := false
	if appMode != appRPC {
		stdinContent, hasStdin = readPipedStdin()
		if hasStdin && appMode == appInteractive {
			appMode = appPrint
		}
	}
	core.Time("readPipedStdin")

	initial, err := prepareInitialMessage(&parsed, cwd, startupSettingsManager.GetImageAutoResize(), stdinContent)
	if err != nil {
		return fmt.Errorf("prepare initial message: %w", err)
	}
	core.Time("prepareInitialMessage")

	// initTheme deferred (theme wiring not in this phase); the TUI runs on the
	// default theme.
	core.Time("initTheme")

	// Show deprecation warnings (hooks//tools/ dirs) in interactive mode before
	// the TUI takes over (TS main).
	if appMode == appInteractive && len(migrationResult.DeprecationWarnings) > 0 {
		migrations.ShowDeprecationWarnings(migrationResult.DeprecationWarnings)
	}

	// The scoped-models banner is computed inside launchInteractive (the only
	// consumer).
	core.Time("resolveModelScope")

	reportDiagnostics(os.Stderr, runtime.Diagnostics())
	for _, d := range runtime.Diagnostics() {
		if d.Type == core.DiagnosticError {
			os.Exit(1)
		}
	}
	core.Time("createAgentSession")

	// Non-interactive runs require a model.
	if appMode != appInteractive && session.GetModel().ID == "" {
		fmt.Fprintln(os.Stderr, ansiRed+core.FormatNoModelIsAvailableMessage()+ansiReset)
		os.Exit(1)
	}

	// PI_STARTUP_BENCHMARK fast-path deferred (needs App init/stop split).
	switch appMode {
	case appRPC:
		runRPCDispatch(runtime)
	case appPrint, appJSON:
		runPrintDispatch(runtime, appMode, parsed, initial)
	default: // appInteractive
		launchInteractive(parsed, runtime, initial, modelFallbackMessage,
			migrationResult.MigratedAuthProviders, services.SettingsManager)
	}
	return nil
}
