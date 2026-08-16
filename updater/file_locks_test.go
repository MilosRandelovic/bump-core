package updater

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCrossProcessFileLock(t *testing.T) {
	filePath := os.Getenv("BUMP_CORE_LOCK_TEST_PATH")
	if filePath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		lockFile, err := acquireCrossProcessFileLock(ctx, filePath)
		if lockFile != nil {
			releaseAcquiredFileLocks(nil, 0, []*os.File{lockFile})
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cross-process lock error = %v, expected deadline", err)
		}
		return
	}

	filePath = filepath.Join(t.TempDir(), "package.json")
	lockFile, err := acquireCrossProcessFileLock(context.Background(), filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseAcquiredFileLocks(nil, 0, []*os.File{lockFile})

	command := exec.Command(os.Args[0], "-test.run=^TestCrossProcessFileLock$")
	command.Env = append(os.Environ(), "BUMP_CORE_LOCK_TEST_PATH="+filePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cross-process lock helper failed: %v\n%s", err, output)
	}
}

func TestLockDependencyFilesHonoursCancellationWhileWaiting(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	unlock, err := lockDependencyFiles(context.Background(), []string{filePath})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lockDependencyFiles(ctx, []string{filePath}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lock error = %v, expected cancellation", err)
	}
	unlock()

	unlockAgain, err := lockDependencyFiles(context.Background(), []string{filePath})
	if err != nil {
		t.Fatalf("lock remained unavailable after cancellation: %v", err)
	}
	unlockAgain()
}
