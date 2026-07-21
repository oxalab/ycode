package extensions

// js_loader.go is the JS/TS analogue of loadExtensionFromConfig. Given a
// .ts/.js/.mjs file path, it:
//   1. Reads the file.
//   2. Transpiles TS→JS via esbuild (js_transpile.go).
//   3. Evaluates the CJS module in qjs to extract the default-export factory.
//   4. Builds the JS-facing `pi` object (js_api.go).
//   5. Invokes the factory, which populates the *Extension maps via pi.on /
//      pi.registerTool / etc.
//   6. Returns the populated *Extension, identical in shape to what the JSON
//      loader produces — so the runner picks it up unchanged.
//
// The shared *JSRuntime (one per LoadExtensions call) is created in
// LoadExtensions and passed in; it's anchored on LoadExtensionsResult.JSRuntime
// so it lives as long as the extensions do.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loadJSExtension loads one .ts/.js/.mjs extension file. jsrt is the shared
// JSRuntime for this load batch; runtime is the shared ExtensionRuntime.
//
// On any failure (read, transpile, eval, factory-throw) it returns an error
// that LoadExtensions surfaces as an ExtensionLoadError — the host keeps
// running and other extensions still load.
func loadJSExtension(extensionPath, cwd string, eventBus EventBus, runtime *ExtensionRuntime, jsrt *JSRuntime) (*Extension, error) {
	resolvedPath := resolvePath(extensionPath, cwd)

	// 1. Read.
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read JS extension: %w", err)
	}

	// 2. Stat for mtime (cache key for transpile).
	mtime := int64(0)
	if fi, err := os.Stat(resolvedPath); err == nil {
		mtime = fi.ModTime().UnixNano()
	}

	// 3. Transpile (no-op for .js/.mjs).
	js, err := TranspileExtension(resolvedPath, data, mtime)
	if err != nil {
		return nil, err
	}

	// 4. Eval the CJS module to get the factory handle.
	factoryHandle, err := jsrt.EvalModule(resolvedPath, js)
	if err != nil {
		return nil, err
	}

	// 5. Build the *Extension + JS-backed `pi` object, then invoke the factory.
	extension := NewExtension(extensionPath, resolvedPath, nil)
	jsAPI, err := BuildJSAPI(jsrt, extension, runtime, cwd, eventBus)
	if err != nil {
		return nil, fmt.Errorf("build pi: %w", err)
	}
	defer jsrt.SafeFree(jsAPI.PIValue())

	// 6. Invoke the factory — this is where the extension calls pi.on/registerTool
	//    and populates the maps.
	if err := jsrt.InvokeFactory(factoryHandle, jsAPI.PIValue()); err != nil {
		return nil, fmt.Errorf("factory failed: %w", err)
	}

	return extension, nil
}

// IsJSExtensionFile reports whether a path is a JS/TS extension file (the
// suffixes the JS loader handles). Used by the suffix-dispatch in
// loadExtensionFromConfig and by isExtensionFile for discovery.
func IsJSExtensionFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".ts") ||
		strings.HasSuffix(lower, ".js") ||
		strings.HasSuffix(lower, ".mjs") ||
		strings.HasSuffix(lower, ".cjs")
}

// jsExtensionFileNames lists the conventional entry-point files for a
// JS-extension subdirectory, checked in order by resolveExtensionEntries.
var jsExtensionFileNames = []string{"index.ts", "index.js", "index.mjs"}

// hasJSExtensionIndex returns the first existing JS index file in dir, or "".
func hasJSExtensionIndex(dir string) string {
	for _, name := range jsExtensionFileNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
