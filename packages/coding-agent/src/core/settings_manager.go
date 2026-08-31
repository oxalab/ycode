package ycode

import (
	"encoding/json"
	"os"
	"sync"
	"ycode/packages/llm/src"
)

type SettingsManager struct {
	storage 	SettingsStorage
	global 		Settings
	project 	Settings
	settings 	Settings
	errors 		[]SettingsError

	globalLoadError error
	projectLoadError error

	modifiedFields  		map[string]bool
	modifiedNestedFields 	map[string]map[string]bool
	modifiedProjectFields 	map[string]bool
	modifiedProjectNestedFields map[string]map[string]bool

	mu 			sync.Mutex
	writeQueue 	chan func()
}

func NewSettingsManager(storage SettingsStorage) *SettingsManager {
	globalLoad := tryLoadFromStorage(storage, ScopeGlobalSetting)
	projectLoad := tryLoadFromStorage(storage, ScopeProjectSetting)

	var initialErrors []SettingsError
	if globalLoad.err != nil {
		initialErrors = append(initialErrors, SettingsError{Scope: ScopeGlobalSetting, Error: globalLoad.err})
	}
	if projectLoad.err != nil {
		initialErrors = append(initialErrors, SettingsError{Scope: ScopeProjectSetting, Error: projectLoad.err})
	}
	sm := &SettingsManager {
		storage: 				storage,
		global: 				globalLoad.settings,
		project: 				projectLoad.settings,
		globalLoadError: 		globalLoad.err,
		projectLoadError: 		projectLoad.err,
		errors:					initialErrors,
		modifiedFields: 		make(map[string]bool),
		modifiedNestedFields: 	make(map[string]map[string]bool),
		modifiedProjectFields:  make(map[string]bool),
		modifiedProjectNestedFields: make(map[string]map[string]bool),
		writeQueue: 			make(chan func(), 256),
	}
	sm.settings = deepMergeSettings(sm.global, sm.project)

	// Start write worker
	go func() {
		for fn := range sm.writeQueue {
			fn()
		}
	}()
	return sm
}

func NewSettingsManagerFromDirs(cwd, agentDir string) *SettingsManager {
	storage := NewFileSettingsStorage(cwd, agentDir)
	return NewSettingsManager(storage)
}

func InMemorySettingsManager(initial Settings) *SettingsManager {
	storage := NewInMemorySettingsStorage()
	migrated := migrateSettings(initial)
	content := settingsToJSON(migrated)
	storage.WithLock(ScopeGlobalSetting, func(current *string) *string {
		return &content
	})
	return NewSettingsManager(storage)
}

type loadResult struct {
	settings Settings
	err 	 error
}

func tryLoadFromStorage(storage SettingsStorage, scope SettingsScope) loadResult {
	var content *string
	storage.WithLock(scope, func(current *string) *string {
		content = current
		return nil
	})
	if content == nil {
		return loadResult{ settings: Settings{} }
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(*content), &raw); err != nil {
		return loadResult{settings: Settings{}, err: err}
	}
	migrated := migrateSettingsMap(raw)
	var s Settings
	data, _ := json.Marshal(migrated)
	json.Unmarshal(data, &s)
	return loadResult{settings: s}
}

func migrateSettings(s Settings) Settings {
	data, _ := json.Marshal(s)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	migrated := migrateSettingsMap(raw)
	var result Settings
	data, _ = json.Marshal(migrated)
	json.Unmarshal(data, &result)
	return result
}

