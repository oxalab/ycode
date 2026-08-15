// Package cli implements command-line argument parsing and help display.
//
// This is the Go port of reference/coding-agent/src/cli/args.ts. Like the
// reference, it is a hand-rolled parser — NOT a flag framework. The reference
// deliberately captures unknown long flags into UnknownFlags (so extensions can
// register flags such as --plan), collects @file arguments separately, and has
// special --print/-p next-argument-consumption semantics, none of which fit a
// strict parser like pflag/cobra. Porting the manual loop faithfully preserves
// every one of those behaviours. (Cobra was listed in the Phase 14.1 plan but
// was never added to go.mod; the reference source itself uses no framework.)
//
// parseArgs does not act on the result — it only produces an Args struct. The
// entry point (cmd/ycode/main.go, Phase 19.8) inspects Args and dispatches.
package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/config"
	"ycode/packages/coding-agent/src/core/extensions"
)

// Mode is the output mode for a run (TS Mode).
type Mode string

const (
	ModeText Mode = "text"
	ModeJSON Mode = "json"
	ModeRPC  Mode = "rpc"
)

// Diagnostic is a non-fatal parse notice (TS Args.diagnostics entry).
type Diagnostic struct {
	Type    string // "warning" or "error"
	Message string
}

// Args holds the parsed command-line arguments (TS Args interface). Pointer /
// presence semantics from the TS optional fields are represented as follows:
//   - Scalar strings use "" (zero value) for "unset"; the parser only assigns a
//     value when the flag is actually present, so "" reliably means absent.
//   - Repeatable flags ([]string) start nil and are appended on each occurrence;
//     nil/empty means the flag was never given.
//   - ListModels is split into a bool (was --list-models given?) plus an
//     optional search string (the TS "string | true" union).
//   - UnknownFlags mirrors the TS Map<string, boolean | string>: values are
//     either bool (true) or string.
type Args struct {
	Provider           string
	Model              string
	APIKey             string
	SystemPrompt       string
	AppendSystemPrompt []string
	Thinking           sdk.ThinkingLevel
	Continue           bool
	Resume             bool
	Help               bool
	Version            bool
	Mode               Mode
	NoSession          bool
	Session            string
	Fork               string
	SessionDir         string
	Models             []string
	Tools              []string
	NoTools            bool
	NoBuiltinTools     bool
	Extensions         []string
	NoExtensions       bool
	Print              bool
	Export             string
	NoSkills           bool
	Skills             []string
	PromptTemplates    []string
	NoPromptTemplates  bool
	Themes             []string
	NoThemes           bool
	NoContextFiles     bool
	ListModels         bool
	ListModelsSearch   string
	Offline            bool
	Verbose            bool

	// Messages is the ordered list of positional prompt arguments.
	Messages []string
	// FileArgs is the ordered list of @file arguments (with the @ stripped).
	FileArgs []string
	// UnknownFlags captures unrecognised long flags (potentially extension
	// flags) keyed by flag name to a bool or string value.
	UnknownFlags map[string]any
	// Diagnostics collects warnings and errors encountered while parsing.
	Diagnostics []Diagnostic
}

// validThinkingLevels mirrors the TS VALID_THINKING_LEVELS tuple, in order, for
// validation and diagnostic messages.
var validThinkingLevels = []sdk.ThinkingLevel{
	sdk.ThinkingOff,
	sdk.ThinkingMinimal,
	sdk.ThinkingLow,
	sdk.ThinkingMedium,
	sdk.ThinkingHigh,
	sdk.ThinkingXHigh,
}

// IsValidThinkingLevel reports whether level is a recognised thinking level
// (TS isValidThinkingLevel).
func IsValidThinkingLevel(level string) bool {
	for _, l := range validThinkingLevels {
		if string(l) == level {
			return true
		}
	}
	return false
}

