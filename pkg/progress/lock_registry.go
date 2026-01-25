package progress

import (
	"path/filepath"
	"sync"
)

var (
	activeLocksMu sync.RWMutex
	activeLocks   = make(map[string]struct{})
)

// registerActiveLock marks a progress file as locked by this process.
func registerActiveLock(path string) {
	activeLocksMu.Lock()
	activeLocks[canonicalPath(path)] = struct{}{}
	activeLocksMu.Unlock()
}

// unregisterActiveLock removes a progress file lock entry for this process.
func unregisterActiveLock(path string) {
	activeLocksMu.Lock()
	delete(activeLocks, canonicalPath(path))
	activeLocksMu.Unlock()
}

// IsPathLockedByCurrentProcess reports whether this process holds the active lock for path.
func IsPathLockedByCurrentProcess(path string) bool {
	activeLocksMu.RLock()
	_, ok := activeLocks[canonicalPath(path)]
	activeLocksMu.RUnlock()
	return ok
}

func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}