func migrateSettingsMap(settings map[string]any) map[string]any {
	// queueMode -> steeringMode
	if _, ok := settings["queueMode"]; ok {
		if _, ok2 := settings["steeringMode"]; !ok2 {
			settings["steeringMode"] = settings["queueMode"]
			delete(settings, "queueMode")
		}
	}

	// websockets boolean -> transport
	if _, ok := settings["transport"]; !ok {
		if ws, ok := settings["websockets"]; ok {
			if b, ok := ws.(bool); ok {
				if b {
					settings["transport"] = "websocket"
				} else {
					settings["transport"] = "sse"
				}
			}
		}
		delete(settings, "websockets")
	}

	// old skills object -> new array
	if skills, ok := settings["skills"]; ok {
		if m, ok := skills.(map[string]any); ok && m != nil {
			if esc, ok := m["enableSkillCommands"]; ok && settings["enableSkillCommands"] == nil {
				settings["enableSkillCommands"] = esc
			}
			if dirs, ok := m["customDirectories"]; ok {
				if arr, ok := dirs.([]any); ok && len(arr) > 0 {
					settings["skills"] = dirs
				} else {
					delete(settings, "skills")
				}
			} else {
				delete(settings, "skills")
			}
		}
	}

	// retry.maxDelayMs -> retry.provider.maxRetryDelayMs
	if retry, ok := settings["retry"]; ok {
		if m, ok := retry.(map[string]any); ok && m != nil {
			if maxDelay, ok := m["maxDelayMs"]; ok {
				provider, _ := m["provider"].(map[string]any)
				if provider == nil {
					provider = make(map[string]any)
				}
				if provider["maxRetryDelayMs"] == nil {
					provider["maxRetryDelayMs"] = maxDelay
				}
				m["provider"] = provider
				delete(m, "maxDelayMs")
			}
		}
	}
	return settings
}


// --- Getters ---

func (sm *SettingsManager) GetGlobalSettings() Settings {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return cloneSettings(sm.global)
}

func (sm *SettingsManager) GetProjectSettings() Settings {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return cloneSettings(sm.project)
}

func (sm *SettingsManager) Reload() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	globalLoad := tryLoadFromStorage(sm.storage, ScopeGlobalSetting)
	if globalLoad.err == nil {
		sm.global = globalLoad.settings
		sm.globalLoadError = nil
	} else {
		sm.globalLoadError = globalLoad.err
		sm.recordError(ScopeGlobalSetting, globalLoad.err)
	}

	sm.modifiedFields = make(map[string]bool)
	sm.modifiedNestedFields = make(map[string]map[string]bool)
	sm.modifiedProjectFields = make(map[string]bool)
	sm.modifiedProjectNestedFields = make(map[string]map[string]bool)

	projectLoad := tryLoadFromStorage(sm.storage, ScopeProjectSetting)
	if projectLoad.err == nil {
		sm.project = projectLoad.settings
		sm.projectLoadError = nil
	} else {
		sm.projectLoadError = projectLoad.err
		sm.recordError(ScopeProjectSetting, projectLoad.err)
	}
	sm.settings = deepMergeSettings(sm.global, sm.project)
}

func (sm *SettingsManager) ApplyOverrides(overrides Settings) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.settings = deepMergeSettings(sm.settings, overrides)
}

func (sm *SettingsManager) DrainError() []SettingsError {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	drained := make([]SettingsError, len(sm.errors))
	copy(drained, sm.errors)
	sm.errors = nil
	return drained
}

func (sm *SettingsManager) Flush() {
	// Wait for pending writes to drain
	// In go we usee a done channel pattern
	done := make(chan struct{})
	sm.writeQueue <- func() { close(done) }
	<-done
}

// --- Setting Accessors ---
func (sm *SettingsManager) GetDefaultProvider() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.DefaultProvider != nil {
		return *sm.settings.DefaultProvider
	}
	return ""
}

func (sm *SettingsManager) GetDefaultModel() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.DefaultModel != nil {
		return *sm.settings.DefaultModel
	}
	return ""
}

