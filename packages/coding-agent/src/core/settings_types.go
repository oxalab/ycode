package ycode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"ycode/packages/llm/src"
)

type CompactionSettings struct {
	Enabled 		*bool 	`json:"enabled,omitempty"`
	ReserveTokens 	*int 	`json:"reserveTokens,omitempty"`
	KeepRecentTokens *int 	`json:"keepRecentTokens,omitempty"`
}

type BranchSummarySettings struct {
	ReserveTokens *int 	`json:"reserveTokens,omitempty"`
	SkipPrompt 	  *bool `json:"skipPrompt,omitempty"`
}

type ProviderRetrySettings struct {
	TimeoutMs 		*int `json:"timeoutMs,omitempty"`
	MaxRetries  	*int `json:"maxRetries,omitempty"`
	MaxRetryDelayMs *int `json:"maxRetryDelayMs,omitempty"`
}

type RetrySettings struct {
	Enabled 	*bool  		`json:"enabled,omitempty"`
	MaxRetries 	*int 		`json:"maxRetries,omitempty"`
	BaseDelayMs *int 		`json:"baseDelayMs,omitempty"`
	Provider 	*ProviderRetrySettings `json:"provider,omitempty"`
}

type TerminalSettings struct {
	ShowImages 		*bool `json:"showImages,omitempty"`
	ImageWidthCells *int  `json:"imageWidthCells,omitempty"`
	ClearOnShrink 	*bool `json:"cleanOnShrink,omitempty"`
	ShowTerminalProgress *bool `json:"showTerminalProgress,omitempty"`
}

type ImageSettings struct {
	AutoResize 		*bool 	`json:"autoResize,omitempty"`
	BlockImages 	*bool 	`json:"blockImages,omitempty"`
}

type ThinkingBudgetsSettings struct {
	Minimal *int `json:"minimal,omitempty"`
	Low 	*int `json:"low,omitempty"`
	Medium  *int `json:"medium,omitempty"`
	High	*int `json:"high,omitempty"`
}

type MarkdownSettings struct {
	CodeBlockIndent *string `json:"codeBlockIndent,omitempty"`
}

type WarningSettings struct {
	AnthropicExtraUsage  *bool 	`json:"anthropicExtraUsage,omitempty"`
}

type PackageSourceFilter struct {
	Source 		string  	`json:"source"`
	Extensions	[]string 	`json:"extensions,omitempty"`
	Skills 		[]string 	`json:"skills,omitempty"`
	Prompts 	[]string 	`json:"prompt,omitempty"`
	Themes 		[]string 	`json:"themes,omitempty"`
}

type PackageSource = any   // string or PackageSourceFilter

type Settings struct {
	LastChangelogVersion	*string 		`json:"lastChangelogVersion,omitempty"`
	DefaultProvider 		*string 		`json:"defaultProvider,omitempty"`
	DefaultModel 			*string 		`json:"defaultModel,omitempty"`
	DefaultThinkingLevel 	*string 		`json:"defaultThinkingLevel,omitempty"`
	Transport 				*llm.Transport 	`json:"transport,omitempty"`
	SteeringMode 			*string 		`json:"steeringMode,omitempty"`
	FollowUpMode 			*string 		`json:"followUpMode,omitempty"`
	Theme 					*string 		`json:"theme,omitempty"`
	Compaction 				*CompactionSettings `json:"compaction,omitempty"`
	BranchSummary 			*BranchSummarySettings `json:"branchSummary,omitempty"`
	Retry 					*RetrySettings 	`json:"retry,omitempty"`
	HideThinkingBlock 		*bool 			`json:"hideThinkingBlock,omitempty"`
	ShellPath 				*string 		`json:"shellPath,omitempty"`
	QuietStartup 			*bool 			`json:"queitStartup,omitempty"`
	ShellCommandPrefix 		*string 		`json:"shellCommandPrefix,omitempty"`
	NpmCommand 				[]string 		`json:"npmCommand,omitempty"`
	CollapseChangelog 		*bool 			`json:"collapseChangelog,omitempty"`
	EnableInstallTelemetry 	*bool 			`json:"enableInstallTelemetry,omitempty"`
	Packages 				[]PackageSource `json:"packages,omitempty"`
	Extensions 				[]string 		`json:"extension,omitempty"`
	Skills 					[]string 		`json:"skills,omitempty"`
	Prompts 				[]string 		`json:"prompts,omitempty"`
	Themes 					[]string 		`json:"themes,omitempty"`
	EnableSkillCommands 	*bool 			`json:"enableSkillCommands"`
	Terminal 				*TerminalSettings `json:"terminal,omitempty"`
	Images 					*ImageSettings 	`json:"imageSettings,omitempty"`
	EnabledModels 			[]string 		`json:"enabledModels,omitempty"`
	DoubleEscapeAction 		*string 		`json:"doubleEscapeAction,omitempty"`
	TreeFilterMode 			*string 		`json:"treeFilterMode,omitempty"`
	ThinkingBudgets 		*ThinkingBudgetsSettings `json:"thinkingBudgets,omitempty"`
	EditorPaddingX			*int 			`json:"editorPaddingX,omitempty"`
	AutocompleteMaxVisible 	*int 			`json:"autocompleteMaxVisible,omitempty"`
	ShowHardwareCursor 		*bool 			`json:"showHardwareCursor,omitempty"`
	Markdown 				*MarkdownSettings `json:"markdown,omitempty"`
	Warnings 				*WarningSettings `json:"warnings,omitempty"`
	SessionDir 				*string 		`json:"sessionDir,omitempty"`
}

