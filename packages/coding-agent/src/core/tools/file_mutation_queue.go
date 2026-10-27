package tools

import (
	"path/filepath"
	"sync"
)

// Go port of reference core/tools/file-mutation-queue.ts: serializes file
// mutations targeting the same file (resolved to a real path). Map entries are
// refcounted and deleted when the last user releases — TS deletes the queue
// after release (file-mutation-queue.ts:34-38); the Go translation needs the
// refcount because a plain delete would let a third arrival build a second
// mutex while a second waiter is still blocked on the first (mutual exclusion
// would break mid-handoff).

type mutationEntry struct {
	mu   *sync.Mutex
	refs int
}

var (
	mutationMu    sync.Mutex
	mutationLocks = make(map[string]*mutationEntry)
)

// getMutationLock acquires the per-file entry, bumping its refcount. The
// caller must call releaseMutationLock when done.
func getMutationLock(key string) (*sync.Mutex, *mutationEntry) {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	if entry, ok := mutationLocks[key]; ok {
		entry.refs++
		return entry.mu, entry
	}
	entry := &mutationEntry{mu: &sync.Mutex{}, refs: 1}
	mutationLocks[key] = entry
	return entry.mu, entry
}

// releaseMutationLock drops one reference and deletes the entry when it was
// the last — mirroring TS's `if (queues.get(key) === chainedQueue) delete`.
func releaseMutationLock(entry *mutationEntry, key string) {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	entry.refs--
	if entry.refs <= 0 && mutationLocks[key] == entry {
		delete(mutationLocks, key)
	}
}

// getMutationQueueKey resolves the file to its real path (TS
// `resolve(filePath)` + `realpathSync.native` — absolutize against the
// process cwd, then resolve symlinks).
func getMutationQueueKey(filePath string) string {
	resolved, err := filepath.Abs(filePath)
	if err != nil {
		resolved = filepath.Clean(filePath)
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		return real
	}
	return resolved
}

// WithFileMutationQueue: Serialized file mutation operations targeting the same file
// Operations for different files still run in parallel
func WithFileMutationQueue[T any](filePath string, fn func() (T, error)) (T, error) {
	key := getMutationQueueKey(filePath)
	lock, entry := getMutationLock(key)
	lock.Lock()
	defer lock.Unlock()
	defer releaseMutationLock(entry, key)
	return fn()
}
