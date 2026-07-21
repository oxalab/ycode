package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	configDirName = ".ycode"
	agentDirName  = func() string { return "" }
)

func SetConfigDirName(name string)     { configDirName = name }
func SetAgentDirName(fn func() string) { agentDirName = fn }

// --- Extension Runtime ---

func CreateExtensionRuntime() *ExtensionRuntime {
	runtime := &ExtensionRuntime{
		FlagValues:                   make(map[string]any),
		PendingProviderRegistrations: nil,
		Actions:                      nil,
		ContextActions:               nil,
		CommandActions:               nil,
	}

	runtime.AssertActive = func() error {
		return nil
	}
	runtime.Invalidate = func(message string) {}
	runtime.RegisterProviderFn = func(name string, config *ProviderConfig, extensionPath string) {
		runtime.PendingProviderRegistrations = append(runtime.PendingProviderRegistrations, ProviderRegistration{
			Name:          name,
			Config:        config,
			ExtensionPath: extensionPath,
		})
	}

	runtime.UnregisterProviderFn = func(name string) {
		var filtered []ProviderRegistration
		for _, r := range runtime.PendingProviderRegistrations {
			if r.Name != name {
				filtered = append(filtered, r)
			}
		}
		runtime.PendingProviderRegistrations = filtered
	}
	return runtime
}

//  --- Extension API ---

type extensionAPI struct {
	extension *Extension
	runtime   *ExtensionRuntime
	cwd       string
	eventBus  EventBus
}

type EventBus interface {
	Emit(channel string, data any)
	On(channel string, handler func(data any)) func()
}

func createExtensionAPI(extension *Extension, runtime *ExtensionRuntime, cwd string, eventBus EventBus) ExtensionAPI {
	return &extensionAPI{
		extension: extension,
		runtime:   runtime,
		cwd:       cwd,
		eventBus:  eventBus,
	}
}

func (api *extensionAPI) On(eventType string, handler ExtensionHandler) {
	api.runtime.AssertActive()
	api.extension.Handlers[eventType] = append(api.extension.Handlers[eventType], handler)
}

func (api *extensionAPI) RegisterTool(definition *ToolDefinition, sourceInfo any) {
	api.runtime.AssertActive()
	api.extension.Tools[definition.Name] = &RegisteredTool{
		Definition: definition,
		SourceInfo: sourceInfo,
	}
}

func (api *extensionAPI) RegisterCommand(name string, handler func(args string, ctx context.Context) error, options map[string]any) {
	api.runtime.AssertActive()
	cmd := &RegisteredCommand{
		Name: 		name,
		SourceInfo: api.extension.SourceInfo,
		Handler: 	handler,
	}
	if desc, ok := options["description"].(string); ok {
		cmd.Description = desc
	}
	if completions, ok := options["getArgumentCompletions"].(func() []string); ok {
		cmd.GetArgumentCompletions = completions
	}
	api.extension.Commands[name] = cmd
}

func (api *extensionAPI) RegisterShortcut(shortcut string, handler func(ctx context.Context) error, options map[string]any) {
	api.runtime.AssertActive()
	s := &ExtensionShortcut{
		Shortcut: 	shortcut,
		Handler: 	handler,
		ExtensionPath: api.extension.Path,
	}
	if desc, ok := options["description"].(string); ok {
		s.Description = desc
	}
	api.extension.Shortcuts[shortcut] = s
}

func (api *extensionAPI) RegisterFlag(flag *ExtensionFlag) {
	api.runtime.AssertActive()
	flag.ExtensionPath = api.extension.Path
	api.extension.Flags[flag.Name] = flag
	if flag.Default != nil {
		if _, exists := api.runtime.FlagValues[flag.Name]; !exists {
			api.runtime.FlagValues[flag.Name] = flag.Default
		}
	}
}

func (api *extensionAPI) RegisterMessageRenderer(customType string, renderer MessageRenderer) {
	api.runtime.AssertActive()
	api.extension.MessageRenderers[customType] = renderer
}

func (api *extensionAPI) SendMessage(msg any) error {
	api.runtime.AssertActive()
	if api.runtime.Actions != nil && api.runtime.Actions.SendMessage != nil {
		return api.runtime.Actions.SendMessage(msg)
	}
	return nil
}