func (sm *SettingsManager) SetDefaultProvider(provider string) {
	sm.mu.Lock()
	sm.global.DefaultProvider = &provider
	sm.markModified("defaultProvider", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) SetDefaultModel(modelID string) {
	sm.mu.Lock()
	sm.global.DefaultModel = &modelID
	sm.markModified("defaultModel", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) SetDefaultModelAndProvider(provider, modelID string) {
	sm.mu.Lock()
	sm.global.DefaultProvider = &provider
	sm.global.DefaultModel = &modelID
	sm.markModified("defaultProvider", "")
	sm.markModified("defaultModel", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetSteeringMode() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.SteeringMode != nil {
		return *sm.settings.SteeringMode
	}
	return "one-at-a-time"
}

func (sm *SettingsManager) SetSteeringMode(mode string) {
	sm.mu.Lock()
	sm.global.SteeringMode = &mode
	sm.markModified("steeringMode", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetFollowUpMode() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.FollowUpMode != nil {
		return *sm.settings.FollowUpMode
	}
	return "one-at-a-time"
}

func (sm *SettingsManager) SetFollowUpMode(mode string) {
	sm.mu.Lock()
	sm.global.FollowUpMode = &mode
	sm.markModified("followUpMode", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetTheme() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Theme != nil {
		return *sm.settings.Theme
	}
	return ""
}

func (sm *SettingsManager) SetTheme(theme string) {
	sm.mu.Lock()
	sm.global.Theme = &theme
	sm.markModified("theme", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetDefaultThinkingLevel() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.DefaultThinkingLevel != nil {
		return *sm.settings.DefaultThinkingLevel
	}
	return ""
}

func (sm *SettingsManager) SetDefaultThinkingLevel(level string) {
	sm.mu.Lock()
	sm.global.DefaultThinkingLevel = &level
	sm.markModified("defaultThinkingLevel", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetTransport() llm.Transport {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Transport != nil {
		return *sm.settings.Transport
	}
	return llm.TransportAuto
}

func (sm *SettingsManager) SetTransport(transport llm.Transport) {
	sm.mu.Lock()
	sm.global.Transport = &transport
	sm.markModified("transport", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetCompactionEnabled() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Compaction != nil && sm.settings.Compaction.Enabled != nil {
		return *sm.settings.Compaction.Enabled
	}
	return true
}

func (sm *SettingsManager) SetCompactionEnabled(enabled bool) {
	sm.mu.Lock()
	if sm.settings.Compaction == nil {
		sm.settings.Compaction = &CompactionSettings{}
	}
	sm.settings.Compaction.Enabled = &enabled
	sm.markModified("compaction.enabled", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetCompactionReserveTokens() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Compaction != nil && sm.settings.Compaction.ReserveTokens != nil {
		return *sm.settings.Compaction.ReserveTokens
	}
	return 16384
}

func (sm *SettingsManager) GetCompactionKeepRecentTokens() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Compaction != nil && sm.settings.Compaction.KeepRecentTokens != nil {
		return *sm.settings.Compaction.KeepRecentTokens
	}
	return 20000
}

func (sm *SettingsManager) GetRetryEnabled() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Retry != nil && sm.settings.Retry.Enabled != nil {
		return *sm.settings.Retry.Enabled
	}
	return true
}

func (sm *SettingsManager) GetRetryMaxRetries() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Retry != nil && sm.settings.Retry.MaxRetries != nil {
		return *sm.settings.Retry.MaxRetries
	}
	return 3
}

func (sm *SettingsManager) GetRetryBaseDelayMs() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Retry != nil && sm.settings.Retry.BaseDelayMs != nil {
		return *sm.settings.Retry.BaseDelayMs
	}
	return 2000
}

func (sm *SettingsManager) GetProviderRetryMaxDelayMs() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Retry != nil && sm.settings.Retry.Provider != nil && sm.settings.Retry.Provider.MaxRetryDelayMs != nil {
		return *sm.settings.Retry.Provider.MaxRetryDelayMs
	}
	return 60000
}

// GetProviderRetrySettings returns the provider-scoped retry settings
// (timeoutMs / maxRetries / maxRetryDelayMs). Nil sub-settings fall back to
// zero values; callers apply their own defaults via derefInt.
func (sm *SettingsManager) GetProviderRetrySettings() ProviderRetrySettings {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Retry != nil && sm.settings.Retry.Provider != nil {
		return *sm.settings.Retry.Provider
	}
	return ProviderRetrySettings{}
}

func (sm *SettingsManager) GetHideThinkingBlock() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.HideThinkingBlock != nil {
		return *sm.settings.HideThinkingBlock
	}
	return false
}

func (sm *SettingsManager) GetShellPath() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.ShellPath != nil {
		return *sm.settings.ShellPath
	}
	return ""
}

func (sm *SettingsManager) GetQuietStartup() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.QuietStartup != nil {
		return *sm.settings.QuietStartup
	}
	return false
}

func (sm *SettingsManager) GetShellCommandPrefix() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.ShellCommandPrefix != nil {
		return *sm.settings.ShellCommandPrefix
	}
	return ""
}

func (sm *SettingsManager) GetNpmCommand() []string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.NpmCommand != nil {
		result := make([]string, len(sm.settings.NpmCommand))
		copy(result, sm.settings.NpmCommand)
		return result
	}
	return nil
}

