package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"appleseum/backend/internal/fault"
)

func TestTransferSlotsShareAccountAndServerBudgets(t *testing.T) {
	var slots transferSlots
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var releases []func()
	for i := 0; i < 16; i++ {
		release, err := slots.acquire(ctx, fmt.Sprint(i/3), true, false, true)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	granted := make(chan func(), 1)
	go func() {
		release, err := slots.acquire(ctx, "next", true, false, true)
		if err == nil {
			granted <- release
		}
	}()
	select {
	case <-granted:
		t.Fatal("server exceeded 16 transfers")
	case <-time.After(20 * time.Millisecond):
	}
	releases[0]()
	select {
	case release := <-granted:
		release()
	case <-ctx.Done():
		t.Fatal("free server slot was not reused")
	}
	for _, release := range releases {
		release()
		release()
	}
	if slots.active != 0 || len(slots.accounts) != 0 {
		t.Fatal("slot leak", slots.active, slots.accounts)
	}
}

func TestSaturatedAccountDoesNotBlockOtherAccounts(t *testing.T) {
	var slots transferSlots
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var releases []func()
	for range 3 {
		release, err := slots.acquire(ctx, "same-account", true, false, true)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	waitingCtx, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() {
		release, err := slots.acquire(waitingCtx, "same-account", true, false, true)
		if err == nil {
			release()
		}
		result <- err
	}()
	release, err := slots.acquire(ctx, "other-account", true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case <-result:
		t.Fatal("account exceeded 3 transfers")
	case <-time.After(20 * time.Millisecond):
	}
	stop()
	if err := <-result; err != context.Canceled {
		t.Fatal(err)
	}
	for _, release := range releases {
		release()
	}
}

func TestImportsUseCommonTransferSlots(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(fmt.Sprint(public), func(t *testing.T) {
			var slots transferSlots
			ctx := context.Background()
			upload, err := slots.acquire(ctx, "owner", public, true, false)
			if err != nil {
				t.Fatal(err)
			}
			if public {
				if _, err := slots.acquire(ctx, "owner", true, true, false); err != fault.ImportBusy {
					t.Fatal("second account import accepted", err)
				}
			}
			a, _ := slots.acquire(ctx, "owner", public, false, true)
			b, _ := slots.acquire(ctx, "owner", public, false, true)
			blocked, stop := context.WithCancel(ctx)
			stop()
			if _, err := slots.acquire(blocked, "owner", public, false, true); err != context.Canceled {
				t.Fatal(err)
			}
			if slots.active != 3 {
				t.Fatal("import bypassed common budget", slots.active)
			}
			b()
			a()
			upload()
		})
	}
}

func TestStorageReservationsAndRecovery(t *testing.T) {
	var mu sync.Mutex
	free := int64(5 << 30)
	guard := storageGuard{space: func(string) (int64, int64, error) { mu.Lock(); defer mu.Unlock(); return free, 20 << 30, nil }}
	a, err := guard.reserve(2 << 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.reserve(3 << 30); err != fault.StorageFull {
		t.Fatal("overcommitted disk", err)
	}
	if err := guard.ready(); err != fault.StorageFull {
		t.Fatal("lost storage pause", err)
	}
	a()
	a()
	if err := guard.ready(); err != nil {
		t.Fatal("did not recover after reservation release", err)
	}
	mu.Lock()
	free = 512 << 20
	mu.Unlock()
	if err := guard.check(); err != fault.StorageFull {
		t.Fatal("used emergency disk headroom", err)
	}
	mu.Lock()
	free = 5 << 30
	mu.Unlock()
	if err := guard.ready(); err != nil {
		t.Fatal("did not recover after freeing disk", err)
	}
}

func TestActualStorageFailurePausesUntilWriteProbeSucceeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Service{storage: storageGuard{directory: dir, space: func(string) (int64, int64, error) { return 10 << 30, 20 << 30, nil }}}
	s.StorageFailure(&os.PathError{Op: "write", Path: "file", Err: syscall.ENOSPC})
	if code := s.StorageError(); code != fault.StorageFull {
		t.Fatal("new work was not paused", code)
	}
	s.storage.mu.Lock()
	s.storage.retryAfter = time.Time{}
	s.storage.mu.Unlock()
	if code := s.StorageError(); code != "" {
		t.Fatal("write probe did not restore capacity", code)
	}
	files, err := os.ReadDir(filepath.Join(dir, "packages"))
	if err != nil || len(files) != 0 {
		t.Fatal("write probe leaked a file", files, err)
	}
}