func (api *extensionAPI) SendUserMessage(text string, images []any) error {
	api.runtime.AssertActive()
	if api.runtime.Actions != nil && api.runtime.Actions.SendUserMessage != nil {
		return api.runtime.Actions.SendUserMessage(text, images)
	}
	return nil
}

func (api *extensionAPI) RegisterProvider(name string, config *ProviderConfig) {
	api.runtime.AssertActive()
	api.runtime.RegisterProviderFn(name, config, api.extension.Path)
}

func (api *extensionAPI) UnregisterProvider(name string) {
	api.runtime.AssertActive()
	api.runtime.UnregisterProviderFn(name)
}

// --- Path Helpers ---

func expandPath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(trimmed, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, trimmed[2:])
	}
	if strings.HasPrefix(trimmed, "~") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, trimmed[1:])
	}
	return trimmed
}

func resolvePath(extPath, cwd string) string {
	expanded := expandPath(extPath)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(cwd, expanded)
}


//  --- Manifest ---

type ycodeManifest struct {
	Extensions 	[]string 	`json:"extensions,omitempty"`
	Themes 		[]string 	`json:"themes,omitempty"`
	Skills 		[]string 	`json:"skills,omitempty"`
	Prompts 	[]string   	`json:"prompts,omitempty"`
}

