package updater

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

type referencedFileLock struct {
	semaphore  chan struct{}
	references int
}

var dependencyFileLocks = struct {
	sync.Mutex
	locks map[string]*referencedFileLock
}{locks: make(map[string]*referencedFileLock)}

func lockDependencyFiles(ctx context.Context, paths []string) (func(), error) {
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

	// Sorting canonical paths prevents multi-file deadlocks across goroutines and processes.
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
			lock = &referencedFileLock{semaphore: make(chan struct{}, 1)}
			dependencyFileLocks.locks[key] = lock
		}
		lock.references++
		locks = append(locks, lock)
	}
	dependencyFileLocks.Unlock()

	acquiredProcessLocks := 0
	crossProcessLocks := make([]*os.File, 0, len(locks))
	for index, lock := range locks {
		select {
		case lock.semaphore <- struct{}{}:
			acquiredProcessLocks++
		case <-ctx.Done():
			releaseAcquiredFileLocks(locks, acquiredProcessLocks, crossProcessLocks)
			releaseFileLockReferences(keys, locks)
			return nil, fmt.Errorf("dependency file lock cancelled: %w", ctx.Err())
		}

		crossProcessLock, err := acquireCrossProcessFileLock(ctx, keys[index])
		if err != nil {
			releaseAcquiredFileLocks(locks, acquiredProcessLocks, crossProcessLocks)
			releaseFileLockReferences(keys, locks)
			return nil, err
		}
		crossProcessLocks = append(crossProcessLocks, crossProcessLock)
	}

	return func() {
		releaseAcquiredFileLocks(locks, len(locks), crossProcessLocks)
		releaseFileLockReferences(keys, locks)
	}, nil
}

func acquireCrossProcessFileLock(ctx context.Context, canonicalPath string) (*os.File, error) {
	lockDirectory := filepath.Join("/tmp", fmt.Sprintf("bump-core-locks-%d", os.Getuid()))
	if err := os.MkdirAll(lockDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create dependency lock directory: %w", err)
	}
	pathHash := sha256.Sum256([]byte(canonicalPath))
	lockPath := filepath.Join(lockDirectory, fmt.Sprintf("%x.lock", pathHash))
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open dependency lock file: %w", err)
	}

	for {
		err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lockFile, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			lockFile.Close()
			return nil, fmt.Errorf("failed to lock dependency file %s: %w", canonicalPath, err)
		}

		retryTimer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			retryTimer.Stop()
			lockFile.Close()
			return nil, fmt.Errorf("dependency file lock cancelled: %w", ctx.Err())
		case <-retryTimer.C:
		}
	}
}

func releaseAcquiredFileLocks(processLocks []*referencedFileLock, acquiredProcessLocks int, crossProcessLocks []*os.File) {
	for index := len(crossProcessLocks) - 1; index >= 0; index-- {
		_ = syscall.Flock(int(crossProcessLocks[index].Fd()), syscall.LOCK_UN)
		_ = crossProcessLocks[index].Close()
	}
	for index := acquiredProcessLocks - 1; index >= 0; index-- {
		<-processLocks[index].semaphore
	}
}

func releaseFileLockReferences(keys []string, locks []*referencedFileLock) {
	dependencyFileLocks.Lock()
	defer dependencyFileLocks.Unlock()
	for index, key := range keys {
		locks[index].references--
		if locks[index].references == 0 {
			delete(dependencyFileLocks.locks, key)
		}
	}
}
