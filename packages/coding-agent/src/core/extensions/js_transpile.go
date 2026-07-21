package extensions

// js_transpile.go handles TypeScript → JavaScript transpilation for JS/TS
// extension loading. Uses esbuild's native Go API (github.com/evanw/esbuild/pkg/api)
// to strip types and lower syntax to ES2020, which the qjs runtime accepts.
//
// Plain .js/.mjs files skip transpilation entirely (passed through unchanged).

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

// transpileCache memoizes transpiled output by (path, mtime) so unchanged
// extensions aren't re-transpiled on every reload.
var (
	transpileCacheMu sync.Mutex
	transpileCache   = map[string]transpileCacheEntry{}
)

type transpileCacheEntry struct {
	mtime int64
	code  []byte
}

// TranspileExtension transforms a .ts extension file to ES2020 CommonJS JS.
// .js/.mjs files are returned as-is (no transform needed). Returns an error
// if esbuild reports syntax errors.
func TranspileExtension(absPath string, source []byte, mtime int64) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(absPath))

	// Plain JS passes through unchanged.
	if ext == ".js" || ext == ".mjs" {
		return source, nil
	}

	// Cache hit?
	transpileCacheMu.Lock()
	if entry, ok := transpileCache[absPath]; ok && entry.mtime == mtime {
		transpileCacheMu.Unlock()
		return entry.code, nil
	}
	transpileCacheMu.Unlock()

	// Transpile via esbuild. Format=CommonJS because qjs's Eval with TypeGlobal
	// runs the source as a script (not an ES module), and CJS wraps the
	// export/default machinery in a `module.exports = {...}` shape we can
	// extract from the global scope.
	result := api.Transform(string(source), api.TransformOptions{
		Loader: api.LoaderTS,
		Target: api.ES2020,
		Format: api.FormatCommonJS,
		Platform: api.PlatformNeutral,
		Sourcefile: filepath.Base(absPath),
	})
	if len(result.Errors) > 0 {
		msgs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			msgs = append(msgs, e.Text)
		}
		return nil, &TranspileError{Path: absPath, Messages: msgs}
	}

	code := result.Code

	// Store in cache.
	transpileCacheMu.Lock()
	transpileCache[absPath] = transpileCacheEntry{mtime: mtime, code: code}
	transpileCacheMu.Unlock()

	return code, nil
}

// TranspileError reports esbuild syntax errors during transpilation.
type TranspileError struct {
	Path     string
	Messages []string
}

func (e *TranspileError) Error() string {
	if len(e.Messages) == 1 {
		return "transpile " + e.Path + ": " + e.Messages[0]
	}
	return "transpile " + e.Path + ": " + strings.Join(e.Messages, "; ")
}

// ClearTranspileCache drops all cached transpiled output. Used by tests and
// full-reload scenarios.
func ClearTranspileCache() {
	transpileCacheMu.Lock()
	transpileCache = map[string]transpileCacheEntry{}
	transpileCacheMu.Unlock()
}