func (sm *SettingsManager) GetPackages() []PackageSource {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Packages != nil {
		return sm.settings.Packages
	}
	return nil
}

func (sm *SettingsManager) SetPackages(packages []PackageSource) {
	sm.mu.Lock()
	sm.global.Packages = packages
	sm.markModified("packages", "")
	sm.save()
	sm.mu.Unlock()
}

func (sm *SettingsManager) SetProjectPackages(packages []PackageSource) {
	sm.mu.Lock()
	project := cloneSettings(sm.project)
	project.Packages = packages
	sm.markProjectModified("packages","")
	sm.saveProjectSettings(project)
	sm.mu.Unlock()
}

func (sm *SettingsManager) GetExtensionPaths() []string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Extensions != nil {
		return sm.settings.Extensions
	}
	return nil
}

func (sm *SettingsManager) GetSkillPaths() []string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Skills != nil {
		return sm.settings.Skills
	}
	return nil
}

func (sm *SettingsManager) GetPromptTemplatePaths() []string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Prompts != nil {
		return sm.settings.Prompts
	}
	return nil
}

func (sm *SettingsManager) GetEnableSkillCommands() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.EnableSkillCommands != nil {
		return *sm.settings.EnableSkillCommands
	}
	return true
}

func (sm *SettingsManager) GetShowImages() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Terminal != nil && sm.settings.Terminal.ShowImages != nil {
		return *sm.settings.Terminal.ShowImages
	}
	return true
}

func (sm *SettingsManager) GetBlockImages() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Images != nil && sm.settings.Images.BlockImages != nil {
		return *sm.settings.Images.BlockImages
	}
	return false
}

func (sm *SettingsManager) GetEnabledModels() []string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.settings.EnabledModels
}

func (sm *SettingsManager) GetSessionDir() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.SessionDir == nil {
		return ""
	}
	dir := *sm.settings.SessionDir
	if dir == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if len(dir) > 2 && dir[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return home + dir[1:]
	}
	return dir
}

func (sm *SettingsManager) GetThinkingBudgets() *ThinkingBudgetsSettings {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.settings.ThinkingBudgets
}

func (sm *SettingsManager) GetCodeBlockIndent() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Markdown != nil && sm.settings.Markdown.CodeBlockIndent != nil {
		return *sm.settings.Markdown.CodeBlockIndent
	}
	return "  "
}

func (sm *SettingsManager) GetWarnings() WarningSettings {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.settings.Warnings != nil {
		return *sm.settings.Warnings
	}
	return WarningSettings{}
}

// --- Private Helpers ---

func (sm *SettingsManager) recordError(scope SettingsScope, err error){
	sm.errors = append(sm.errors, SettingsError{Scope: scope, Error: err})
}

func (sm *SettingsManager) markModified(field, nestedKey string) {
	sm.modifiedFields[field] = true
	if nestedKey != "" {
		if sm.modifiedNestedFields[field] == nil {
			sm.modifiedNestedFields[field] = make(map[string]bool)
		}
		sm.modifiedNestedFields[field][nestedKey] = true
	}
}

