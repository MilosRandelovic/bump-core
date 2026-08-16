package updater

import (
	"path/filepath"
	"sort"
	"sync"
)

type referencedFileLock struct {
	mutex      sync.Mutex
	references int
}

var dependencyFileLocks = struct {
	sync.Mutex
	locks map[string]*referencedFileLock
}{locks: make(map[string]*referencedFileLock)}

// lockDependencyFiles serializes the complete prepare/apply transaction for
// overlapping file sets. Sorting canonical paths prevents multi-file deadlocks.
func lockDependencyFiles(paths []string) func() {
	uniquePaths := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		canonicalPath := path
		if absolutePath, err := filepath.Abs(path); err == nil {
			canonicalPath = absolutePath
		}
		if resolvedPath, err := filepath.EvalSymlinks(canonicalPath); err == nil {
			canonicalPath = resolvedPath
		}
		uniquePaths[filepath.Clean(canonicalPath)] = struct{}{}
	}

	keys := make([]string, 0, len(uniquePaths))
	for path := range uniquePaths {
		keys = append(keys, path)
	}
	sort.Strings(keys)

	dependencyFileLocks.Lock()
	locks := make([]*referencedFileLock, 0, len(keys))
	for _, key := range keys {
		lock := dependencyFileLocks.locks[key]
		if lock == nil {
			lock = &referencedFileLock{}
			dependencyFileLocks.locks[key] = lock
		}
		lock.references++
		locks = append(locks, lock)
	}
	dependencyFileLocks.Unlock()

	for _, lock := range locks {
		lock.mutex.Lock()
	}

	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].mutex.Unlock()
		}
		dependencyFileLocks.Lock()
		for index, key := range keys {
			locks[index].references--
			if locks[index].references == 0 {
				delete(dependencyFileLocks.locks, key)
			}
		}
		dependencyFileLocks.Unlock()
	}
}