// ParseArgs parses the argument slice into an Args struct (TS parseArgs). It
// never returns an error: invalid input is recorded in Diagnostics (warnings
// and errors), matching the reference, so the caller can decide how to surface
// them.
func ParseArgs(args []string) Args {
	result := Args{
		Messages:     []string{},
		FileArgs:     []string{},
		UnknownFlags: map[string]any{},
		Diagnostics:  []Diagnostic{},
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		hasNext, next := peek(args, i)

		switch {
		case arg == "--help" || arg == "-h":
			result.Help = true

		case arg == "--version" || arg == "-v":
			result.Version = true

		case arg == "--mode" && hasNext:
			mode := next
			if mode == string(ModeText) || mode == string(ModeJSON) || mode == string(ModeRPC) {
				result.Mode = Mode(mode)
				i++
			}

		case arg == "--continue" || arg == "-c":
			result.Continue = true

		case arg == "--resume" || arg == "-r":
			result.Resume = true

		case arg == "--provider" && hasNext:
			result.Provider = next
			i++

		case arg == "--model" && hasNext:
			result.Model = next
			i++

		case arg == "--api-key" && hasNext:
			result.APIKey = next
			i++

		case arg == "--system-prompt" && hasNext:
			result.SystemPrompt = next
			i++

		case arg == "--append-system-prompt" && hasNext:
			result.AppendSystemPrompt = append(result.AppendSystemPrompt, next)
			i++

		case arg == "--no-session":
			result.NoSession = true

		case arg == "--session" && hasNext:
			result.Session = next
			i++

		case arg == "--fork" && hasNext:
			result.Fork = next
			i++

		case arg == "--session-dir" && hasNext:
			result.SessionDir = next
			i++

		case arg == "--models" && hasNext:
			result.Models = splitCSV(next)
			i++

		case arg == "--no-tools" || arg == "-nt":
			result.NoTools = true

		case arg == "--no-builtin-tools" || arg == "-nbt":
			result.NoBuiltinTools = true

		case (arg == "--tools" || arg == "-t") && hasNext:
			result.Tools = splitCSVFiltered(next)
			i++

		case arg == "--thinking" && hasNext:
			level := next
			i++
			if IsValidThinkingLevel(level) {
				result.Thinking = sdk.ThinkingLevel(level)
			} else {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{
					Type:    "warning",
					Message: fmt.Sprintf("Invalid thinking level %q. Valid values: %s", level, joinThinkingLevels()),
				})
			}

		case arg == "--print" || arg == "-p":
			result.Print = true
			// Consume the next argument as the prompt message when it is not a
			// flag-like token: not @-prefixed, and not --prefixed unless it is a
			// triple-dash (---) sentinel (TS: !startsWith("-") || startsWith("---")).
			if hasNext && !strings.HasPrefix(next, "@") &&
				(!strings.HasPrefix(next, "-") || strings.HasPrefix(next, "---")) {
				result.Messages = append(result.Messages, next)
				i++
			}

		case arg == "--export" && hasNext:
			result.Export = next
			i++

		case (arg == "--extension" || arg == "-e") && hasNext:
			result.Extensions = append(result.Extensions, next)
			i++

		case arg == "--no-extensions" || arg == "-ne":
			result.NoExtensions = true

		case arg == "--skill" && hasNext:
			result.Skills = append(result.Skills, next)
			i++

		case arg == "--prompt-template" && hasNext:
			result.PromptTemplates = append(result.PromptTemplates, next)
			i++

		case arg == "--theme" && hasNext:
			result.Themes = append(result.Themes, next)
			i++

		case arg == "--no-skills" || arg == "-ns":
			result.NoSkills = true

		case arg == "--no-prompt-templates" || arg == "-np":
			result.NoPromptTemplates = true

		case arg == "--no-themes":
			result.NoThemes = true

		case arg == "--no-context-files" || arg == "-nc":
			result.NoContextFiles = true

		case arg == "--list-models":
			// Consume the next argument as a search pattern when it is not a
			// flag and not a file argument (TS: !startsWith("-") && !startsWith("@")).
			if hasNext && !strings.HasPrefix(next, "-") && !strings.HasPrefix(next, "@") {
				result.ListModels = true
				result.ListModelsSearch = next
				i++
			} else {
				result.ListModels = true
			}

		case arg == "--verbose":
			result.Verbose = true

		case arg == "--offline":
			result.Offline = true

		case strings.HasPrefix(arg, "@"):
			// File argument: strip the leading @.
			result.FileArgs = append(result.FileArgs, arg[1:])

		case strings.HasPrefix(arg, "--"):
			// Unknown long flag: capture it (and its value, if any) so extensions
			// can register flags such as --plan. Supports --name=value and
			// --name value forms; a bare --name is recorded as true.
			if eq := strings.IndexByte(arg, '='); eq != -1 {
				result.UnknownFlags[arg[2:eq]] = arg[eq+1:]
			} else {
				flagName := arg[2:]
				if hasNext && !strings.HasPrefix(next, "-") && !strings.HasPrefix(next, "@") {
					result.UnknownFlags[flagName] = next
					i++
				} else {
					result.UnknownFlags[flagName] = true
				}
			}

		case strings.HasPrefix(arg, "-"):
			// Unknown short flag: record an error diagnostic (TS behaviour).
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Type:    "error",
				Message: fmt.Sprintf("Unknown option: %s", arg),
			})

		default:
			// Positional prompt argument.
			result.Messages = append(result.Messages, arg)
		}
	}

	return result
}

