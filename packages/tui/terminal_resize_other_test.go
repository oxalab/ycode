//go:build !unix

package tui

import (
	"sync"
	"testing"
	"time"
)

// Resize-responsiveness R1: on non-Unix there was NO resize notification at
// all (empty stub, false "engine polls" comment) — a terminal resize only
// repainted when an unrelated event fired a frame. The poll must fire the
// resize handler on size change and stop on teardown. Driven through the
// documented COLUMNS fallback of Columns() (test stdout is not a console).
func TestResizePollFiresHandlerOnChange(t *testing.T) {
	orig := resizePollInterval
	resizePollInterval = 2 * time.Millisecond
	t.Cleanup(func() { resizePollInterval = orig })

	t.Setenv("COLUMNS", "100")

	pt := NewProcessTerminal()
	var mu sync.Mutex
	fires := 0
	pt.resizeHandler = func() {
		mu.Lock()
		fires++
		mu.Unlock()
	}

	pt.setupResize()
	time.Sleep(60 * time.Millisecond) // several ticks at baseline size

	mu.Lock()
	n := fires
	mu.Unlock()
	if n != 0 {
		t.Fatalf("baseline: handler fired %d times, want 0", n)
	}

	t.Setenv("COLUMNS", "120")
	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	n = fires
	mu.Unlock()
	if n == 0 {
		t.Fatal("resize handler never fired after COLUMNS change")
	}

	pt.teardownResize()
	mu.Lock()
	fires = 0
	mu.Unlock()
	time.Sleep(60 * time.Millisecond)

	mu.Lock()
	n = fires
	mu.Unlock()
	if n != 0 {
		t.Fatalf("handler fired %d times after teardownResize, want 0", n)
	}
}