func (sm *SettingsManager) markProjectModified(field, nestedKey string) {
	sm.modifiedProjectFields[field] = true
	if nestedKey != "" {
		if sm.modifiedProjectNestedFields[field] == nil {
			sm.modifiedProjectNestedFields[field] = make(map[string]bool)
		}
		sm.modifiedProjectNestedFields[field][nestedKey] = true
	}
}

func (sm *SettingsManager) save() {
	sm.settings = deepMergeSettings(sm.global, sm.project)
	if sm.globalLoadError != nil {
		return
	}

	snapshotGlobal := cloneSettings(sm.global)
	modifiedFields := make(map[string]bool)
	for k, v := range sm.modifiedFields {
		modifiedFields[k] = v
	}
	modifiedNested := make(map[string]map[string]bool)
	for k, v := range sm.modifiedNestedFields {
		modifiedNested[k] = make(map[string]bool)
		for kk, vv := range v {
			modifiedNested[k][kk] = vv
		}
	}
	sm.writeQueue <- func() {
		sm.mu.Lock()
		defer sm.mu.Unlock()
		sm.persistScopedSettings(ScopeGlobalSetting, snapshotGlobal, modifiedFields, modifiedNested)
		sm.modifiedFields = make(map[string]bool)
		sm.modifiedNestedFields = make(map[string]map[string]bool)
	}
}

func (sm *SettingsManager) saveProjectSettings(projectSettings Settings) {
	sm.project = cloneSettings(projectSettings)
	sm.settings = deepMergeSettings(sm.global, sm.project)
	if sm.projectLoadError != nil {
		return
	}
	snapshotProject := cloneSettings(sm.project)
	modifiedFields := make(map[string]bool)
	for k, v := range sm.modifiedProjectFields {
		modifiedFields[k] = v
	}
	modifiedNested := make(map[string]map[string]bool)
	for k, v := range sm.modifiedProjectNestedFields {
		modifiedNested[k] = make(map[string]bool)
		for kk, vv := range v {
			modifiedNested[k][kk] = vv
		}
	}

	sm.writeQueue <- func() {
		sm.mu.Lock()
		defer sm.mu.Unlock()
		sm.persistScopedSettings(ScopeProjectSetting, snapshotProject, modifiedFields, modifiedNested)
		sm.modifiedProjectFields = make(map[string]bool)
		sm.modifiedProjectNestedFields = make(map[string]map[string]bool)
	}
}

func (sm *SettingsManager) persistScopedSettings(
	scope SettingsScope,
	snapshotSettings Settings,
	modifiedFields map[string]bool,
	modifiedNestedFields map[string]map[string]bool,
) {
	sm.storage.WithLock(scope, func(current *string) *string{
		var currentSettings Settings
		if current != nil {
			currentSettings, _ = parseSettings(*current)
			currentSettings, _ = parseSettings(*current)
		}
		merged := currentSettings
		for field := range modifiedFields {
			// Use JSON round-trip for dynamic field access
			snapData, _ := json.Marshal(snapshotSettings)
			currData, _ := json.Marshal(merged)
			var snapMap, currMap map[string]any
			json.Unmarshal(snapData, &snapMap)
			json.Unmarshal(currData, &currMap)

			if nestedKeys, hasNested := modifiedNestedFields[field]; hasNested {
				baseNested, _ := currMap[field].(map[string]any)
				if baseNested == nil {
					baseNested = make(map[string]any)
				}
				inMemoryNested, _ := snapMap[field].(map[string]any)
				mergedNested := make(map[string]any)
				for k, v := range baseNested {
					mergedNested[k] = v
				}
				for nestedKey := range nestedKeys {
					if inMemoryNested != nil {
						mergedNested[nestedKey] = inMemoryNested[nestedKey]
					}
				}
				currMap[field] = mergedNested
			} else {
				currMap[field] = snapMap[field]
			}

			mergedData, _ := json.Marshal(currMap)
			json.Unmarshal(mergedData, &merged)
		}

		result := settingsToJSON(merged)
		return &result
	}) 
}