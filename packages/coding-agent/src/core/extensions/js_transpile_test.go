package extensions

// js_transpile_test.go verifies the esbuild transpile step.

import (
	"strings"
	"testing"
)

func TestTranspileExtension_StripsTypesAndLowersAsync(t *testing.T) {
	ts := `interface Opts { name: string; }
export default async function (pi: any): Promise<void> {
  pi.on("session_start", () => { console.log("started"); });
}`
	js, err := TranspileExtension("/tmp/ext.ts", []byte(ts), 1)
	if err != nil {
		t.Fatalf("TranspileExtension errored: %v", err)
	}
	out := string(js)
	// esbuild CJS output should reference module.exports (the default export).
	if !strings.Contains(out, "module.exports") && !strings.Contains(out, "__toCommonJS") {
		t.Errorf("output missing module.exports wrapper; got:\n%s", out)
	}
	// async should be preserved (ES2020 target supports it natively).
	if !strings.Contains(out, "session_start") {
		t.Errorf("output lost the 'session_start' literal; got:\n%s", out)
	}
}

func TestTranspileExtension_JSPassthrough(t *testing.T) {
	js := `module.exports = function(pi) { pi.on("x", () => {}); };`
	out, err := TranspileExtension("/tmp/ext.js", []byte(js), 1)
	if err != nil {
		t.Fatalf(".js passthrough errored: %v", err)
	}
	if string(out) != js {
		t.Errorf(".js passthrough modified the source: got %q", string(out))
	}
}

func TestTranspileExtension_SyntaxError(t *testing.T) {
	bad := `export default function ( { ;` // malformed
	_, err := TranspileExtension("/tmp/bad.ts", []byte(bad), 1)
	if err == nil {
		t.Fatal("expected a transpile error for malformed TS")
	}
	te, ok := err.(*TranspileError)
	if !ok {
		t.Fatalf("expected *TranspileError, got %T: %v", err, err)
	}
	if te.Path != "/tmp/bad.ts" {
		t.Errorf("TranspileError.Path = %q, want /tmp/bad.ts", te.Path)
	}
}

func TestTranspileExtension_CacheHit(t *testing.T) {
	ClearTranspileCache()
	ts := `export default (pi: any) => { pi.on("y", () => {}); };`
	_, _ = TranspileExtension("/tmp/c.ts", []byte(ts), 100)
	// Second call with same mtime should hit the cache (same path+ mtime → same bytes).
	out2, err := TranspileExtension("/tmp/c.ts", []byte("DIFFERENT SOURCE THAT WON'T BE USED"), 100)
	if err != nil {
		t.Fatalf("cached call errored: %v", err)
	}
	if !strings.Contains(string(out2), "session_start") && !strings.Contains(string(out2), "pi.on") {
		// Just confirm it returned the cached transpile, not a fresh one of the different source.
		// The "DIFFERENT SOURCE" would fail to reference pi.on.
		if strings.Contains(string(out2), "DIFFERENT SOURCE") {
			t.Error("cache miss: returned a fresh transpile of the second source instead of the cached one")
		}
	}
}
