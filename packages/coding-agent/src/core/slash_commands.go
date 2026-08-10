package ycode

type SlashCommandSource string

const (
	SlashSourceExtension SlashCommandSource = "extension"
	SlashSourcePrompt    SlashCommandSource = "prompt"
	SlashSourceSkill     SlashCommandSource = "skill"
)

type SlashCommandInfo struct {
	Name        string
	Description string
	Source      SlashCommandSource
	SourceInfo  SourceInfo
}

type BuiltinSlashCommand struct {
	Name        string
	Description string
}

var BuiltinSlashCommands = []BuiltinSlashCommand{
	{Name: "settings", Description: "Open settings menu"},
	{Name: "model", Description: "Select model (opens selector UI)"},
	{Name: "scoped-models", Description: "Enable/disable models for Ctrl+P cycling"},
	{Name: "export", Description: "Export session (HTML default, or specify path: .html/.jsonl)"},
	{Name: "import", Description: "Import and resume a session from a JSONL file"},
	{Name: "share", Description: "Share session as a secret GitHub gist"},
	{Name: "copy", Description: "Copy last agent message to clipboard"},
	{Name: "name", Description: "Set session display name"},
	{Name: "session", Description: "Show session info and stats"},
	{Name: "changelog", Description: "Show changelog entries"},
	{Name: "hotkeys", Description: "Show all keyboard shortcuts"},
	{Name: "fork", Description: "Create a new fork from a previous user message"},
	{Name: "clone", Description: "Duplicate the current session at the current position"},
	{Name: "tree", Description: "Navigate session tree (switch branches)"},
	{Name: "login", Description: "Configure provider authentication"},
	{Name: "logout", Description: "Remove provider authentication"},
	{Name: "new", Description: "Start a new session"},
	{Name: "compact", Description: "Manually compact the session context"},
	{Name: "context", Description: "Show context window usage breakdown"},
	{Name: "debug", Description: "Write rendered lines + message JSONL to the debug log"},
	{Name: "resume", Description: "Resume a different session"},
	{Name: "reload", Description: "Reload keybindings, extensions, skills, prompts, and themes"},
	{Name: "quit", Description: "Quit " + AppName},
}


/*
String enum for SlashCommandSource	TS uses union type `"extension"
*/