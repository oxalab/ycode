package tools

import (
	"path/filepath"
	"sync"
)

var (
	mutationMu sync.Mutex
	mutationLocks = make(map[string]*sync.Mutex)
)


func getMutationLock(key string) *sync.Mutex {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	if lock, ok := mutationLocks[key]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	mutationLocks[key] = lock
	return lock
}

func getMutationQueueKey(filePath string) string {
	resolved := filepath.Clean(filePath)
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		return real
	}
	return resolved
}

// WithFileMutationQueue: Serialized file mutation operations targeting the same file
// Operations for different files still run in parallel
func WithFileMutationQueue[T any](filePath string, fn func() (T, error)) (T, error) {
	key := getMutationQueueKey(filePath)
	lock := getMutationLock(key)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}