type SettingsScope string

const (
	ScopeGlobalSetting	 	SettingsScope = "global"
	ScopeProjectSetting 	SettingsScope = "project"
)

type SettingsError struct {
	Scope SettingsScope
	Error error
}

// SettingsStorage interface 
type SettingsStorage interface {
	WithLock(scope SettingsScope, fn func(current *string) *string)
}

// FileSettingsStorage
type FileSettingsStorage struct {
	globalPath 	string
	projectPath string
	mu 			sync.Mutex
}

func NewFileSettingsStorage(cwd, agentDir string) *FileSettingsStorage {
	return &FileSettingsStorage{
		globalPath: 	filepath.Join(agentDir, "settings.json"),
		projectPath: 	filepath.Join(cwd, ".ycode", "settings.json"),
	}
}

func (s *FileSettingsStorage) WithLock(scope SettingsScope, fn func(current *string) *string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.getPath(scope)
	dir := filepath.Dir(path)

	var current *string
	if data, err := os.ReadFile(path); err == nil {
		content := string(data)
		current = &content
	}
	next := fn(current)
	if next != nil {
		os.MkdirAll(dir, 0o755)
		os.WriteFile(path, []byte(*next), 0o644)
	}
}


func (s *FileSettingsStorage) getPath(scope SettingsScope) string {
	if scope == ScopeGlobalSetting {
		return s.globalPath
	}
	return s.projectPath
}

// In-Memory Settings Storage
type InMemorySettingsStorage struct {
	mu 		sync.Mutex
	global 	*string
	project *string
}


func NewInMemorySettingsStorage() *InMemorySettingsStorage {
	return &InMemorySettingsStorage{}
}

func (s *InMemorySettingsStorage) WithLock(scope SettingsScope, fn func(current *string) *string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var current *string
	if scope == ScopeGlobalSetting{
		current = s.global
	} else {
		current = s.project
	}
	next := fn(current)
	if next != nil {
		if scope == ScopeGlobalSetting {
			s.global = next
		} else {
			s.project = next
		}
	}
}

// deepMergeSettings: Merges Overrides on top of base
func deepMergeSettings(base, overrides Settings) Settings {
	result := base

	// Use JSON round-trip for simplicity
	baseJSON, _ := json.Marshal(base)
	overridesJSON, _ := json.Marshal(overrides)
	var baseMap, overrideMap map[string]any
	json.Unmarshal(baseJSON, &baseMap)
	json.Unmarshal(overridesJSON, &overrideMap)

	merged := make(map[string]any)
	for k, v := range baseMap {
		merged[k] = v
	}
	for k, v := range overrideMap {
		if v == nil {
			continue
		}
		// Nested objects merge recursively
		if baseVal, ok := baseMap[k]; ok {
			if baseMap, ok := baseVal.(map[string]any); ok {
				if overrideMap, ok := v.(map[string]any); ok {
					mergedMap := make(map[string]any)
					for mk, mv := range baseMap {
						mergedMap[mk] = mv
					}
					for mk, mv := range overrideMap {
						mergedMap[mk] = mv
					}
					merged[k] = mergedMap
					continue
				}
			}
		}
		merged[k] = v
	}
	mergedJSON, _ := json.Marshal(merged)
	json.Unmarshal(mergedJSON, &result)
	return result
}

func cloneSettings(s Settings) Settings {
	data, _ := json.Marshal(s)
	var result Settings
	json.Unmarshal(data, &result)
	return result
}

func settingsToJSON(s Settings) string {
	data, _ := json.MarshalIndent(s, "", "  ")
	return string(data)
}

func parseSettings(content string) (Settings, error) {
	var s Settings
	err := json.Unmarshal([]byte(content), &s)
	return s, err
}