// peek returns whether args[i+1] exists and, if so, its value. It mirrors the
// TS `args[i+1] !== undefined` / `args[++i]` pattern without the off-by-one.
func peek(args []string, i int) (hasNext bool, next string) {
	if i+1 < len(args) {
		return true, args[i+1]
	}
	return false, ""
}

// splitCSV splits a comma-separated value into trimmed fields (TS
// `next.split(",").map(s => s.trim())`).
func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// splitCSVFiltered is splitCSV that also drops empty fields (TS
// `split(",").map(trim).filter(name => name.length > 0)`, used by --tools).
func splitCSVFiltered(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// joinThinkingLevels renders the valid thinking levels as a comma-separated list
// for diagnostic messages (TS VALID_THINKING_LEVELS.join(", ")).
func joinThinkingLevels() string {
	parts := make([]string, len(validThinkingLevels))
	for i, l := range validThinkingLevels {
		parts[i] = string(l)
	}
	return strings.Join(parts, ", ")
}

// ============================================================================
// Help
// ============================================================================

// PrintHelp prints the CLI help text to stdout (TS printHelp). Extension flags,
// when present, are rendered in an "Extension CLI Flags" section. The dynamic
// values (app name, config dir, env-var names) are sourced from the config
// package, matching the reference's use of APP_NAME / CONFIG_DIR_NAME /
// ENV_AGENT_DIR / ENV_SESSION_DIR.
func PrintHelp(extFlags []extensions.ExtensionFlag) {
	body := strings.NewReplacer(
		"{{APP}}", config.AppName,
		"{{CONFIG_DIR}}", config.ConfigDirName,
		"{{ENV_AGENT_DIR}}", padRight(config.EnvAgentDir, 32),
		"{{ENV_SESSION_DIR}}", padRight(config.EnvSessionDir, 32),
		"{{EXTENSION_FLAGS}}", renderExtensionFlags(extFlags),
	).Replace(helpTemplate)

	// Bold the section headers and the leading app name, matching chalk.bold in
	// the reference. Headers are replaced once each.
	for _, h := range []string{
		"Usage:", "Commands:", "Options:", "Examples:",
		"Environment Variables:", "Built-in Tool Names:",
	} {
		body = strings.Replace(body, h, bold(h), 1)
	}
	body = strings.Replace(body,
		config.AppName+" - AI coding assistant",
		bold(config.AppName)+" - AI coding assistant", 1)
	if len(extFlags) > 0 {
		body = strings.Replace(body, "Extension CLI Flags:", bold("Extension CLI Flags:"), 1)
	}

	fmt.Print(body)
}

// renderExtensionFlags builds the Extension CLI Flags block, or "" when there
// are none (TS extensionFlagsText).
func renderExtensionFlags(flags []extensions.ExtensionFlag) string {
	if len(flags) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, f := range flags {
		value := ""
		if f.Type == "string" {
			value = " <value>"
		}
		desc := f.Description
		if desc == "" {
			desc = "Registered by " + f.ExtensionPath
		}
		b.WriteString(padRight("  --"+f.Name+value, 30))
		b.WriteString(desc)
		b.WriteString("\n")
	}
	return b.String()
}

// padRight left-aligns s in a field of the given width by appending spaces
// (TS String.prototype.padEnd). Width is measured in runes.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// bold wraps s in ANSI bold SGR codes, matching chalk.bold (unconditional bold,
// independent of the theme/colorprofile — help is printed before the TUI starts).
func bold(s string) string {
	return "\x1b[1m" + s + "\x1b[0m"
}

// helpTemplate is the help text with config-driven values as {{tokens}}. The
// literal spacing of the environment-variable lines is copied verbatim from the
// reference; only {{ENV_AGENT_DIR}} and {{ENV_SESSION_DIR}} are padded at
// substitution time. Static product references (e.g. the pi.dev share URL,
// "pi and installed extensions") are kept verbatim from the reference and may
// warrant ycode-specific adjustment.
const helpTemplate = `{{APP}} - AI coding assistant with read, bash, edit, write tools

Usage:
  {{APP}} [options] [@files...] [messages...]

Commands:
  {{APP}} install <source> [-l]     Install extension source and add to settings
  {{APP}} remove <source> [-l]      Remove extension source from settings
  {{APP}} uninstall <source> [-l]   Alias for remove
  {{APP}} update [source|self|pi]   Update pi and installed extensions
  {{APP}} list                      List installed extensions from settings
  {{APP}} config                    Open TUI to enable/disable package resources
  {{APP}} <command> --help          Show help for install/remove/uninstall/update/list

Options:
  --provider <name>              Provider name (default: google)
  --model <pattern>              Model pattern or ID (supports "provider/id" and optional ":<thinking>")
  --api-key <key>                API key (defaults to env vars)
  --system-prompt <text>         System prompt (default: coding assistant prompt)
  --append-system-prompt <text>  Append text or file contents to the system prompt (can be used multiple times)
  --mode <mode>                  Output mode: text (default), json, or rpc
  --print, -p                    Non-interactive mode: process prompt and exit
  --continue, -c                 Continue previous session
  --resume, -r                   Select a session to resume
  --session <path|id>            Use specific session file or partial UUID
  --fork <path|id>               Fork specific session file or partial UUID into a new session
  --session-dir <dir>            Directory for session storage and lookup
  --no-session                   Don't save session (ephemeral)
  --models <patterns>            Comma-separated model patterns for Ctrl+P cycling
                                 Supports globs (anthropic/*, *sonnet*) and fuzzy matching
  --no-tools, -nt                Disable all tools by default (built-in and extension)
  --no-builtin-tools, -nbt       Disable built-in tools by default but keep extension/custom tools enabled
  --tools, -t <tools>            Comma-separated allowlist of tool names to enable
                                 Applies to built-in, extension, and custom tools
  --thinking <level>             Set thinking level: off, minimal, low, medium, high, xhigh
  --extension, -e <path>         Load an extension file (can be used multiple times)
  --no-extensions, -ne           Disable extension discovery (explicit -e paths still work)
  --skill <path>                 Load a skill file or directory (can be used multiple times)
  --no-skills, -ns               Disable skills discovery and loading
  --prompt-template <path>       Load a prompt template file or directory (can be used multiple times)
  --no-prompt-templates, -np     Disable prompt template discovery and loading
  --theme <path>                 Load a theme file or directory (can be used multiple times)
  --no-themes                    Disable theme discovery and loading
  --no-context-files, -nc        Disable AGENTS.md and CLAUDE.md discovery and loading
  --export <file>                Export session file to HTML and exit
  --list-models [search]         List available models (with optional fuzzy search)
  --verbose                      Force verbose startup (overrides quietStartup setting)
  --offline                      Disable startup network operations (same as PI_OFFLINE=1)
  --help, -h                     Show this help
  --version, -v                  Show version number

Extensions can register additional flags (e.g., --plan from plan-mode extension).{{EXTENSION_FLAGS}}
Examples:
  # Interactive mode
  {{APP}}

  # Interactive mode with initial prompt
  {{APP}} "List all .ts files in src/"

  # Include files in initial message
  {{APP}} @prompt.md @image.png "What color is the sky?"

  # Non-interactive mode (process and exit)
  {{APP}} -p "List all .ts files in src/"

  # Multiple messages (interactive)
  {{APP}} "Read package.json" "What dependencies do we have?"

  # Continue previous session
  {{APP}} --continue "What did we discuss?"

  # Use different model
  {{APP}} --provider openai --model gpt-4o-mini "Help me refactor this code"

  # Use model with provider prefix (no --provider needed)
  {{APP}} --model openai/gpt-4o "Help me refactor this code"

  # Use model with thinking level shorthand
  {{APP}} --model sonnet:high "Solve this complex problem"

  # Limit model cycling to specific models
  {{APP}} --models claude-sonnet,claude-haiku,gpt-4o

  # Limit to a specific provider with glob pattern
  {{APP}} --models "github-copilot/*"

  # Cycle models with fixed thinking levels
  {{APP}} --models sonnet:high,haiku:low

  # Start with a specific thinking level
  {{APP}} --thinking high "Solve this complex problem"

  # Read-only mode (no file modifications possible)
  {{APP}} --tools read,grep,find,ls -p "Review the code in src/"

  # Export a session file to HTML
  {{APP}} --export ~/{{CONFIG_DIR}}/agent/sessions/--path--/session.jsonl
  {{APP}} --export session.jsonl output.html

Environment Variables:
  ANTHROPIC_API_KEY                - Anthropic Claude API key
  ANTHROPIC_OAUTH_TOKEN            - Anthropic OAuth token (alternative to API key)
  OPENAI_API_KEY                   - OpenAI GPT API key
  AZURE_OPENAI_API_KEY             - Azure OpenAI API key
  AZURE_OPENAI_BASE_URL            - Azure OpenAI/Cognitive Services base URL (e.g. https://{resource}.openai.azure.com)
  AZURE_OPENAI_RESOURCE_NAME       - Azure OpenAI resource name (alternative to base URL)
  AZURE_OPENAI_API_VERSION         - Azure OpenAI API version (default: v1)
  AZURE_OPENAI_DEPLOYMENT_NAME_MAP - Azure OpenAI model=deployment map (comma-separated)
  DEEPSEEK_API_KEY                 - DeepSeek API key
  GEMINI_API_KEY                   - Google Gemini API key
  GROQ_API_KEY                     - Groq API key
  CEREBRAS_API_KEY                 - Cerebras API key
  XAI_API_KEY                      - xAI Grok API key
  FIREWORKS_API_KEY                - Fireworks API key
  TOGETHER_API_KEY                 - Together AI API key
  OPENROUTER_API_KEY               - OpenRouter API key
  AI_GATEWAY_API_KEY               - Vercel AI Gateway API key
  ZAI_API_KEY                      - ZAI API key
  MISTRAL_API_KEY                  - Mistral API key
  MINIMAX_API_KEY                  - MiniMax API key
  MOONSHOT_API_KEY                 - Moonshot AI API key
  OPENCODE_API_KEY                 - OpenCode Zen/OpenCode Go API key
  KIMI_API_KEY                     - Kimi For Coding API key
  CLOUDFLARE_API_KEY               - Cloudflare API token (Workers AI and AI Gateway)
  CLOUDFLARE_ACCOUNT_ID            - Cloudflare account id (required for both)
  CLOUDFLARE_GATEWAY_ID            - Cloudflare AI Gateway slug (required for AI Gateway)
  XIAOMI_API_KEY                   - Xiaomi MiMo API key (api.xiaomimimo.com billing)
  XIAOMI_TOKEN_PLAN_CN_API_KEY     - Xiaomi MiMo Token Plan API key (China region)
  XIAOMI_TOKEN_PLAN_AMS_API_KEY    - Xiaomi MiMo Token Plan API key (Amsterdam region)
  XIAOMI_TOKEN_PLAN_SGP_API_KEY    - Xiaomi MiMo Token Plan API key (Singapore region)
  AWS_PROFILE                      - AWS profile for Amazon Bedrock
  AWS_ACCESS_KEY_ID                - AWS access key for Amazon Bedrock
  AWS_SECRET_ACCESS_KEY            - AWS secret key for Amazon Bedrock
  AWS_BEARER_TOKEN_BEDROCK         - Bedrock API key (bearer token)
  AWS_REGION                       - AWS region for Amazon Bedrock (e.g., us-east-1)
  {{ENV_AGENT_DIR}}- Config directory (default: ~/{{CONFIG_DIR}}/agent)
  {{ENV_SESSION_DIR}}- Session storage directory (overridden by --session-dir)
  PI_PACKAGE_DIR                   - Override package directory (for Nix/Guix store paths)
  PI_OFFLINE                       - Disable startup network operations when set to 1/true/yes
  PI_TELEMETRY                     - Override install telemetry when set to 1/true/yes or 0/false/no
  PI_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pi.dev/session/)

Built-in Tool Names:
  read   - Read file contents
  bash   - Execute bash commands
  edit   - Edit files with find/replace
  write  - Write files (creates/overwrites)
  grep   - Search file contents (read-only, off by default)
  find   - Find files by glob pattern (read-only, off by default)
  ls     - List directory contents (read-only, off by default)
`