func readYcodeManifest(packageJsonPath string) *ycodeManifest {
	data, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return nil
	}
	var pkg struct {
		Ycode 	*ycodeManifest 	`json:"ycode"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Ycode
}

func isExtensionFile(name string) bool {
	return strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".so") || IsJSExtensionFile(name)
}

func resolveExtensionEntries(dir string) []string {
	// Check for package.json with "ycode" field
	packageJsonPath := filepath.Join(dir, "package.json")
	if _, err := os.Stat(packageJsonPath); err == nil {
		manifest := readYcodeManifest(packageJsonPath)
		if manifest != nil && len(manifest.Extensions) > 0 {
			var entries []string
			for _, extPath := range manifest.Extensions {
				resolved := filepath.Join(dir, extPath)
				if _, err := os.Stat(resolved); err == nil {
					entries = append(entries, resolved)
				}
			}
			if len(entries) > 0 {
				return entries
			}
		}
	}

	// Check for index.go (Go plugin convention)
	indexPath := filepath.Join(dir, "index.go")
	if _, err := os.Stat(indexPath); err == nil {
		return []string{indexPath}
	}
	// Check for JS/TS index files (index.ts / index.js / index.mjs).
	if jsIdx := hasJSExtensionIndex(dir); jsIdx != "" {
		return []string{jsIdx}
	}
	return nil
}

func discoverExtensionsInDir(dir string) []string {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var discovered []string
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())

		// Direct files
		if !entry.IsDir() && isExtensionFile(entry.Name()) {
			discovered = append(discovered, entryPath)
			continue
		}
		// Subdirectories
		if entry.IsDir() {
			subEntries := resolveExtensionEntries(entryPath)
			if len(subEntries) > 0 {
				discovered = append(discovered, subEntries...)
			}
		}
	}
	return discovered
}

// --- Loading ---

func LoadExtensionFromFactory(
	factory 	ExtensionFactory,
	cwd 		string,
	eventBus 	EventBus,
	runtime 	*ExtensionRuntime,
) (*Extension, error) {
	extension := NewExtension("<inline>", "<inline>", nil)
	api := createExtensionAPI(extension, runtime, cwd, eventBus)
	if err := factory(api); err != nil {
		return nil, err
	}
	return extension, nil
}

func LoadExtensions(paths []string, cwd string, eventBus EventBus) *LoadExtensionsResult {
	var extensions []*Extension
	var errors []ExtensionLoadError

	runtime := CreateExtensionRuntime()

	// Create one shared JSRuntime for this load batch — it's needed only if any
	// path is a JS/TS file. Lazily created on first JS load to avoid spinning
	// up qjs (and its WASM runtime) when all extensions are JSON.
	var jsrt *JSRuntime
	for _, extPath := range paths {
		// Suffix-dispatch: JS/TS files go through the JS loader (sharing the
		// JSRuntime); everything else stays on the JSON path unchanged.
		if IsJSExtensionFile(extPath) {
			if jsrt == nil {
				jr, err := NewJSRuntime()
				if err != nil {
					errors = append(errors, ExtensionLoadError{Path: extPath, Error: "create JS runtime: " + err.Error()})
					continue
				}
				jsrt = jr
				jsrt.SetCWD(cwd)
			}
			extension, err := loadJSExtension(extPath, cwd, eventBus, runtime, jsrt)
			if err != nil {
				errors = append(errors, ExtensionLoadError{Path: extPath, Error: err.Error()})
				continue
			}
			if extension != nil {
				extensions = append(extensions, extension)
			}
			continue
		}
		extension, err := loadExtensionFromConfig(extPath, cwd, eventBus, runtime)
		if err != nil {
			errors = append(errors, ExtensionLoadError{Path: extPath, Error: err.Error()})
			continue
		}
		if extension != nil {
			extensions = append(extensions, extension)
		}
	}

	result := &LoadExtensionsResult{
		Extensions: extensions,
		Errors:     errors,
		Runtime:    runtime,
	}
	// Only anchor the JSRuntime when one was actually created. Assigning a nil
	// *JSRuntime to the `any` field would produce a non-nil interface wrapping
	// a nil pointer (the typed-nil-in-interface pitfall), breaking nil checks.
	if jsrt != nil {
		result.JSRuntime = jsrt
	}
	return result
}

func loadExtensionFromConfig(extensionPath, cwd string, eventBus EventBus, runtime *ExtensionRuntime) (*Extension, error) {
	resolvedPath := resolvePath(extensionPath, cwd)

	// In Go, extensions are config-driven (JSON), not dynamically loaded code
	// Try loading as a JSON config file
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read extension config: %w", err)
	}

	var config struct {
		Tools 	[]ToolDefinition 	`json:"tools"`
		Commands []RegisteredCommand `json:"commands"`
		Flags 	[]ExtensionFlag  	`json:"flags"`
		Provider *ProviderConfig 	`json:"provider"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse extension config: %w", err)
	}

	extension := NewExtension(extensionPath, resolvedPath, nil)
	api := createExtensionAPI(extension, runtime, cwd, eventBus)

	for i := range config.Tools {
		api.RegisterTool(&config.Tools[i], nil)
	}
	for _, cmd := range config.Commands {
		api.RegisterCommand(cmd.Name, cmd.Handler, map[string]any{"description": cmd.Description})
	}
	for _, flag := range config.Flags {
		api.RegisterFlag(&flag)
	}
	if config.Provider != nil {
		api.RegisterProvider(config.Provider.Name, config.Provider)
	}

	return extension, nil
}

func DiscoverAndLoadExtensions(
	configuredPaths []string,
	cwd string,
	agentDir string,
	eventBus EventBus,
) *LoadExtensionsResult {
	var allPaths []string
	seen := make(map[string]bool)

	addPaths := func(paths []string) {
		for _, p := range paths {
			abs, _ := filepath.Abs(p)
			if !seen[abs] {
				seen[abs] = true
				allPaths = append(allPaths, p)
			}
		}
	}

	// 1.Project-local: cwd/.ycode/extensions
	localExtDir := filepath.Join(cwd, configDirName, "extensions")
	addPaths(discoverExtensionsInDir(localExtDir))

	// 2. Global: agentDir/extensions
	globalExtDir := filepath.Join(agentDir, "extensions")
	addPaths(discoverExtensionsInDir(globalExtDir))

	// 3. Explicitly configured paths
	for _, p := range configuredPaths {
		resolved := resolvePath(p, cwd) 
		if stat, err := os.Stat(resolved); err == nil && stat.IsDir() {
			entries := resolveExtensionEntries(resolved)
			if len(entries) > 0 {
				addPaths(entries)
				continue
			}
			addPaths(discoverExtensionsInDir(resolved))
			continue
		}
		addPaths([]string{resolved})
	} 
	return LoadExtensions(allPaths, cwd, eventBus)
}