package tools

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// C15: the mutation-queue map must clean up after the last user releases
// (TS file-mutation-queue.ts:34-38 deletes the entry post-release; Go used to
// grow it forever).
func TestMutationQueueCleansUpAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if _, err := WithFileMutationQueue(path, func() (int, error) { return 1, nil }); err != nil {
		t.Fatalf("WithFileMutationQueue: %v", err)
	}
	key := getMutationQueueKey(path)
	mutationMu.Lock()
	_, still := mutationLocks[key]
	mutationMu.Unlock()
	if still {
		t.Errorf("map entry for %q survived release", key)
	}
}

// C15: the key must be absolutized (TS resolve(filePath)) — different
// spellings of the same file resolve to one key.
func TestMutationQueueKeyAbsolutized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	k1 := getMutationQueueKey(path)
	k2 := getMutationQueueKey(filepath.Join(dir, ".", "f.txt"))
	if k1 != k2 {
		t.Errorf("keys differ: %q vs %q", k1, k2)
	}
	if !filepath.IsAbs(k1) {
		t.Errorf("key %q is not absolute", k1)
	}
}

// C15: mutual exclusion must hold across handoff while waiters exist, and the
// map must clean up once the last leaves. A plain delete-on-unlock would let
// a third arrival construct a second mutex while a second waiter is still
// blocked on the first — two goroutines in the critical section at once.
func TestMutationQueueMutualExclusionUnderContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.txt")
	var inSection int32
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_, _ = WithFileMutationQueue(path, func() (int, error) {
					if atomic.AddInt32(&inSection, 1) > 1 {
						t.Error("two goroutines held the same file's critical section")
					}
					time.Sleep(time.Millisecond) // widen the handoff window
					atomic.AddInt32(&inSection, -1)
					return 0, nil
				})
			}
		}()
	}
	wg.Wait()

	mutationMu.Lock()
	remaining := len(mutationLocks)
	mutationMu.Unlock()
	if remaining != 0 {
		t.Errorf("%d mutation-queue entries leaked after all goroutines finished", remaining)
	}
}
